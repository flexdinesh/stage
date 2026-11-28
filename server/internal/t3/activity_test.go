package t3

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestActivityMapsT3Timeline(t *testing.T) {
	f := newFakeT3(t)
	f.tool("t3_thread_read", func(args map[string]any) (*mcp.CallToolResult, error) {
		if args["view"] != "activity" || args["limit"] != float64(30) || args["maxCharsPerItem"] != float64(2000) || args["threadId"] != "scratch" {
			t.Errorf("read args: %v", args)
		}
		return structured(`{
			"thread":{"threadId":"scratch","providerInstanceId":"codex","model":"gpt","runtimeMode":"auto",
				"interactionMode":"plan","branch":"test","worktreePath":"/scratch","updatedAt":"2026-11-29T01:00:00Z"},
			"hasMore":false,"earlierPosition":5,
			"items":[
				{"position":14,"itemId":"a","visibility":"local","type":"assistant_message","text":"Reply","status":"completed","updatedAt":"2026-11-29T00:59:00Z"},
				{"position":13,"itemId":"b","visibility":"synthetic","type":"assistant_message","text":"Synthetic"},
				{"position":12,"itemId":"c","visibility":"inherited","type":"assistant_message","text":"Inherited"},
				{"position":11,"itemId":"d","visibility":"local","type":"file_change","text":"a.go\n+2 -1\ndiff"},
				{"position":10,"itemId":"e","visibility":"local","type":"file_change","text":"b.go"},
				{"position":9,"itemId":"f","visibility":"local","type":"file_change","text":"a.go\n+3 -0"},
				{"position":8,"itemId":"g","visibility":"local","type":"todo_list","text":"Testing\n[completed] Write\n[in_progress] Run\n[pending] Review"},
				{"position":7,"itemId":"h","visibility":"local","type":"dynamic_tool","text":"tool result"},
				{"position":6,"itemId":"i","visibility":"local","type":"command_execution","text":"$ go test ./...\noutput","status":"running"},
				{"position":5,"itemId":"j","visibility":"local","type":"user_message","text":"Check"}
			]
		}`), nil
	})
	a := NewAdapter(newTestClient(t, f, f.adminToken, NewMemoryTokenStore()))
	ref := execution.SessionRef{ConnectionID: 1, ExternalSessionID: "scratch"}
	got, err := a.Activity(t.Context(), ref, execution.ActivityQuery{})
	if err != nil {
		t.Fatal(err)
	}
	wantKinds := []string{"message_user", "command", "tool", "plan", "file_change", "file_change", "file_change", "message_agent"}
	kinds := make([]string, 0, len(got.Items))
	for _, item := range got.Items {
		kinds = append(kinds, item.Kind)
	}
	if !slices.Equal(kinds, wantKinds) || got.Items[1].Preview != "go test ./..." || got.Items[1].Status != "running" ||
		got.Items[4].Preview != "a.go" || got.Items[7].Status != "done" || got.Items[7].At.IsZero() {
		t.Fatalf("items: %+v", got.Items)
	}
	wantFiles := []execution.FileChange{{Path: "a.go", Additions: 5, Deletions: 1}, {Path: "b.go", CountsUnknown: true}}
	wantPlan := []execution.PlanStep{{Text: "Write", Status: "done"}, {Text: "Run", Status: "in_progress"}, {Text: "Review", Status: "pending"}}
	if !reflect.DeepEqual(got.Files, wantFiles) || !reflect.DeepEqual(got.Plan, wantPlan) {
		t.Fatalf("files %+v, plan %+v", got.Files, got.Plan)
	}
	var details activityDetails
	wantDetails := activityDetails{ProviderInstance: "codex", Model: "gpt", RuntimeMode: RuntimeAuto, InteractionMode: InteractionPlan, Branch: "test", Worktree: "/scratch"}
	if err := json.Unmarshal(got.Details, &details); err != nil || details != wantDetails ||
		got.Model != "gpt" || !got.Truncated || !got.At.Equal(time.Date(2026, 11, 29, 1, 0, 0, 0, time.UTC)) {
		t.Fatalf("activity %+v details %+v: %v", got, details, err)
	}
	ref.ConnectionID++
	if _, err := a.Activity(t.Context(), ref, execution.ActivityQuery{}); !errors.Is(err, execution.ErrUnavailable) {
		t.Fatal("wrong connection", err)
	}
}

func TestAdvertisedSessionTools(t *testing.T) {
	f := newFakeT3(t)
	notCalled := func(map[string]any) (*mcp.CallToolResult, error) {
		t.Error("discover tools without calling them")
		return structured(`{}`), nil
	}
	f.tool("t3_thread_read", notCalled)
	f.tool("t3_thread_send", notCalled)
	f.tool("orchestrator_capabilities", func(map[string]any) (*mcp.CallToolResult, error) {
		return structured(`{"providers":[]}`), nil
	})
	a := NewAdapter(newTestClient(t, f, f.adminToken, NewMemoryTokenStore()))
	c, err := a.Features(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if c.Operations[execution.OpRespond].Available || !c.Operations[execution.OpActivity].Available || !c.Operations[execution.OpQueue].Available {
		t.Fatal(c.Operations)
	}
}

func TestActivityReadsTailOnForwardOnlyT3(t *testing.T) {
	f := newFakeT3(t)
	f.tool("t3_thread_read", func(args map[string]any) (*mcp.CallToolResult, error) {
		switch args["afterPosition"] {
		case nil:
			return structured(`{"thread":{"itemCount":8},"items":[{"position":0,"itemId":"first","visibility":"local","type":"user_message"}],"nextPosition":0,"hasMore":true}`), nil
		case float64(6):
			return structured(`{"thread":{"itemCount":8},"items":[{"position":7,"itemId":"last","visibility":"local","type":"assistant_message"}],"nextPosition":7,"hasMore":false}`), nil
		default:
			t.Errorf("unexpected read: %v", args)
			return structured(`{}`), nil
		}
	})
	a := NewAdapter(newTestClient(t, f, f.adminToken, NewMemoryTokenStore()))
	ref := execution.SessionRef{ConnectionID: 1, ExternalSessionID: "forward-only"}
	got, err := a.Activity(t.Context(), ref, execution.ActivityQuery{Limit: 1})
	if err != nil || len(got.Items) != 1 || got.Items[0].ID != "last" || !got.Truncated {
		t.Fatal(got, err)
	}
}
