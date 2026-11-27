package claudecodesetup_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest/fakestate"
	"github.com/flexdinesh/stage/server/internal/claudecodesetup"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

func stageMCPFact(t *testing.T, st harness.Status) execution.Fact {
	t.Helper()
	for _, f := range st.Facts {
		if f.ID == "stageMcp" {
			if f.Label != "Stage MCP" {
				t.Fatalf("label %q", f.Label)
			}
			return f
		}
	}
	t.Fatalf("no Stage MCP fact in %+v", st.Facts)
	return execution.Fact{}
}

func connected(t *testing.T, e *env) *claudecodesetup.Service {
	t.Helper()
	s, err := claudecodesetup.New(t.Context(), claudecodesetup.Ports{Settings: e.ports.Settings(), Identity: e.ports, Install: e.local.Install, CLI: "stage-dev"}, e.opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	return s
}

// recheck runs the check action, which probes the facts again.
func recheck(t *testing.T, s *claudecodesetup.Service) harness.Status {
	t.Helper()
	if _, err := claudecodesetup.NewConnector(s).Run(t.Context(), "check", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	return s.Status(t.Context(), false)
}

func setMCP(e *env, servers map[string]fakestate.MCPServer) {
	e.f.Update(func(s *fakestate.State) { s.MCP = servers })
}

// The Stage MCP fact follows `claude mcp get stage`, run from the home
// folder; a missing server gets the setup command as the status fix.
func TestStatusReportsStageMCP(t *testing.T) {
	e := newEnv(t)
	s := connected(t, e)
	ctx := t.Context()
	st := s.Status(ctx, true)
	if f := stageMCPFact(t, st); f.Value != "Connected" || f.Tone != execution.ToneOK || st.Fix != nil {
		t.Fatalf("connected: %+v fix %+v", f, st.Fix)
	}
	var probed bool
	for _, c := range e.f.Read().Calls {
		if strings.Join(c.Args, " ") == "mcp get stage" {
			probed = c.Dir == e.f.Home
		}
	}
	if !probed {
		t.Fatal("mcp get stage did not run from the home folder")
	}

	for _, c := range []struct {
		server map[string]fakestate.MCPServer
		value  string
		tone   execution.Tone
	}{
		{map[string]fakestate.MCPServer{"stage": {Status: "✘ Failed to connect", Issue: "ENOENT: no such file or directory, posix_spawn 'stage'"}}, "Failed: ENOENT: no such file or directory, posix_spawn 'stage'", execution.ToneBad},
		{map[string]fakestate.MCPServer{"stage": {Status: "△ Needs authentication"}}, "Needs sign-in", execution.ToneBad},
		{map[string]fakestate.MCPServer{"stage": {Status: "⏸ Pending approval"}}, "Pending approval", execution.ToneWarn},
		{map[string]fakestate.MCPServer{"stage": {Raw: "stage:\n  Type: stdio\n"}}, "Couldn't check: claude mcp get: no status in its output", execution.ToneBad},
	} {
		setMCP(e, c.server)
		st := recheck(t, s)
		if f := stageMCPFact(t, st); f.Value != c.value || f.Tone != c.tone || st.Fix != nil || !st.Connected {
			t.Fatalf("%+v: %+v fix %+v connected %v", c.server, f, st.Fix, st.Connected)
		}
	}

	setMCP(e, nil)
	st = recheck(t, s)
	if f := stageMCPFact(t, st); f.Value != "Not configured" || f.Tone != execution.ToneWarn {
		t.Fatalf("missing: %+v", f)
	}
	if st.Fix == nil || st.Fix.Command != "stage-dev setup mcp --claude" || st.Phase != harness.PhaseConnected {
		t.Fatalf("missing fix %+v phase %s", st.Fix, st.Phase)
	}
	// Status reads within a minute reuse the probe, even with refresh.
	setMCP(e, map[string]fakestate.MCPServer{"stage": {Status: "✔ Connected"}})
	for _, refresh := range []bool{false, true} {
		if f := stageMCPFact(t, s.Status(ctx, refresh)); f.Value != "Not configured" {
			t.Fatalf("refresh %v probed again: %+v", refresh, f)
		}
	}
}

// A probe that never exits is cut off at its timeout; status still answers
// with the CLI checks' result.
func TestStageMCPTimeoutDoesNotFailStatus(t *testing.T) {
	e := newEnv(t)
	e.opts.MCPTimeout = 300 * time.Millisecond
	s := connected(t, e)
	e.f.Update(func(st *fakestate.State) { st.HangMCP = true })
	start := time.Now()
	st := s.Status(t.Context(), true)
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("status took %s", d)
	}
	if f := stageMCPFact(t, st); f.Value != "No answer within 300ms" || f.Tone != execution.ToneBad || !st.Connected || st.State != claudecodesetup.StateConnected {
		t.Fatalf("timeout: %+v %+v", f, st)
	}
}

// No Stage MCP probe or fact before Claude Code is set up, or while it is
// logged out.
func TestNoStageMCPFactUnlessConnected(t *testing.T) {
	e := newEnv(t)
	s := e.service(t)
	st := s.Status(t.Context(), true)
	for _, f := range st.Facts {
		if f.ID == "stageMcp" {
			t.Fatalf("fact before setup: %+v", st.Facts)
		}
	}
	for _, c := range e.f.Read().Calls {
		if len(c.Args) > 0 && c.Args[0] == "mcp" {
			t.Fatal("probed before setup")
		}
	}
	s = connected(t, e)
	e.f.Update(func(st *fakestate.State) { st.LoggedIn = false })
	setMCP(e, nil)
	st = s.Status(t.Context(), true)
	for _, f := range st.Facts {
		if f.ID == "stageMcp" {
			t.Fatalf("fact while logged out: %+v", st.Facts)
		}
	}
	if st.Fix == nil || st.Fix.Command != "claude auth login" {
		t.Fatalf("logged out fix %+v", st.Fix)
	}
}
