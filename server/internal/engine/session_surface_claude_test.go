package engine_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func TestContractClaudeHookDelivery(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		ctx := t.Context()
		f := claudetest.New(t)
		_, svc, task, _ := claudeSetupWith(t, p, f, f.HookedAdapter(claudeConn, f.Hooks()))
		a, err := svc.Connections.Adapter(claudeConn)
		if err != nil {
			t.Fatal(err)
		}
		features, err := a.Features(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, op := range []execution.Op{execution.OpSteer, execution.OpQueue, execution.OpActivity} {
			if avail := features.Operations[op]; !avail.Available {
				t.Errorf("%s unavailable: %s", op, avail.Reason)
			}
		}

		running, err := svc.Coordinator.StartTask(ctx, task.ID)
		if err != nil || running.State != db.StateRunning || running.Session == nil {
			t.Fatalf("start: %+v %v", running, err)
		}
		ref := *running.Session
		id := ref.ExternalSessionID
		const steer, queued = "Check the edge cases", "Add a regression test"
		for _, msg := range []execution.Message{
			{Text: queued, Delivery: execution.DeliveryQueue},
			{Text: steer, Delivery: execution.DeliverySteer},
		} {
			got, err := svc.Coordinator.Send(ctx, task.ID, msg, nil)
			if err != nil || got.State != db.StateRunning || got.Session == nil || *got.Session != ref {
				t.Fatalf("%s: %+v %v", msg.Delivery, got, err)
			}
		}

		body := claudeDeliveryHook(t, f, id, "PostToolUse")
		if body.Context.Event != "PostToolUse" || !strings.Contains(body.Context.Text, steer) || strings.Contains(body.Context.Text, queued) {
			t.Fatalf("PostToolUse response: %+v", body)
		}
		if code := f.Hook(id, "PostToolUse", nil); code != http.StatusNoContent {
			t.Fatalf("PostToolUse after steer: HTTP %d", code)
		}
		body = claudeDeliveryHook(t, f, id, "Stop")
		if body.Decision != "block" || !strings.Contains(body.Reason, queued) || strings.Contains(body.Reason, steer) {
			t.Fatalf("Stop response: %+v", body)
		}
		if code := f.Hook(id, "Stop", nil); code != http.StatusNoContent {
			t.Fatalf("Stop after queue: HTTP %d", code)
		}
		if launches := f.Launches(); len(launches) != 1 {
			t.Fatalf("hook delivery launched Claude again: %+v", launches)
		}
	})
}

type claudeHookOutput struct {
	Context struct {
		Event string `json:"hookEventName"`
		Text  string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// Read the hook's response body using the URL and auth headers passed to Claude.
func claudeDeliveryHook(t *testing.T, f *claudetest.Fake, id, event string) claudeHookOutput {
	t.Helper()
	call := f.HookResponse(id, event, nil)
	if call.Err != nil || call.Status != http.StatusOK {
		t.Fatalf("%s: HTTP %d %s %v", event, call.Status, call.Body, call.Err)
	}
	var out claudeHookOutput
	if err := json.Unmarshal([]byte(call.Body), &out); err != nil {
		t.Fatalf("%s: invalid response %q: %v", event, call.Body, err)
	}
	return out
}

func TestContractClaudeActivity(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		ctx := t.Context()
		f := claudetest.New(t)
		_, svc, task, _ := claudeSetup(t, p, f)
		running, err := svc.Coordinator.StartTask(ctx, task.ID)
		if err != nil || running.Session == nil {
			t.Fatalf("start: %+v %v", running, err)
		}
		id := running.Session.ExternalSessionID
		const prompt, reply = "Check the edge cases", "The tests pass."
		f.Reply(id, "The initial work is complete.", true)
		f.Prompt(id, prompt)
		f.Reply(id, reply, true)
		f.SetState(id, "done", "idle", "")

		out, err := svc.Coordinator.Activity(ctx, task.ID, nil, execution.ActivityQuery{Limit: 2})
		if err != nil || len(out.Items) != 2 || !out.Truncated || out.Model != "claude-haiku-5-5" {
			t.Fatalf("activity: %+v %v", out, err)
		}
		if user, agent := out.Items[0], out.Items[1]; user.Kind != "message_user" || user.Text != prompt || agent.Kind != "message_agent" || agent.Text != reply {
			t.Fatalf("activity items: %+v", out.Items)
		}
	})
}

func TestContractClaudeAdoptedWorkingSendIsBusy(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		ctx := t.Context()
		f := claudetest.New(t)
		store, svc, task, _ := claudeSetupWith(t, p, f, f.HookedAdapter(claudeConn, f.Hooks()))

		res, err := f.CLI().Launch(ctx, f.Workspace, "--bg", "--", "Started outside Stage")
		if err != nil || res.Code != 0 {
			t.Fatalf("launch: %+v %v", res, err)
		}
		sessions := f.Read().Sessions
		if len(sessions) != 1 {
			t.Fatalf("Claude sessions: %+v", sessions)
		}
		ref := execution.SessionRef{ConnectionID: claudeConn, ExternalSessionID: sessions[0].SessionID}
		if err := svc.Coordinator.RefreshScopedSession(ctx, ref); err != nil {
			t.Fatal(err)
		}
		adopted, created, err := store.AdoptSession(ctx, db.AdoptInput{Session: ref, ProjectID: &task.ProjectID})
		if err != nil || !created || adopted.State != db.StateRunning {
			t.Fatalf("adopt: %+v %v", adopted, err)
		}

		for _, mode := range []execution.Delivery{"", execution.DeliverySteer, execution.DeliveryQueue} {
			_, err := svc.Coordinator.Send(ctx, adopted.ID, execution.Message{Text: "follow-up", Delivery: mode}, nil)
			var conflict *db.ConflictError
			if !errors.As(err, &conflict) || conflict.Code != "session_busy" {
				t.Fatalf("working adopted session, delivery %q: %v", mode, err)
			}
			if mode == "" && !strings.Contains(conflict.Msg, "is working") {
				t.Fatalf("legacy send guidance: %s", conflict.Msg)
			}
		}
	})
}
