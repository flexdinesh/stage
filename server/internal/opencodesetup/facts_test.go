package opencodesetup

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/opencode"
	"github.com/flexdinesh/stage/server/internal/opencode/opencodetest"
)

// connectedFacts is a Service connected to a fake OpenCode (no auth) whose
// display probes answer within timeout, naming CLI stage-dev in fixes.
func connectedFacts(t *testing.T, timeout time.Duration) (*Connector, *opencodetest.Server) {
	t.Helper()
	h := newHarness(t)
	h.s.cli, h.s.probeTimeout = "stage-dev", timeout
	srv := opencodetest.New(t, "")
	if err := h.s.Save(t.Context(), Edit{Endpoint: &srv.URL}); err != nil {
		t.Fatal(err)
	}
	return NewConnector(h.s), srv
}

func status(t *testing.T, c *Connector, refresh bool) harness.Status {
	t.Helper()
	st, err := c.Status(t.Context(), refresh)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// recheck runs the check action, which probes the facts again.
func recheck(t *testing.T, c *Connector) harness.Status {
	t.Helper()
	if _, err := c.Run(t.Context(), opencode.ActionCheck, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	return status(t, c, false)
}

func stageMCP(t *testing.T, st harness.Status) execution.Fact {
	t.Helper()
	for _, f := range st.Facts {
		if f.ID == "stageMcp" && f.Label == "Stage MCP" {
			return f
		}
	}
	t.Fatalf("no Stage MCP fact in %+v", st.Facts)
	return execution.Fact{}
}

// The Stage MCP fact follows OpenCode's MCP status; a missing server gets
// the setup command as the status fix.
func TestStatusReportsStageMCP(t *testing.T) {
	c, srv := connectedFacts(t, time.Second)
	st := status(t, c, true)
	if f := stageMCP(t, st); f.Value != "Connected" || f.Tone != execution.ToneOK || st.Fix != nil {
		t.Fatalf("connected: %+v fix %+v", f, st.Fix)
	}
	for _, tc := range []struct {
		server      map[string]any
		value       string
		tone        execution.Tone
		wantConnect bool
	}{
		{opencodetest.MCP("stage", "pending", ""), "Connecting", execution.ToneNeutral, true},
		{opencodetest.MCP("stage", "disabled", ""), "Disabled", execution.ToneWarn, true},
		{opencodetest.MCP("stage", "needs_auth", "Unauthorized"), "Needs sign-in", execution.ToneBad, true},
		{opencodetest.MCP("stage", "failed", "spawn stage ENOENT"), "Failed: spawn stage ENOENT", execution.ToneBad, true},
	} {
		srv.SetMCP(opencodetest.MCP("other", "connected", ""), tc.server)
		st := recheck(t, c)
		if f := stageMCP(t, st); f.Value != tc.value || f.Tone != tc.tone || st.Fix != nil || st.Connected != tc.wantConnect {
			t.Fatalf("%v: %+v fix %+v", tc.server, f, st.Fix)
		}
	}

	srv.SetMCP(opencodetest.MCP("other", "connected", ""))
	st = recheck(t, c)
	if f := stageMCP(t, st); f.Value != "Not configured" || f.Tone != execution.ToneWarn {
		t.Fatalf("missing: %+v", f)
	}
	if st.Fix == nil || st.Fix.Command != "stage-dev setup --opencode --mcp" || st.Phase != harness.PhaseConnected {
		t.Fatalf("missing fix %+v phase %s", st.Fix, st.Phase)
	}

	// Status reads within a minute reuse the probe, even with refresh; the
	// check action probes again.
	srv.SetMCP(opencodetest.MCP("stage", "connected", ""))
	for _, refresh := range []bool{false, true} {
		if f := stageMCP(t, status(t, c, refresh)); f.Value != "Not configured" {
			t.Fatalf("refresh %v probed again: %+v", refresh, f)
		}
	}
	if f := stageMCP(t, recheck(t, c)); f.Value != "Connected" {
		t.Fatalf("check: %+v", f)
	}
}

// A failing or slow MCP read is a bad fact; the status still answers,
// connected.
func TestStageMCPFailureAndTimeoutDoNotFailStatus(t *testing.T) {
	c, srv := connectedFacts(t, 200*time.Millisecond)
	srv.FailMCP(http.StatusInternalServerError)
	st := status(t, c, true)
	if f := stageMCP(t, st); f.Tone != execution.ToneBad || len(f.Value) < len("Couldn't check: ") || f.Value[:len("Couldn't check: ")] != "Couldn't check: " || !st.Connected {
		t.Fatalf("failure: %+v connected %v", f, st.Connected)
	}

	srv.FailMCP(0)
	srv.DelayMCP(5 * time.Second)
	start := time.Now()
	st = recheck(t, c)
	if d := time.Since(start); d > 4*time.Second {
		t.Fatalf("status took %s", d)
	}
	if f := stageMCP(t, st); f.Value != "No answer within 200ms" || f.Tone != execution.ToneBad || !st.Connected || st.Phase != harness.PhaseConnected {
		t.Fatalf("timeout: %+v %+v", f, st)
	}
}

// No probe without a connected service.
func TestNoStageMCPFactUnlessConnected(t *testing.T) {
	h := newHarness(t)
	st := status(t, NewConnector(h.s), true)
	for _, f := range st.Facts {
		if f.ID == "stageMcp" {
			t.Fatalf("fact while unconfigured: %+v", st.Facts)
		}
	}
}
