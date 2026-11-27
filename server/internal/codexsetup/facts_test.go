package codexsetup_test

import (
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/codex"
	"github.com/flexdinesh/stage/server/internal/codex/codextest"
	"github.com/flexdinesh/stage/server/internal/codexsetup"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// factsRig is a connected Codex whose display probes answer within
// timeout, with reset times formatted in UTC at a fixed now.
func factsRig(t *testing.T, timeout time.Duration) *rig {
	t.Helper()
	r := newRig(t)
	r.close()
	now := time.Unix(1_795_000_000, 0)
	composed, err := codexsetup.Module.Compose(t.Context(), harness.Ports{
		Settings: r.ports.Settings(), Identity: r.ports, Install: r.local.Install, CLI: "stage-dev",
		Overrides: map[string]any{codex.Kind: codexsetup.Options{
			Version: "test", Daemon: func(string) codex.Daemon { return r.daemon },
			ProbeTimeout: timeout, Now: func() time.Time { return now }, Location: time.UTC,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(composed.Close)
	r.close, r.conn = composed.Close, composed.Connector
	if _, err := r.conn.Run(t.Context(), codex.ActionConnect, nil); err != nil {
		t.Fatal(err)
	}
	return r
}

// recheck runs the check action (which probes the facts again), then
// reads the status.
func (r *rig) recheck(t *testing.T) harness.Status {
	t.Helper()
	if _, err := r.conn.Run(t.Context(), codex.ActionCheck, nil); err != nil {
		t.Fatal(err)
	}
	return r.status(t)
}

func fact(st harness.Status, id string) (execution.Fact, bool) {
	for _, f := range st.Facts {
		if f.ID == id {
			return f, true
		}
	}
	return execution.Fact{}, false
}

func wantFact(t *testing.T, st harness.Status, id, label, value string, tone execution.Tone) {
	t.Helper()
	f, ok := fact(st, id)
	if !ok || f.Label != label || f.Value != value || f.Tone != tone {
		t.Fatalf("fact %s = %+v (found %v), want %q %q %q; facts %+v", id, f, ok, label, value, tone, st.Facts)
	}
}

// A connected Codex shows the Stage MCP server and the account's usage
// windows in Codex's words; nothing needs fixing.
func TestStatusShowsStageMCPAndUsage(t *testing.T) {
	r := factsRig(t, time.Second)
	st := r.status(t)
	wantFact(t, st, "stageMcp", "Stage MCP", "Connected", execution.ToneOK)
	wantFact(t, st, "usagePrimary", "5h limit", "12% used · resets 12:06", "")
	wantFact(t, st, "usageSecondary", "Weekly limit", "30% used · resets 22:26 on 21 Nov", "")
	if st.Fix != nil || st.Phase != harness.PhaseConnected {
		t.Fatalf("fix %+v phase %s", st.Fix, st.Phase)
	}
	if last := st.Facts[len(st.Facts)-1]; last.ID != "checkedAt" {
		t.Fatalf("last fact %+v, want checkedAt", last)
	}
}

// Without the Stage MCP server the fact warns and the status fix names the
// matching setup command for this machine's CLI; the phase stays connected.
func TestStatusFixesAMissingStageMCP(t *testing.T) {
	r := factsRig(t, time.Second)
	r.srv.SetMCPServers(codextest.MCPServer("other", ""))
	st := r.status(t)
	wantFact(t, st, "stageMcp", "Stage MCP", "Not configured", execution.ToneWarn)
	if st.Fix == nil || st.Fix.Command != "stage-dev setup mcp --codex" || st.Fix.Text == "" || st.Phase != harness.PhaseConnected || !st.Connected {
		t.Fatalf("fix %+v phase %s connected %v", st.Fix, st.Phase, st.Connected)
	}
}

func TestStatusReportsAFailingStageMCP(t *testing.T) {
	r := factsRig(t, time.Second)
	r.srv.SetMCPServers(codextest.MCPServer("stage", "MCP startup failed: No such file or directory (os error 2)"))
	st := r.status(t)
	wantFact(t, st, "stageMcp", "Stage MCP", "Failed: MCP startup failed: No such file or directory (os error 2)", execution.ToneBad)
	if st.Fix != nil {
		t.Fatalf("fix %+v", st.Fix)
	}
}

// A probe that fails or never answers becomes a bad fact; the status
// itself still answers, connected.
func TestStageMCPProbeFailureAndTimeoutDoNotFailStatus(t *testing.T) {
	r := factsRig(t, 200*time.Millisecond)
	r.srv.Unsupported("mcpServerStatus/list")
	st := r.status(t)
	f, _ := fact(st, "stageMcp")
	if f.Tone != execution.ToneBad || f.Value[:len("Couldn't check: ")] != "Couldn't check: " || !st.Connected {
		t.Fatalf("unsupported: %+v connected %v", f, st.Connected)
	}

	r.srv.Supported("mcpServerStatus/list")
	r.srv.DropReply("mcpServerStatus/list", 1)
	r.srv.DropReply("account/rateLimits/read", 1)
	start := time.Now()
	st = r.recheck(t)
	if time.Since(start) > 4*time.Second {
		t.Fatalf("status took %s", time.Since(start))
	}
	wantFact(t, st, "stageMcp", "Stage MCP", "No answer within 200ms", execution.ToneBad)
	if _, ok := fact(st, "usagePrimary"); ok || !st.Connected || st.State != codexsetup.StateConnected {
		t.Fatalf("timed out usage shown or status failed: %+v", st)
	}
}

// Status reads (UI polls ask with refresh) reuse the probe for a minute;
// the check action probes again.
func TestDisplayProbesAreCached(t *testing.T) {
	r := factsRig(t, time.Second)
	r.status(t)
	calls := r.srv.Calls("mcpServerStatus/list")
	r.srv.SetMCPServers()
	for _, refresh := range []bool{false, true} {
		st, err := r.conn.Status(t.Context(), refresh)
		if err != nil {
			t.Fatal(err)
		}
		wantFact(t, st, "stageMcp", "Stage MCP", "Connected", execution.ToneOK)
	}
	if r.srv.Calls("mcpServerStatus/list") != calls {
		t.Fatal("a status read probed again within a minute")
	}
	wantFact(t, r.recheck(t), "stageMcp", "Stage MCP", "Not configured", execution.ToneWarn)
}

// Usage shows only for a login Codex reports limits for, and its tone
// follows the share used.
func TestUsageFacts(t *testing.T) {
	r := factsRig(t, time.Second)
	r.srv.SetRateLimits(map[string]any{"primary": codextest.Window(79.4, 300, 1_795_003_600), "secondary": codextest.Window(80, 10080, 1_795_300_000)})
	st := r.status(t)
	wantFact(t, st, "usagePrimary", "5h limit", "79% used · resets 12:06", "")
	wantFact(t, st, "usageSecondary", "Weekly limit", "80% used · resets 22:26 on 21 Nov", execution.ToneWarn)

	r.srv.SetRateLimits(map[string]any{"primary": codextest.Window(100, 1440, 1_795_003_600)})
	st = r.recheck(t)
	wantFact(t, st, "usagePrimary", "1d limit", "100% used · resets 12:06", execution.ToneBad)
	if _, ok := fact(st, "usageSecondary"); ok {
		t.Fatal("absent secondary window shown")
	}

	r.srv.SetRateLimits(nil)
	st = r.recheck(t)
	if _, ok := fact(st, "usagePrimary"); ok {
		t.Fatalf("usage without limits: %+v", st.Facts)
	}
	wantFact(t, st, "stageMcp", "Stage MCP", "Connected", execution.ToneOK)
}

// No probes before Stage is connected to Codex.
func TestNoDisplayProbesWhenNotConnected(t *testing.T) {
	r := newRig(t)
	st := r.status(t)
	if _, ok := fact(st, "stageMcp"); ok || r.srv.Calls("mcpServerStatus/list") != 0 {
		t.Fatalf("probed before connect: %+v", st.Facts)
	}
}
