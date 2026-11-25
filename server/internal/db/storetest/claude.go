package storetest

import (
	"testing"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
)

func init() {
	register("ClaudeSettings", caseClaudeSettings)
}

// caseClaudeSettings: a machine with no row reads the zero value; a save
// reads back, a second save replaces it, and each machine keeps its own.
func caseClaudeSettings(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	local := s.OnMachine(db.LocalMachine)
	other := s.OnMachine(pairMachine(t, s, ctx, "worker").MachineID)

	read := func(m db.MachineStore) config.ClaudeSettings {
		t.Helper()
		c, err := m.ClaudeSettings(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	if c := read(local); c != (config.ClaudeSettings{}) {
		t.Fatalf("default %+v, want zero", c)
	}

	saved := config.ClaudeSettings{Binary: "/opt/bin/claude ✓", ConfigDir: "/home/u/.claude", Disconnected: true}
	if err := local.SaveClaudeSettings(ctx, saved); err != nil {
		t.Fatal(err)
	}
	if c := read(local); c != saved {
		t.Fatalf("saved %+v, want %+v", c, saved)
	}
	if c := read(other); c != (config.ClaudeSettings{}) {
		t.Fatalf("other machine %+v, want zero", c)
	}

	updated := config.ClaudeSettings{Binary: "claude-dev"}
	if err := local.SaveClaudeSettings(ctx, updated); err != nil {
		t.Fatal(err)
	}
	if c := read(local); c != updated {
		t.Fatalf("updated %+v, want %+v", c, updated)
	}

	theirs := config.ClaudeSettings{Disconnected: true}
	if err := other.SaveClaudeSettings(ctx, theirs); err != nil {
		t.Fatal(err)
	}
	if c := read(other); c != theirs {
		t.Fatalf("other machine %+v, want %+v", c, theirs)
	}
	if c := read(local); c != updated {
		t.Fatalf("local after other's save %+v, want %+v", c, updated)
	}
}
