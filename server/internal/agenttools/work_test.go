package agenttools_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
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
		} {
			if msg := call(t, cs, c.tool, c.args, nil); !strings.Contains(msg, c.want) {
				t.Fatalf("%s: %s on own task: %q", name, c.tool, msg)
			}
		}
		// Another task's session reaches the harness (T3 offers no interaction).
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
