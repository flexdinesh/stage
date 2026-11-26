package agenttools_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func TestActionToolsApplyAgentPolicyAndReturnStructuredResults(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Stage-Action-Agent") != "1" || r.Header.Get("Stage-Action-Confirmed") != "" {
			t.Errorf("headers %v", r.Header)
		}
		switch r.Method + " " + r.URL.RequestURI() {
		case "GET /api/projects/8/actions?bindingId=12&connectionId=4":
			if got := r.Header.Get("Stage-Caller-Session"); got != "" {
				t.Errorf("unexpected provenance %q", got)
			}
			json.NewEncoder(w).Encode([]execution.Action{{ID: "inspect", Label: "Inspect"}})
		case "POST /api/tasks/2/actions/inspect":
			if got := r.Header.Get("Stage-Caller-Session"); got != "5:agent" {
				t.Errorf("caller %q", got)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil || string(body) != `{"label":"sample"}` {
				t.Errorf("input %q, %v", body, err)
			}
			json.NewEncoder(w).Encode(execution.ActionResult{
				Message: "Inspected", Facts: []execution.Fact{{ID: "path", Label: "Path", Value: "/tmp"}},
				Link: "https://example.test/detail", JournalError: "journal offline",
			})
		case "POST /api/connections/4/actions/user-only":
			if got := r.Header.Get("Stage-Caller-Session"); got != "" {
				t.Errorf("unexpected provenance %q", got)
			}
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"error":"Action is available only to users.","code":"agent_forbidden"}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	cs := connect(t, &agenttools.Ops{Client: client.New(srv.URL)})
	var listed agenttools.ActionsOut
	mustCall(t, cs, "list_actions", map[string]any{"projectId": 8, "onConnectionId": 4, "bindingId": 12}, &listed)
	if len(listed.Actions) != 1 || listed.Actions[0].ID != "inspect" {
		t.Fatalf("actions %+v", listed.Actions)
	}
	var result execution.ActionResult
	mustCall(t, cs, "run_action", map[string]any{
		"taskId": 2, "actionId": "inspect", "input": map[string]any{"label": "sample"},
		"callerSession": map[string]any{"connectionId": 5, "externalSessionId": "agent"},
	}, &result)
	if result.Message != "Inspected" || result.JournalError != "journal offline" ||
		result.Link != "https://example.test/detail" || len(result.Facts) != 1 || result.Facts[0].Value != "/tmp" {
		t.Fatalf("result %+v", result)
	}
	if msg := call(t, cs, "run_action", map[string]any{"connectionId": 4, "actionId": "user-only"}, nil); !strings.Contains(msg, "only to users") {
		t.Fatalf("action error %q", msg)
	}
	for _, args := range []map[string]any{
		{"actionId": "inspect"},
		{"actionId": "inspect", "taskId": 2, "connectionId": 4},
		{"actionId": "inspect", "projectId": 8},
		{"actionId": "inspect", "taskId": 2, "onConnectionId": 4},
		{"actionId": "inspect", "taskId": 2, "bindingId": 12},
		{"actionId": "inspect", "taskId": 2, "input": []string{"invalid"}},
		{"actionId": "inspect", "taskId": 2, "confirmed": true},
	} {
		if msg := call(t, cs, "run_action", args, nil); msg == "" {
			t.Fatalf("accepted invalid arguments %+v", args)
		}
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("action requests %d", got)
	}
}
