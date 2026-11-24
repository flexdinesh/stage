package storetest

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
)

func init() {
	register("RedeemPairingLabels", caseRedeemPairingLabels)
	register("RedeemPairingIsSingleUse", caseRedeemPairingIsSingleUse)
	register("RedeemPairingConcurrentSingleWinner", caseRedeemPairingConcurrentSingleWinner)
	register("RedeemPairingRejectsWrongCodes", caseRedeemPairingRejectsWrongCodes)
	register("AuthenticateMachineRejectsWrongTokens", caseAuthenticateMachineRejectsWrongTokens)
	register("RenameMachine", caseRenameMachine)
	register("RevokePairing", caseRevokePairing)
	register("SetDefaultLocalLabelKeepsAGivenName", caseSetDefaultLocalLabelKeepsAGivenName)
}

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

func caseRedeemPairingLabels(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
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

func caseRedeemPairingIsSingleUse(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
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

func caseRedeemPairingConcurrentSingleWinner(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
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

func caseRedeemPairingRejectsWrongCodes(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
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

func caseAuthenticateMachineRejectsWrongTokens(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
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

func caseRenameMachine(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
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

func caseRevokePairing(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
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

func caseSetDefaultLocalLabelKeepsAGivenName(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	label := func() string {
		t.Helper()
		m, err := s.GetMachine(ctx, db.LocalMachine)
		if err != nil {
			t.Fatal(err)
		}
		return m.Label
	}
	if got := label(); got != "" {
		t.Fatalf("fresh label %q, want unset", got)
	}
	if _, err := s.SetDefaultLocalLabel(ctx, " "); !isInvalid(err) {
		t.Fatalf("blank default: %v", err)
	}
	if set, err := s.SetDefaultLocalLabel(ctx, " deebox "); err != nil || !set || label() != "deebox" {
		t.Fatalf("first default: %v %v %q", set, err, label())
	}
	if set, err := s.SetDefaultLocalLabel(ctx, "other"); err != nil || set || label() != "deebox" {
		t.Fatalf("second default must not replace: %v %v %q", set, err, label())
	}
	if err := s.RenameMachine(ctx, db.LocalMachine, "studio"); err != nil {
		t.Fatal(err)
	}
	if set, err := s.SetDefaultLocalLabel(ctx, "deebox"); err != nil || set || label() != "studio" {
		t.Fatalf("rename must survive: %v %v %q", set, err, label())
	}
}
