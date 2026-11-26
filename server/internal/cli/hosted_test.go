package cli_test

import (
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/db"
	stageharness "github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/opencode"
	t3sync "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// A hosted harness this machine does not allow: `stage harness` prints the
// server's reason, not "no connection setup".
func TestHarnessCommandPrintsWhyAHostedHarnessIsUnavailable(t *testing.T) {
	kit := stageharness.Descriptor{Kind: "kit", Name: "Kit", Sessions: stageharness.SessionsMachine, LocalProcesses: true}
	// The test server runs as a foreground server without local harnesses.
	server := stageharness.Profile{Kind: stageharness.ProfileServer}
	h := newHarness(t, func(d *api.Deps) {
		d.Harnesses = stageharness.MustRegistry(t3.Descriptor, opencode.Descriptor, kit)
		svc := t3sync.NewForMachines(d.Store, d.Harnesses, machine.MustSet(machine.NewLocal(db.LocalMachine, server, nil)), nil)
		d.Sync = svc
	})
	reason := server.Allows(kit).Reason
	code, _, errOut := h.run("", "harness", "kit")
	if code != 1 || !strings.Contains(errOut, "harness kit: "+reason) {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}
