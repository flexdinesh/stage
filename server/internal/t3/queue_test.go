package t3

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/flexdinesh/stage/server/internal/execution"
)

func TestQueueManageUsesToolsList(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tools []string
		want  bool
	}{
		{name: "available", tools: []string{queueTool, "t3_thread_read"}, want: true},
		{name: "missing queue tool", tools: []string{"t3_thread_read", "t3_thread_send"}},
		{name: "missing thread read", tools: []string{queueTool}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeT3(t)
			capabilitiesTool(f)
			for _, tool := range tc.tools {
				f.tool(tool, func(map[string]any) (*mcp.CallToolResult, error) {
					return nil, errors.New("feature discovery should only list this tool")
				})
			}
			a := NewAdapter(newTestClient(t, f, f.adminToken, NewMemoryTokenStore()))
			features, err := a.Features(ctxT(t))
			got := features.Operations[execution.OpQueueManage]
			if err != nil || got.Available != tc.want || !tc.want && got.Reason == "" {
				t.Fatalf("queue management = %+v, %v, want availability %v", got, err, tc.want)
			}
		})
	}
}

func TestQueueManagementMissingThread(t *testing.T) {
	a := NewAdapter(NewFake())
	ctx := t.Context()
	ref := execution.SessionRef{ConnectionID: 1, ExternalSessionID: "missing"}
	if _, err := a.Queued(ctx, ref); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("list missing thread: %v", err)
	}
	if err := a.PromoteQueued(ctx, ref, "queued"); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("promote on missing thread: %v", err)
	}
}

func TestMCPQueuePages(t *testing.T) {
	f := newFakeT3(t)
	f.tool(queueTool, func(args map[string]any) (*mcp.CallToolResult, error) {
		if args["threadId"] != "thread-1" || args["action"] != "list" || args["limit"] != float64(100) {
			return nil, fmt.Errorf("unexpected queue request %v", args)
		}
		switch args["cursor"] {
		case float64(0):
			return structured(`{"items":[{"queuedRunId":"run-5","text":"first","truncated":true}],"nextCursor":2}`), nil
		case float64(2):
			return structured(`{"items":[{"queuedRunId":"run-3","text":"second","truncated":false}],"nextCursor":null}`), nil
		default:
			return nil, fmt.Errorf("unexpected cursor %v", args["cursor"])
		}
	})
	c := newTestClient(t, f, f.adminToken, NewMemoryTokenStore())
	got, err := c.ListQueued(ctxT(t), "thread-1")
	want := []QueuedRun{{ID: "run-5", Text: "first"}, {ID: "run-3", Text: "second"}}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("queue = %+v, %v, want %+v", got, err, want)
	}
}

func TestMCPQueueRejectsStuckCursor(t *testing.T) {
	f := newFakeT3(t)
	f.tool(queueTool, func(map[string]any) (*mcp.CallToolResult, error) {
		return structured(`{"items":[{"queuedRunId":"run-3","text":"next"}],"nextCursor":0}`), nil
	})
	c := newTestClient(t, f, f.adminToken, NewMemoryTokenStore())
	if got, err := c.ListQueued(ctxT(t), "thread-1"); got != nil || err == nil || !strings.Contains(err.Error(), "cursor did not advance") {
		t.Fatalf("queue = %+v, %v", got, err)
	}
}

