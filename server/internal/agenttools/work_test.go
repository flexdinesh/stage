package agenttools_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// An agent completes another task with the two-step preview, which
// releases the auto dependent and is attributed to the agent and its
// session; it cannot complete, reopen, answer or message its own task or
// session, whether its caller comes from its environment or the HTTP MCP
// header.
func TestWorkToolsActOnOtherTasksOnly(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	p := e.project("/w/work")
	workspace := e.workspaceID(p.Bindings[0])
	a, err := e.client.CreateTask(ctx, client.CreateTask{ProjectID: p.ID, ConnectionID: dbtest.T3Connection, WorkspaceID: workspace, Title: "source", Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	auto := db.StartAuto
	b, err := e.client.CreateTask(ctx, client.CreateTask{ProjectID: p.ID, ConnectionID: dbtest.T3Connection, WorkspaceID: workspace, Title: "dependent", Prompt: "y", Needs: []int64{a.ID}, StartMode: &auto})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := e.client.CreateTask(ctx, client.CreateTask{ProjectID: p.ID, ConnectionID: dbtest.T3Connection, WorkspaceID: workspace, Title: "worker", Prompt: "z", Start: true})
	if err != nil || worker.ExternalSessionID == nil {
		t.Fatalf("worker: %+v %v", worker, err)
	}
	self := e.t3(*worker.ExternalSessionID)
	header := fmt.Sprintf("%d:%s", self.ConnectionID, self.ExternalSessionID)

	envCaller := connect(t, &agenttools.Ops{Client: e.client.AsToolTransport(), Caller: agentctx.Caller{Session: self}})
	for name, cs := range map[string]*mcp.ClientSession{"env": envCaller, "header": mustTransport(t, e, "http", header)} {
		var own db.CompletionPreview
		if msg := call(t, cs, "get_completion_preview", map[string]any{"taskId": worker.ID}, &own); msg != "" || own.EvidenceVersion == "" {
			t.Fatalf("%s: own preview %+v %s", name, own, msg)
		}
		for _, c := range []struct {
			tool, want string
			args       map[string]any
		}{
			{"complete_task", "complete its own task", map[string]any{"taskId": worker.ID, "revision": own.Revision, "evidenceVersion": own.EvidenceVersion, "requestId": "self"}},
			{"reopen_task", "reopen its own task", map[string]any{"taskId": worker.ID, "revision": own.Revision, "requestId": "self"}},
			{"respond_request", "answer its own session's request", map[string]any{"taskId": worker.ID, "requestId": "q1", "option": "allow"}},
			{"send_message", "message its own session", map[string]any{"taskId": worker.ID, "text": "hello"}},
			{"queued_messages", "change its own session's queue", map[string]any{"taskId": worker.ID, "action": "cancel", "id": "queued-run"}},
			{"queued_messages", "change its own session's queue", map[string]any{"taskId": worker.ID, "action": "steer", "id": "queued-run"}},
		} {
			if msg := call(t, cs, c.tool, c.args, nil); !strings.Contains(msg, c.want) {
				t.Fatalf("%s: %s on own task: %q", name, c.tool, msg)
			}
		}
		// Another task passes the self check and reaches the coordinator (a has no session yet).
		if msg := call(t, cs, "send_message", map[string]any{"taskId": a.ID, "text": "hello"}, nil); !strings.Contains(msg, "no execution session") {
			t.Fatalf("%s: send_message to another task: %q", name, msg)
		}
	}

	var preview db.CompletionPreview
	mustCall(t, envCaller, "get_completion_preview", map[string]any{"taskId": a.ID}, &preview)
	if len(preview.Dependents) != 1 || preview.Dependents[0].TaskID != b.ID {
		t.Fatalf("preview dependents %+v", preview.Dependents)
	}
	if msg := call(t, envCaller, "complete_task", map[string]any{"taskId": a.ID, "revision": preview.Revision, "evidenceVersion": "stale", "requestId": "stale"}, nil); !strings.Contains(msg, "Evidence changed") || !strings.Contains(msg, "get_completion_preview again") {
		t.Fatalf("stale evidence: %q", msg)
	}
	var result db.AcceptanceResult
	mustCall(t, envCaller, "complete_task", map[string]any{"taskId": a.ID, "revision": preview.Revision, "evidenceVersion": preview.EvidenceVersion, "requestId": "agent-accept", "note": "Verified by the worker."}, &result)
	if result.AcceptanceID == nil {
		t.Fatalf("complete_task %+v", result)
	}
	acceptances, err := e.store.Acceptances(ctx, a.ID)
	if err != nil || len(acceptances) != 1 {
		t.Fatalf("acceptances %+v %v", acceptances, err)
	}
	if stamp := acceptances[0].Attribution; stamp.Actor.Kind != "agent" || stamp.Cause == nil || stamp.Cause.Session == nil || *stamp.Cause.Session != *self {
		t.Fatalf("acceptance attribution %+v", stamp)
	}
	if err := e.runner.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := e.client.Task(ctx, b.ID)
	if err != nil || got.LaunchAttempt != 1 {
		t.Fatalf("agent completion did not release the auto dependent: %+v %v", got.Task, err)
	}
	var reopened db.AcceptanceResult
	mustCall(t, envCaller, "reopen_task", map[string]any{"taskId": a.ID, "revision": result.Revision, "requestId": "agent-reopen"}, &reopened)
	if reopened.Revision != result.Revision+1 || reopened.AcceptanceID != nil {
		t.Fatalf("reopen_task %+v", reopened)
	}
}

func TestSessionSurfaceTools(t *testing.T) {
	past := db.SessionRef{ConnectionID: 9, ExternalSessionID: "old"}
	current := db.SessionRef{ConnectionID: 9, ExternalSessionID: "current"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/tasks/7":
			_ = json.NewEncoder(w).Encode(client.TaskDetail{Sessions: []client.TaskSession{
				{Attempt: 2, Session: current, Current: true, State: "running"},
				{Attempt: 1, Session: past, State: "failed"},
			}})
		case "POST /api/tasks/7/messages":
			var in execution.Message
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Text != "hello" || in.Delivery != execution.DeliveryQueue {
				t.Errorf("message request %+v: %v", in, err)
			}
			_ = json.NewEncoder(w).Encode(client.Task{})
		case "GET /api/tasks/7/activity":
			if r.URL.Query().Get("limit") != "2" || r.URL.Query().Get("attempt") != "0" {
				t.Errorf("activity query %s", r.URL)
			}
			_ = json.NewEncoder(w).Encode(client.SessionActivity{Activity: execution.Activity{
				Items: []execution.ActivityItem{{ID: "last", Title: "Working", Kind: "event", Status: "running"}},
			}})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cs := connect(t, &agenttools.Ops{Client: client.New(srv.URL)})
	if msg := call(t, cs, "send_message", map[string]any{"taskId": 7, "text": "hello", "delivery": "restart"}, nil); !strings.Contains(msg, "enum") {
		t.Fatalf("unexpected MCP validation error %q", msg)
	}
	mustCall(t, cs, "send_message", map[string]any{"taskId": 7, "text": "hello", "delivery": "queue"}, nil)
	var task client.TaskDetail
	mustCall(t, cs, "get_task", map[string]any{"id": 7}, &task)
	if len(task.Sessions) != 2 || task.Sessions[0].Session != current || !task.Sessions[0].Current ||
		task.Sessions[1].Session != past || task.Sessions[1].Attempt != 1 || task.Sessions[1].Current {
		t.Fatalf("unexpected task history %+v", task.Sessions)
	}
	var out client.SessionActivity
	mustCall(t, cs, "get_session_activity", map[string]any{"taskId": 7, "attempt": 0, "limit": 2}, &out)
	if len(out.Activity.Items) != 1 || out.Activity.Items[0].ID != "last" {
		t.Fatalf("unexpected activity %+v", out)
	}
}

func TestQueuedMessagesTool(t *testing.T) {
	caller := &db.SessionRef{ConnectionID: 9, ExternalSessionID: "caller"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.EscapedPath() {
		case "GET /api/tasks/7/queue":
			_ = json.NewEncoder(w).Encode(client.SessionQueue{Messages: []execution.QueuedMessage{
				{ID: "queue/1", Text: "preview", Delivery: execution.DeliveryQueue},
			}})
		case "GET /api/tasks/8/queue":
			_ = json.NewEncoder(w).Encode(client.SessionQueue{Messages: []execution.QueuedMessage{}})
		case "DELETE /api/tasks/7/queue/queue%2F1", "POST /api/tasks/7/queue/queue%2F1/steer":
			if r.Header.Get(agentctx.CallerHeader) != "9:caller" {
				t.Error("write did not carry caller session")
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cs := connect(t, &agenttools.Ops{Client: client.New(srv.URL), Caller: agentctx.Caller{Session: caller}})
	for _, args := range []struct {
		in   map[string]any
		want string
	}{
		{map[string]any{"taskId": 7, "action": "edit", "id": "queue/1"}, "enum"},
		{map[string]any{"taskId": 7, "action": "list", "id": "queue/1"}, "id can only be used"},
		{map[string]any{"taskId": 7, "action": "steer"}, "id is required"},
	} {
		if msg := call(t, cs, "queued_messages", args.in, nil); !strings.Contains(msg, args.want) {
			t.Fatalf("unexpected MCP validation error %q, want %q", msg, args.want)
		}
	}
	var list client.SessionQueue
	mustCall(t, cs, "queued_messages", map[string]any{"taskId": 7, "action": "list"}, &list)
	if len(list.Messages) != 1 || list.Messages[0].ID != "queue/1" || list.Messages[0].Text != "preview" {
		t.Fatalf("queue %+v", list)
	}
	var empty client.SessionQueue
	mustCall(t, cs, "queued_messages", map[string]any{"taskId": 8, "action": "list"}, &empty)
	if empty.Messages == nil || len(empty.Messages) != 0 {
		t.Fatalf("empty queue %+v", empty)
	}
	for _, action := range []string{"cancel", "steer"} {
		var result agenttools.QueuedMessagesOut
		mustCall(t, cs, "queued_messages", map[string]any{"taskId": 7, "action": action, "id": "queue/1"}, &result)
		if !result.OK {
			t.Fatalf("%s result %+v", action, result)
		}
	}
}
