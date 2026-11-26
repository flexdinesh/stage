package db_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// pairMachine pairs a new remote machine and returns its grant.
func pairMachine(t *testing.T, s *db.Store, ctx context.Context, label string) db.MachineGrant {
	t.Helper()
	p, err := s.CreatePairing(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.RedeemPairing(ctx, p.Code, label, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestRedeemPairingCreatesMachineAndToken(t *testing.T) {
	s, ctx := setup(t)
	p, err := s.CreatePairing(secondUser(t, ctx), "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.Code, "stage_pc_") || time.Until(p.ExpiresAt) > db.PairingTTL || time.Until(p.ExpiresAt) < db.PairingTTL-5*time.Second {
		t.Fatalf("pairing %+v", p)
	}
	g, err := s.RedeemPairing(ctx, " "+p.Code+" ", "  laptop ", "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if g.MachineID == db.LocalMachine || g.Label != "laptop" || !db.IsMachineToken(g.Token) {
		t.Fatalf("grant %+v", g)
	}
	m, err := s.GetMachineInfo(ctx, g.MachineID)
	if err != nil || m.Label != "laptop" || m.CreatedBy != 2 || m.Version != "1.2.3" || m.LastSeenAt == nil || len(m.Connections) != 0 {
		t.Fatalf("machine %+v %v", m, err)
	}
	if id, err := s.AuthenticateMachine(ctx, g.Token); err != nil || id != g.MachineID {
		t.Fatalf("authenticate %d %v", id, err)
	}
	ms, err := s.ListMachines(ctx)
	if err != nil || len(ms) != 2 || ms[0].ID != db.LocalMachine || ms[1].ID != g.MachineID {
		t.Fatalf("machines %+v %v", ms, err)
	}
	if ms[0].LastSeenAt != nil || ms[0].CreatedBy != db.DefaultUser {
		t.Fatalf("local machine %+v", ms[0])
	}
}

func TestRedeemPairingExpires(t *testing.T) {
	s, ctx := setup(t)
	p, err := s.CreatePairing(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testDB.Pool.ExecContext(ctx, `UPDATE pairing_codes SET expires_at=strftime('%Y-%m-%dT%H:%M:%f000Z','now','-1 second') WHERE id=?`, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RedeemPairing(ctx, p.Code, "", ""); !errors.Is(err, db.ErrInvalidPairing) {
		t.Fatalf("expired: %v", err)
	}
	if ms, _ := s.ListMachines(ctx); len(ms) != 1 {
		t.Fatalf("machine created from an expired code: %+v", ms)
	}
}

func TestCreatePairingPrunesOldCodes(t *testing.T) {
	s, ctx := setup(t)
	old, err := s.CreatePairing(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testDB.Pool.ExecContext(ctx, `UPDATE pairing_codes SET expires_at=strftime('%Y-%m-%dT%H:%M:%f000Z','now','-2 days') WHERE id=?`, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreatePairing(ctx, ""); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := testDB.Pool.QueryRowContext(ctx, `SELECT count(*) FROM pairing_codes WHERE id=?`, old.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("old code kept: %d %v", n, err)
	}
}

func TestRemoveMachineRevokesTokenAndRetiresConnections(t *testing.T) {
	s, ctx := setup(t)
	g := pairMachine(t, s, ctx, "laptop")
	keep := pairMachine(t, s, ctx, "desktop")
	stable := harness.Descriptor{Kind: "stable", Name: "Stable", Sessions: harness.SessionsMachine, StableIdentity: true}
	loose := harness.Descriptor{Kind: "loose", Name: "Loose", Sessions: harness.SessionsMachine}
	used, err := s.SwitchInstance(ctx, g.MachineID, stable, "env-a", "env a", 0)
	if err != nil {
		t.Fatal(err)
	}
	unused, err := s.SwitchInstance(ctx, g.MachineID, loose, "http://oc", "oc", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testDB.Pool.ExecContext(ctx, `INSERT INTO session_identities(connection_id,external_id) VALUES(?,'ses')`, used.New); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.GetMachineInfo(ctx, g.MachineID); len(m.Connections) != 2 {
		t.Fatalf("connections before removal %+v", m.Connections)
	}
	if _, err := s.SetDefaultConnection(ctx, &used.New); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveMachine(ctx, g.MachineID); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.GetPreferences(ctx); p.DefaultConnectionID != nil {
		t.Fatalf("removed machine's connection still the default: %d", *p.DefaultConnectionID)
	}
	if _, err := s.AuthenticateMachine(ctx, g.Token); !errors.Is(err, db.ErrInvalidMachineToken) {
		t.Fatalf("token after removal: %v", err)
	}
	if _, err := s.AuthenticateMachine(ctx, keep.Token); err != nil {
		t.Fatalf("other machine's token: %v", err)
	}
	if c, err := s.GetHarnessConnection(ctx, used.New); err != nil || c.Active || c.RetiredAt == nil || c.Label != "env a" {
		t.Fatalf("referenced connection %+v %v", c, err)
	}
	if _, err := s.GetHarnessConnection(ctx, unused.New); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("unreferenced connection: %v", err)
	}
	if s.ActiveID(g.MachineID, "stable") != 0 {
		t.Fatal("removed machine still has an active connection")
	}
	if _, err := s.GetMachineInfo(ctx, g.MachineID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("get removed: %v", err)
	}
	if m, err := s.GetMachine(ctx, g.MachineID); err != nil || m.Label != "laptop" {
		t.Fatalf("history keeps the label: %+v %v", m, err)
	}
	if ms, _ := s.ListMachines(ctx); len(ms) != 2 || ms[1].ID != keep.MachineID {
		t.Fatalf("list after removal %+v", ms)
	}
	if err := s.RenameMachine(ctx, g.MachineID, "x"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("rename removed: %v", err)
	}
	if err := s.RemoveMachine(ctx, g.MachineID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("remove twice: %v", err)
	}
}

func TestLocalMachineCannotBeRemoved(t *testing.T) {
	s, ctx := setup(t)
	var conflict *db.ConflictError
	if err := s.RemoveMachine(ctx, db.LocalMachine); !errors.As(err, &conflict) || conflict.Code != "local_machine" {
		t.Fatalf("remove local: %v", err)
	}
	if _, err := testDB.Pool.ExecContext(ctx, `INSERT INTO machine_tokens(machine_id,secret_hash) VALUES(1,zeroblob(32))`); err == nil {
		t.Fatal("local machine got a token")
	}
	if ms, _ := s.ListMachines(ctx); len(ms) != 1 || ms[0].ID != db.LocalMachine {
		t.Fatalf("machines %+v", ms)
	}
}

func TestPairingStatus(t *testing.T) {
	s, ctx := setup(t)
	p, err := s.CreatePairing(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.PairingStatus(ctx, p.ID)
	if err != nil || st.State != "waiting" || st.MachineID != nil || !st.ExpiresAt.Equal(p.ExpiresAt) {
		t.Fatalf("waiting: %+v %v", st, err)
	}
	g, err := s.RedeemPairing(ctx, p.Code, "desk", "")
	if err != nil {
		t.Fatal(err)
	}
	st, err = s.PairingStatus(ctx, p.ID)
	if err != nil || st.State != "joined" || st.MachineID == nil || *st.MachineID != g.MachineID {
		t.Fatalf("joined: %+v %v", st, err)
	}
	// A joined code stays joined after its expiry.
	if _, err := testDB.Pool.ExecContext(ctx, `UPDATE pairing_codes SET expires_at=strftime('%Y-%m-%dT%H:%M:%f000Z','now','-1 second') WHERE id=?`, p.ID); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.PairingStatus(ctx, p.ID); st.State != "joined" {
		t.Fatalf("joined after expiry: %+v", st)
	}
	late, err := s.CreatePairing(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testDB.Pool.ExecContext(ctx, `UPDATE pairing_codes SET expires_at=strftime('%Y-%m-%dT%H:%M:%f000Z','now','-1 second') WHERE id=?`, late.ID); err != nil {
		t.Fatal(err)
	}
	if st, err := s.PairingStatus(ctx, late.ID); err != nil || st.State != "expired" || st.MachineID != nil {
		t.Fatalf("expired: %+v %v", st, err)
	}
	if _, err := s.PairingStatus(ctx, late.ID+100); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}
