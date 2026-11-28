package codex_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/execution"
)

func TestSteerTargetsTheActiveCodexTurn(t *testing.T) {
	r := newRig(t)
	ref := launched(t, r)
	id := ref.ExternalSessionID
	m := execution.Message{Text: "Run lint first.", RequestID: "stage-steer-1", Delivery: execution.DeliverySteer}

	wantBusy := func(reason string) {
		t.Helper()
		var busy *execution.Busy
		if err := r.adapter.Send(t.Context(), ref, m); !errors.As(err, &busy) || busy.Reason != reason {
			t.Fatalf("want %q, got %v", reason, err)
		}
	}
	for _, kind := range []string{"review", "compact"} {
		r.srv.SetTurnKind(id, kind)
		wantBusy("Codex can't steer a review or compaction turn. Wait for it to finish.")
	}
	r.srv.SetTurnKind(id, "")
	for _, race := range []string{"ended", "replaced"} {
		r.srv.SteerRace(race)
		wantBusy("The turn ended; send instead.")
	}
	if err := r.adapter.Send(t.Context(), ref, m); err != nil {
		t.Fatal(err)
	}
	th := r.srv.Thread(id)
	if len(th.Turns) != 1 || len(th.Turns[0].ExtraItems) != 1 || th.Turns[0].ExtraItems[0]["clientId"] != m.RequestID {
		t.Fatalf("steer escaped the original turn: %+v", th)
	}
	r.srv.Finish(id, "completed", "Lint passed.")
	wantBusy("The turn ended; send instead.")
	m.Delivery = execution.DeliveryQueue
	var invalid *execution.Invalid
	if err := r.adapter.Send(t.Context(), ref, m); !errors.As(err, &invalid) || invalid.Reason != "Codex queues messages only in its own client." {
		t.Fatal(err)
	}
}

func TestCodexDeliveryAndActivityRequireMethods(t *testing.T) {
	r := newRig(t)
	r.daemon.VersionTag = "0.161.0"
	r.srv.Unsupported("turn/start")
	r.srv.Unsupported("turn/steer")
	r.srv.Unsupported("thread/items/list")
	caps, err := r.adapter.Features(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []execution.Op{execution.OpSend, execution.OpSteer, execution.OpActivity, execution.OpQueue} {
		if state := caps.Operations[op]; state.Available || state.Reason == "" {
			t.Fatalf("%s: %+v", op, state)
		}
	}
	for _, method := range []string{"turn/start", "turn/steer", "thread/items/list"} {
		r.srv.Supported(method)
	}
	r.client.Close()
	caps, err = r.adapter.Features(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []execution.Op{execution.OpSend, execution.OpSteer, execution.OpActivity} {
		if !caps.Operations[op].Available {
			t.Fatalf("%s: %+v", op, caps.Operations[op])
		}
	}
	if caps.Operations[execution.OpQueue].Available {
		t.Fatal("experimental queue enabled")
	}
	if caps.Operations[execution.OpRespond].Available {
		t.Fatal("approval replay enabled before the supported version")
	}
	r.srv.Unsupported("thread/turns/list")
	r.client.Close()
	caps, err = r.adapter.Features(t.Context())
	if err != nil || caps.Operations[execution.OpSend].Available || caps.Operations[execution.OpSteer].Available ||
		!caps.Operations[execution.OpActivity].Available {
		t.Fatalf("missing turn lookup: %+v %v", caps.Operations, err)
	}
}

func TestCodexActivityMapsRecentItemsAndMetadata(t *testing.T) {
	r := newRig(t)
	ref := launched(t, r)
	id := ref.ExternalSessionID
	change := func(id, typ, diff string) map[string]any {
		changes := []map[string]any{{"path": "README.md", "kind": map[string]any{"type": typ}, "diff": diff}}
		if typ == "update" {
			changes = append(changes, map[string]any{"path": "extra.txt", "kind": map[string]any{"type": "update"}})
		}
		return map[string]any{"type": "fileChange", "id": id, "status": "completed",
			"changes": changes}
	}
	r.srv.AddItems(id,
		map[string]any{"type": "plan", "id": "old-plan", "text": "- [ ] Old"},
		map[string]any{"type": "reasoning", "id": "r", "summary": []string{"", "Checked the requirements\nIgnore this line"}, "content": []string{"raw reasoning"}},
		map[string]any{"type": "commandExecution", "id": "c", "command": "go test ./...", "status": "failed", "exitCode": 1, "aggregatedOutput": "large output"},
		change("f1", "add", "hello\nworld\n"),
		change("f2", "update", "@@ -1,2 +1,3 @@\n-hello\n+HELLO\n world\n+again\n"),
		map[string]any{"type": "mcpToolCall", "id": "m", "server": "stage", "tool": "get_task", "status": "inProgress"},
		map[string]any{"type": "plan", "id": "p", "text": "- [x] Read the code\n- [ ] Fix it"},
		map[string]any{"type": "unrecognized", "id": "skipped", "text": "ignore"},
	)
	r.srv.Finish(id, "completed", "Fixed.")
	a, err := r.adapter.Activity(t.Context(), ref, execution.ActivityQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(a.Items))
	for _, it := range a.Items {
		got = append(got, it.Kind)
	}
	want := []string{"plan", "reasoning", "command", "file_change", "file_change", "tool", "plan", "message_agent", "message_agent"}
	if !a.Truncated || !slices.Equal(got, want) || a.Items[1].At.IsZero() {
		t.Fatalf("items %+v, truncated %v", a.Items, a.Truncated)
	}
	if it := a.Items[1]; it.Title != "Checked the requirements" || it.Text != "" || it.Preview != "" {
		t.Fatalf("reasoning %+v", it)
	}
	if it := a.Items[2]; it.Preview != "go test ./..." || it.Status != "failed" || !strings.Contains(it.Title, "exit 1") || it.Text != "" {
		t.Fatalf("command %+v", it)
	}
	if it := a.Items[5]; it.Preview != "stage/get_task" || it.Status != "running" {
		t.Fatalf("tool %+v", it)
	}
	if !slices.Equal(a.Plan, []execution.PlanStep{{Text: "Read the code", Status: "done"}, {Text: "Fix it", Status: "pending"}}) {
		t.Fatalf("plan %+v", a.Plan)
	}
	if !slices.Equal(a.Files, []execution.FileChange{
		{Path: "README.md", Status: "modified", Additions: 4, Deletions: 1},
		{Path: "extra.txt", Status: "modified", CountsUnknown: true},
	}) {
		t.Fatalf("files %+v", a.Files)
	}
	var details map[string]string
	if err := json.Unmarshal(a.Details, &details); err != nil || a.Model != "gpt-test" || details["model"] != a.Model ||
		details["reasoningEffort"] != "high" || details["approvalPolicy"] != "on-request" || details["sandbox"] != "workspaceWrite" {
		t.Fatalf("details %s %v", a.Details, err)
	}
	// Unloaded and archived attempts can read history without loading it.
	r.srv.Archive(id)
	resumes := r.srv.Calls("thread/resume")
	a, err = r.adapter.Activity(t.Context(), ref, execution.ActivityQuery{})
	if err != nil || len(a.Items) == 0 || a.Items[0].Kind != "message_user" || a.Items[0].Text != "Fix it" ||
		r.srv.Calls("thread/resume") != resumes {
		t.Fatalf("archived activity %+v %v", a, err)
	}
}
