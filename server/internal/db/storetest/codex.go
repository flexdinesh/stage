package storetest

import (
	"testing"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
)

func init() {
	register("CodexSettingsPerMachine", caseCodexSettingsPerMachine)
}

// Codex settings are per machine: none reads never connected, a save
// round-trips and overwrites, and machines never share a row.
func caseCodexSettingsPerMachine(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	remote := pairMachine(t, s, ctx, "worker").MachineID
	if c, err := s.CodexSettings(ctx, db.LocalMachine); err != nil || c != (config.CodexSettings{}) {
		t.Fatalf("unsaved %+v %v", c, err)
	}
	want := config.CodexSettings{Binary: "/opt/cödex bin", Connected: true}
	if err := s.SaveCodexSettings(ctx, db.LocalMachine, want); err != nil {
		t.Fatal(err)
	}
	if c, err := s.CodexSettings(ctx, db.LocalMachine); err != nil || c != want {
		t.Fatalf("saved %+v %v", c, err)
	}
	if c, err := s.CodexSettings(ctx, remote); err != nil || c != (config.CodexSettings{}) {
		t.Fatalf("remote machine read the local row: %+v %v", c, err)
	}
	// Disconnect keeps the binary.
	if err := s.SaveCodexSettings(ctx, db.LocalMachine, config.CodexSettings{Binary: want.Binary}); err != nil {
		t.Fatal(err)
	}
	if c, err := s.CodexSettings(ctx, db.LocalMachine); err != nil || c.Connected || c.Binary != want.Binary {
		t.Fatalf("disconnected %+v %v", c, err)
	}
}