func TestMCPQueueWrites(t *testing.T) {
	for _, tc := range []struct {
		name, action, message string
		want                  error
	}{
		{name: "cancel", action: "cancel"},
		{name: "promote", action: "promote_to_steer"},
		{name: "cancel gone", action: "cancel", message: "Run queued-1 is not queued.", want: execution.ErrNotFound},
		{name: "promote gone", action: "promote_to_steer", message: "Queued run queued-1 is not queued.", want: execution.ErrNotFound},
		{name: "turn changed", action: "promote_to_steer", message: "Target run active-1 is completed and cannot be steered.", want: &execution.Busy{}},
		{name: "refused", action: "promote_to_steer", message: "Automatic completion deliveries cannot be promoted to Steer.", want: &execution.Refused{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeT3(t)
			f.tool("t3_thread_read", func(args map[string]any) (*mcp.CallToolResult, error) {
				if tc.action != "promote_to_steer" || args["threadId"] != "thread-1" {
					return nil, fmt.Errorf("unexpected thread request %v", args)
				}
				return structured(`{"thread":{"threadId":"thread-1","activeRunId":"active-1","latestRunId":"queued-1"}}`), nil
			})
			f.tool(queueTool, func(args map[string]any) (*mcp.CallToolResult, error) {
				want := map[string]any{"threadId": "thread-1", "queuedRunId": "queued-1", "action": tc.action}
				if tc.action == "promote_to_steer" {
					want["targetRunId"] = "active-1"
				}
				if !maps.Equal(args, want) {
					return nil, fmt.Errorf("queue request %v, want %v", args, want)
				}
				if tc.message == "" {
					return structured(`{"sequence":39}`), nil
				}
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{
					Text: fmt.Sprintf(`{"_tag":"OrchestratorMcpFailure","code":"orchestration_error","message":%q}`, tc.message),
				}}}, nil
			})
			a := NewAdapter(newTestClient(t, f, f.adminToken, NewMemoryTokenStore()))
			ref := execution.SessionRef{ConnectionID: 1, ExternalSessionID: "thread-1"}
			var err error
			if tc.action == "cancel" {
				err = a.CancelQueued(ctxT(t), ref, "queued-1")
			} else {
				err = a.PromoteQueued(ctxT(t), ref, "queued-1")
			}
			var busy *execution.Busy
			var refused *execution.Refused
			switch tc.want.(type) {
			case *execution.Busy:
				if !errors.As(err, &busy) || busy.Reason != tc.message {
					t.Fatalf("error = %v, want busy with native reason", err)
				}
			case *execution.Refused:
				if !errors.As(err, &refused) || refused.Reason != tc.message {
					t.Fatalf("error = %v, want refusal with native reason", err)
				}
			default:
				if !errors.Is(err, tc.want) {
					t.Fatalf("error = %v, want %v", err, tc.want)
				}
			}
		})
	}
}

func TestQueueManagement(t *testing.T) {
	f := NewFake()
	a := NewAdapter(f)
	ref := interactionThread(t, f)
	ctx := t.Context()
	for _, prompt := range []string{"follow up", "cancel this", "steer now"} {
		if err := a.Send(ctx, ref, execution.Message{Text: prompt, Delivery: execution.DeliveryQueue}); err != nil {
			t.Fatal(err)
		}
	}
	queued, err := a.Queued(ctx, ref)
	if err != nil || len(queued) != 3 || queued[0].Text != "follow up" || queued[1].Text != "cancel this" || queued[2].Text != "steer now" {
		t.Fatalf("queue = %+v, %v", queued, err)
	}
	for _, item := range queued {
		if item.ID == "" || item.Delivery != execution.DeliveryQueue || !item.CreatedAt.IsZero() || item.Source != "" {
			t.Fatalf("queued item = %+v", item)
		}
	}
	if err := a.CancelQueued(ctx, ref, queued[1].ID); err != nil {
		t.Fatal(err)
	}
	if err := a.PromoteQueued(ctx, ref, queued[2].ID); err != nil {
		t.Fatal(err)
	}
	if got := f.Sent[len(f.Sent)-1]; got.Mode != SendSteer || got.Message != "steer now" {
		t.Fatalf("promoted send = %+v", got)
	}
	if got, err := a.Queued(ctx, ref); err != nil || !slices.Equal(got, queued[:1]) {
		t.Fatalf("queue after writes = %+v, %v", got, err)
	}
	if err := f.SetStatus(ref.ExternalSessionID, StatusCompleted); err != nil {
		t.Fatal(err)
	}
	if thread, err := f.ReadThread(ctx, ref.ExternalSessionID); err != nil || thread.ActiveRunID != queued[0].ID {
		t.Fatalf("active run = %q, %v, want %q", thread.ActiveRunID, err, queued[0].ID)
	}
	if err := a.CancelQueued(ctx, ref, queued[0].ID); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("cancel consumed item = %v", err)
	}
}

func TestPromoteQueuedWhileIdle(t *testing.T) {
	f := NewFake()
	a := NewAdapter(f)
	ref := interactionThread(t, f)
	ctx := t.Context()
	if err := a.Send(ctx, ref, execution.Message{Text: "follow up", Delivery: execution.DeliveryQueue}); err != nil {
		t.Fatal(err)
	}
	if err := f.Interrupt(ctx, ref.ExternalSessionID); err != nil {
		t.Fatal(err)
	}
	queued, err := a.Queued(ctx, ref)
	if err != nil || len(queued) != 1 {
		t.Fatalf("held queue = %+v, %v", queued, err)
	}
	var busy *execution.Busy
	if err := a.PromoteQueued(ctx, ref, queued[0].ID); !errors.As(err, &busy) || len(f.Sent) != 1 {
		t.Fatalf("promote without active run = %v, sends = %+v", err, f.Sent)
	}
	if got, err := a.Queued(ctx, ref); err != nil || !slices.Equal(got, queued) {
		t.Fatalf("held queue after promotion = %+v, %v", got, err)
	}
	if err := a.PromoteQueued(ctx, ref, "gone"); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("promote gone item without active run = %v", err)
	}
}
