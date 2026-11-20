package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/opencode/opencodetest"
)

func TestOpenCodeCommands(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	h := newHarness(t)
	code, out, errOut := h.run("", "harness", "opencode")
	if code != 0 || !strings.Contains(out, "opencode not connected") || !strings.Contains(out, "no OpenCode service registered") ||
		!strings.Contains(out, "next: run `opencode pair`") {
		t.Fatalf("opencode: %d %q %q", code, out, errOut)
	}
	if code, _, errOut := h.run("", "harness", "opencode", "run", "connect-local"); code != 1 || !strings.Contains(errOut, "stage API 409: No OpenCode service file found") {
		t.Fatalf("connect: %d %q", code, errOut)
	}
	if code, _, errOut := h.run("", "harness", "opencode", "run", "pair"); code != 1 || !strings.Contains(errOut, "stage API 400") {
		t.Fatalf("pair without link: %d %q", code, errOut)
	}
	if code, _, errOut := h.run("", "harness", "opencode", "nope"); code != 2 || !strings.Contains(errOut, "harness opencode nope: unknown") {
		t.Fatalf("unknown: %d %q", code, errOut)
	}
	fake := opencodetest.New(t, "server-password")
	code, out, errOut = h.run("", "harness", "opencode", "run", "pair", "link="+fake.Link())
	if code != 0 || !strings.Contains(out, "opencode connected at "+fake.URL+" (paired)") || !strings.Contains(out, "token     expires") {
		t.Fatalf("pair: %d %q %q", code, out, errOut)
	}
	code, out, _ = h.run("", "harness", "opencode", "status", "--json")
	var reply struct {
		Details struct {
			State, Auth, ExpiresAt string
		}
	}
	err := json.Unmarshal([]byte(out), &reply)
	st := reply.Details
	if code != 0 || err != nil || st.State != "connected" || st.Auth != "paired" || st.ExpiresAt == "" {
		t.Fatalf("status --json: %d %s", code, out)
	}
	if code, out, errOut := h.run("", "harness", "opencode", "disconnect"); code != 0 || !strings.Contains(out, "opencode not connected") {
		t.Fatalf("disconnect: %d %q %q", code, out, errOut)
	}
}
