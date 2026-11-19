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
	code, out, errOut := h.run("", "opencode")
	if code != 0 || !strings.Contains(out, "opencode not connected") || !strings.Contains(out, "no OpenCode service registered") ||
		!strings.Contains(out, "next: run `opencode pair`") {
		t.Fatalf("opencode: %d %q %q", code, out, errOut)
	}
	if code, _, errOut := h.run("", "opencode", "connect"); code != 1 || !strings.Contains(errOut, "stage API 409: No OpenCode service file found") {
		t.Fatalf("connect: %d %q", code, errOut)
	}
	if code, _, errOut := h.run("", "opencode", "pair"); code != 2 || !strings.Contains(errOut, "want the link") {
		t.Fatalf("pair without link: %d %q", code, errOut)
	}
	if code, _, errOut := h.run("", "opencode", "nope"); code != 2 || !strings.Contains(errOut, "opencode nope: unknown") {
		t.Fatalf("unknown: %d %q", code, errOut)
	}
	fake := opencodetest.New(t, "server-password")
	code, out, errOut = h.run("", "opencode", "pair", fake.Link())
	if code != 0 || !strings.Contains(out, "opencode connected at "+fake.URL+" (paired)") || !strings.Contains(out, "token     expires") {
		t.Fatalf("pair: %d %q %q", code, out, errOut)
	}
	code, out, _ = h.run("", "opencode", "status", "--json")
	var st struct {
		State, Auth, ExpiresAt string
	}
	if code != 0 || json.Unmarshal([]byte(out), &st) != nil || st.State != "connected" || st.Auth != "paired" || st.ExpiresAt == "" {
		t.Fatalf("status --json: %d %s", code, out)
	}
	if code, out, errOut := h.run("", "opencode", "disconnect"); code != 0 || !strings.Contains(out, "opencode not connected") {
		t.Fatalf("disconnect: %d %q %q", code, out, errOut)
	}
}
