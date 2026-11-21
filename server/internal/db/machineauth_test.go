package db_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
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

func TestRedeemPairingLabels(t *testing.T) {
	s, ctx := setup(t)
	p, err := s.CreatePairing(ctx, " desk ")
	if err != nil || p.Label != "desk" {
		t.Fatalf("pairing %+v %v", p, err)
	}
	if g, err := s.RedeemPairing(ctx, p.Code, "hostname", ""); err != nil || g.Label != "desk" {
		t.Fatalf("pairing label wins: %+v %v", g, err)
	}
	g := pairMachine(t, s, ctx, "")
	if g.Label != "Machine "+strconv.FormatInt(g.MachineID, 10) {
		t.Fatalf("default label %+v", g)
	}
	if _, err := s.CreatePairing(ctx, strings.Repeat("x", 101)); !isInvalid(err) {
		t.Fatalf("long label: %v", err)
	}
}

func TestRedeemPairingIsSingleUse(t *testing.T) {
	s, ctx := setup(t)
	p, err := s.CreatePairing(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RedeemPairing(ctx, p.Code, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RedeemPairing(ctx, p.Code, "", ""); !errors.Is(err, db.ErrInvalidPairing) {
		t.Fatalf("reuse: %v", err)
	}
	if ms, _ := s.ListMachines(ctx); len(ms) != 2 {
		t.Fatalf("machines after reuse %+v", ms)
	}
}

func TestRedeemPairingConcurrentSingleWinner(t *testing.T) {
	s, ctx := setup(t)
	p, err := s.CreatePairing(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	won, lost := 0, 0
	for range 8 {
		wg.Go(func() {
			_, err := s.RedeemPairing(ctx, p.Code, "", "")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				won++
			case errors.Is(err, db.ErrInvalidPairing):
				lost++
			default:
				t.Errorf("redeem: %v", err)
			}
		})
	}
	wg.Wait()
	if won != 1 || lost != 7 {
		t.Fatalf("won %d lost %d", won, lost)
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

func TestRedeemPairingRejectsWrongCodes(t *testing.T) {
	s, ctx := setup(t)
	p, err := s.CreatePairing(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreatePairing(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	_, secret, _ := strings.Cut(strings.TrimPrefix(p.Code, "stage_pc_"), "_")
	flipped := p.Code[:len(p.Code)-1] + flip(p.Code[len(p.Code)-1])
	for name, code := range map[string]string{
		"wrong secret, same length": flipped,
		"truncated secret":          p.Code[:len(p.Code)-4],
		"longer secret":             p.Code + "a",
		"secret of another code":    "stage_pc_" + strconv.FormatInt(other.ID, 10) + "_" + secret,
		"missing secret":            "stage_pc_" + strconv.FormatInt(p.ID, 10) + "_",
		"token prefix":              "stage_mt_" + strconv.FormatInt(p.ID, 10) + "_" + secret,
		"garbage":                   "hello",
		"empty":                     "",
	} {
		if _, err := s.RedeemPairing(ctx, code, "", ""); !errors.Is(err, db.ErrInvalidPairing) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Failed attempts do not burn the code.
	if _, err := s.RedeemPairing(ctx, p.Code, "", ""); err != nil {
		t.Fatalf("valid code after failures: %v", err)
	}
}

func TestAuthenticateMachineRejectsWrongTokens(t *testing.T) {
	s, ctx := setup(t)
	g := pairMachine(t, s, ctx, "a")
	h := pairMachine(t, s, ctx, "b")
	id, secret, _ := strings.Cut(strings.TrimPrefix(g.Token, db.MachineTokenPrefix), "_")
	hid, _, _ := strings.Cut(strings.TrimPrefix(h.Token, db.MachineTokenPrefix), "_")
	for name, tok := range map[string]string{
		"wrong secret, same length": g.Token[:len(g.Token)-1] + flip(g.Token[len(g.Token)-1]),
		"truncated secret":          g.Token[:len(g.Token)-4],
		"longer secret":             g.Token + "a",
		"another token's id":        db.MachineTokenPrefix + hid + "_" + secret,
		"pairing prefix":            "stage_pc_" + id + "_" + secret,
		"no id":                     db.MachineTokenPrefix + secret,
		"empty":                     "",
	} {
		if _, err := s.AuthenticateMachine(ctx, tok); !errors.Is(err, db.ErrInvalidMachineToken) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestRemoveMachineRevokesTokenAndRetiresConnections(t *testing.T) {
	s, ctx := setup(t)
	g := pairMachine(t, s, ctx, "laptop")
	keep := pairMachine(t, s, ctx, "desktop")
	stable := harness.Descriptor{Kind: "stable", Name: "Stable", Hosting: harness.Connected, StableIdentity: true}
	loose := harness.Descriptor{Kind: "loose", Name: "Loose", Hosting: harness.Connected}
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
	if err := s.RemoveMachine(ctx, g.MachineID); err != nil {
		t.Fatal(err)
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

func TestRenameMachine(t *testing.T) {
	s, ctx := setup(t)
	if err := s.RenameMachine(ctx, db.LocalMachine, "  studio  "); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.GetMachineInfo(ctx, db.LocalMachine); m.Label != "studio" {
		t.Fatalf("label %q", m.Label)
	}
	if err := s.RenameMachine(ctx, db.LocalMachine, "  "); !isInvalid(err) {
		t.Fatalf("blank: %v", err)
	}
	if err := s.RenameMachine(ctx, 99, "x"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func flip(c byte) string {
	if c == 'a' {
		return "b"
	}
	return "a"
}

func isInvalid(err error) bool {
	var invalid *db.InvalidError
	return errors.As(err, &invalid)
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

func TestRevokePairing(t *testing.T) {
	s, ctx := setup(t)
	p, err := s.CreatePairing(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RevokePairing(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RedeemPairing(ctx, p.Code, "", ""); !errors.Is(err, db.ErrInvalidPairing) {
		t.Fatalf("redeem after revoke: %v", err)
	}
	if _, err := s.PairingStatus(ctx, p.ID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("status after revoke: %v", err)
	}
	if err := s.RevokePairing(ctx, p.ID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("revoke twice: %v", err)
	}
	used, err := s.CreatePairing(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RedeemPairing(ctx, used.Code, "", ""); err != nil {
		t.Fatal(err)
	}
	var conflict *db.ConflictError
	if err := s.RevokePairing(ctx, used.ID); !errors.As(err, &conflict) || conflict.Code != "pairing_redeemed" {
		t.Fatalf("revoke redeemed: %v", err)
	}
}
