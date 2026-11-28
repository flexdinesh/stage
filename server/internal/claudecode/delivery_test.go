package claudecode_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func TestDeliveryAtClaudeBoundaries(t *testing.T) {
	f, a, _, ref := launchHooked(t)
	id := ref.ExternalSessionID
	send := func(mode execution.Delivery, text string) {
		t.Helper()
		if err := a.Send(t.Context(), ref, execution.Message{Text: text, Delivery: mode}); err != nil {
			t.Fatal(err)
		}
	}
	pass := func(event string, extra map[string]any) {
		t.Helper()
		if got := f.HookResponse(id, event, extra); got.Status != http.StatusNoContent || got.Body != "" {
			t.Fatalf("%s: got %d %s, want no decision", event, got.Status, got.Body)
		}
	}
	stop := func(want string) {
		t.Helper()
		var output struct{ Decision, Reason string }
		readDeliveryHook(t, f.HookResponse(id, "Stop", nil), &output)
		if output.Decision != "block" || output.Reason != want {
			t.Fatalf("Stop: %+v, want block with %q", output, want)
		}
	}
	pass("Stop", nil)
	send(execution.DeliveryQueue, "first at Stop")
	send(execution.DeliverySteer, "first at a tool")
	send(execution.DeliverySteer, "second at a tool")
	send(execution.DeliverySteer, "no more tools")
	for _, tc := range []struct{ event, text string }{{"PostToolUse", "first at a tool"}, {"PostToolBatch", "second at a tool"}} {
		var output struct {
			Specific struct{ HookEventName, AdditionalContext string } `json:"hookSpecificOutput"`
		}
		readDeliveryHook(t, f.HookResponse(id, tc.event, nil), &output)
		if output.Specific.HookEventName != tc.event || output.Specific.AdditionalContext != "Message from the user (sent from Stage): "+tc.text {
			t.Fatalf("%s: %+v", tc.event, output)
		}
	}
	stop("first at Stop")
	pass("Stop", map[string]any{"stop_hook_active": true})
	stop("no more tools")
	pass("Stop", nil)
}

func readDeliveryHook(t *testing.T, call *claudetest.HookCall, out any) {
	t.Helper()
	if call.Status != http.StatusOK {
		t.Fatalf("hook: HTTP %d %s", call.Status, call.Body)
	}
	if err := json.Unmarshal([]byte(call.Body), out); err != nil {
		t.Fatal(err)
	}
}

func TestDeliveryEligibilityAndLimits(t *testing.T) {
	f, a, hooks, ref := launchHooked(t)
	id := ref.ExternalSessionID
	features, err := a.Features(t.Context())
	if err != nil || !features.Operations[execution.OpSteer].Available || !features.Operations[execution.OpQueue].Available {
		t.Fatalf("hooked features: %+v %v", features, err)
	}
	features, err = f.Adapter(conn).Features(t.Context())
	for _, op := range []execution.Op{execution.OpSteer, execution.OpQueue} {
		if av := features.Operations[op]; err != nil || av.Available || av.Reason == "" {
			t.Fatalf("no receiver: %+v %v", av, err)
		}
	}
	queue := func(text string) error {
		return a.Send(t.Context(), ref, execution.Message{Text: text, Delivery: execution.DeliveryQueue})
	}
	var invalid *execution.Invalid
	if err := queue(strings.Repeat("é", 16<<10) + "x"); !errors.As(err, &invalid) {
		t.Fatalf("oversize: %v", err)
	}
	for range 8 {
		if err := queue(strings.Repeat("é", 16<<10)); err != nil {
			t.Fatal(err)
		}
	}
	assertBusy(t, queue("ninth"), "8 pending messages")
	if got := f.Hook(id, "PostToolUse", nil); got != http.StatusNoContent {
		t.Fatalf("tool drained a queued item: HTTP %d", got)
	}
	f.Hook(id, "SessionEnd", nil)
	if got := f.Hook(id, "Stop", nil); got != http.StatusNoContent {
		t.Fatalf("ended session kept deliveries: HTTP %d", got)
	}
	f.SetState(id, "working", "idle", "")
	assertBusy(t, queue("idle"), "Claude Code finished its turn; send instead.")
	f.SetState(id, "done", "idle", "")
	assertBusy(t, queue("done"), "Claude Code finished its turn; send instead.")

	// The version marker survives a reconnect. Pending text does not.
	f.SetState(id, "working", "busy", "")
	if err := queue("lost at restart"); err != nil {
		t.Fatal(err)
	}
	if err := hooks.Close(); err != nil {
		t.Fatal(err)
	}
	a = f.HookedAdapter(conn, f.Hooks())
	if got := f.Hook(id, "Stop", nil); got != http.StatusNoContent {
		t.Fatalf("receiver restart kept deliveries: HTTP %d", got)
	}
	if err := queue("remains eligible"); err != nil {
		t.Fatal(err)
	}
	f.Remove(id)
	if _, err := a.Observe(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	if got := f.Hook(id, "Stop", nil); got != http.StatusNoContent {
		t.Fatalf("removed session kept deliveries: HTTP %d", got)
	}
}

func TestOlderClaudeSessionCannotTakeDelivery(t *testing.T) {
	f := claudetest.New(t)
	ref := execution.SessionRef{ConnectionID: conn, ExternalSessionID: launched(t, f, f.Adapter(conn))}
	a := f.HookedAdapter(conn, f.Hooks())
	for _, mode := range []execution.Delivery{execution.DeliverySteer, execution.DeliveryQueue} {
		err := a.Send(t.Context(), ref, execution.Message{Text: "old session", Delivery: mode})
		assertBusy(t, err, "Launched before Stage could steer this session; send once it finishes, or answer in Claude Code (claude --resume "+ref.ExternalSessionID+").")
	}
}

func assertBusy(t *testing.T, err error, reason string) {
	t.Helper()
	var busy *execution.Busy
	if !errors.As(err, &busy) || !strings.Contains(busy.Reason, reason) {
		t.Fatalf("want Busy with %q, got %v", reason, err)
	}
}
