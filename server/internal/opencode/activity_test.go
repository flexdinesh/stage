package opencode

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/opencode/opencodetest"
)

func TestSessionActivity(t *testing.T) {
	fake := opencodetest.New(t, "")
	a, err := New(liveRef.ConnectionID, Settings{Endpoint: fake.URL})
	if err != nil {
		t.Fatal(err)
	}
	todo := func(status string) map[string]any {
		return map[string]any{"type": "tool", "name": "todowrite", "state": map[string]any{"status": "completed", "input": map[string]any{
			"todos": []map[string]string{{"content": "Verify", "status": status}},
		}}}
	}
	session := opencodetest.Session{
		Info: map[string]any{
			"id": liveSession, "projectID": "proj", "location": map[string]string{"directory": "/workspace"},
			"time": map[string]int64{"created": 1000, "updated": 6000, "busy": 2000}, "agent": "build",
			"model": map[string]string{"providerID": "fixture", "id": "test", "variant": "high"}, "cost": 0.42,
			"tokens": map[string]any{"input": 21, "output": 13, "reasoning": 5, "cache": map[string]int{"read": 8, "write": 3}},
		},
		Running: true,
		Messages: []map[string]any{
			{"id": "msg_assistant", "type": "assistant", "time": map[string]int64{"created": 3000}, "content": []map[string]any{
				todo("pending"),
				{"type": "reasoning", "text": "private reasoning", "time": map[string]int64{"created": 4000}},
				{"type": "tool", "name": "shell", "state": map[string]any{"status": "running", "input": map[string]string{"command": "go test"}}},
				{"type": "tool", "name": "read", "state": map[string]any{"status": "error", "input": map[string]string{"filePath": "old.go"}}},
				todo("completed"),
				{"type": "tool", "name": "write", "state": map[string]any{"status": "streaming", "input": `{"filePath":"incomplete`}},
				{"type": "text", "text": "Done"},
			}},
			{"id": "msg_user", "type": "user", "text": "Check", "time": map[string]int64{"created": 2000}},
		},
		Diff: []map[string]any{
			{"file": "file.go", "additions": 2, "deletions": 1, "status": "modified"},
			{"file": "file.go", "additions": 5, "deletions": 3, "status": "modified"},
			{"file": "other.go", "additions": 0, "deletions": 2, "status": "deleted"},
		},
	}
	// The latest page can be metadata only.
	session.Messages = append(make([]map[string]any, execution.MaxActivityLimit), session.Messages...)
	for i := range execution.MaxActivityLimit {
		session.Messages[i] = map[string]any{"type": "idle"}
	}
	fake.SetSession(liveSession, session)
	ctx := t.Context()
	activity, err := a.Activity(ctx, liveRef, execution.ActivityQuery{})
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{"message_user", "tool", "reasoning", "command", "tool", "tool", "tool", "message_agent"}
	if activity.Truncated || len(activity.Items) != len(kinds) || activity.Model != "fixture/test" || activity.At.UnixMilli() != 6000 {
		t.Fatalf("activity %+v", activity)
	}
	for i, item := range activity.Items {
		if item.Kind != kinds[i] || item.ID == "" {
			t.Fatalf("activity item %d: %+v", i, item)
		}
	}
	items := activity.Items
	if items[0].Text != "Check" || items[2].Title != "Reasoning" || items[2].Text != "" || items[2].Preview != "" ||
		items[2].At.UnixMilli() != 4000 || items[3].Preview != "go test" || items[3].Status != "running" || items[4].Preview != "old.go" ||
		items[4].Status != "failed" || items[5].Status != "done" || items[6].Status != "running" || items[6].Preview != "" ||
		items[7].Text != "Done" {
		t.Fatalf("incorrect activity parts %+v", items)
	}
	if !slices.Equal(activity.Plan, []execution.PlanStep{{Text: "Verify", Status: "done"}}) || !slices.Equal(activity.Files, []execution.FileChange{
		{Path: "file.go", Additions: 7, Deletions: 4, Status: "modified"}, {Path: "other.go", Deletions: 2, Status: "deleted"},
	}) {
		t.Fatalf("plan %+v, files %+v", activity.Plan, activity.Files)
	}
	var details activityDetails
	if err := json.Unmarshal(activity.Details, &details); err != nil || details.Agent != "build" || details.Model != "fixture/test" ||
		details.Variant != "high" || details.Tokens == nil || details.Tokens.Reasoning != 5 {
		t.Fatalf("details %s: %v", activity.Details, err)
	}
	o, err := a.Observe(ctx, liveRef)
	cost := 0.42
	if err != nil || !reflect.DeepEqual(o.Usage, &execution.Usage{InputTokens: 21, OutputTokens: 13, CacheReadTokens: 8, CacheWriteTokens: 3, CostUSD: &cost}) {
		t.Fatalf("usage %+v: %v", o.Usage, err)
	}
	last, err := a.Activity(ctx, liveRef, execution.ActivityQuery{Limit: 2})
	if err != nil || !last.Truncated || !slices.Equal(last.Items, items[6:]) || !slices.Equal(last.Plan, activity.Plan) {
		t.Fatalf("bounded activity %+v: %v", last, err)
	}
}

func TestActivityUnavailableWithoutDiff(t *testing.T) {
	fake := opencodetest.New(t, "")
	fake.WithoutDiff()
	a, err := New(liveRef.ConnectionID, Settings{Endpoint: fake.URL})
	if err != nil {
		t.Fatal(err)
	}
	c, err := a.Features(t.Context())
	if err != nil || c.Operations[execution.OpActivity].Reason != activityUnavailable || !c.Operations[execution.OpRespond].Available ||
		!c.Operations[execution.OpSend].Available || !c.Operations[execution.OpSteer].Available || !c.Operations[execution.OpQueue].Available {
		t.Fatalf("capabilities %+v: %v", c, err)
	}
	if _, err := a.Activity(t.Context(), liveRef, execution.ActivityQuery{}); err == nil || err.Error() != activityUnavailable {
		t.Fatalf("activity without diff: %v", err)
	}
	if _, err := a.Activity(t.Context(), execution.SessionRef{}, execution.ActivityQuery{}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("missing ref: %v", err)
	}
}
