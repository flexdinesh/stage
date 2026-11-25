package cli_test

import (
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/api"
	stageharness "github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/opencode"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// A hosted harness this machine does not allow: `stage harness` prints the
// server's reason, not "no connection setup".
func TestHarnessCommandPrintsWhyAHostedHarnessIsUnavailable(t *testing.T) {
	kit := stageharness.Descriptor{Kind: "kit", Name: "Kit", Hosting: stageharness.Hosted}
	h := newHarness(t, func(d *api.Deps) {
		d.Harnesses = stageharness.MustRegistry(t3.Descriptor, opencode.Descriptor, kit)
	})
	// The test server runs with no profile: a foreground server.
	reason := stageharness.Profile{Kind: stageharness.ProfileServer}.Allows(kit).Reason
	code, _, errOut := h.run("", "harness", "kit")
	if code != 1 || !strings.Contains(errOut, "harness kit: "+reason) {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}
