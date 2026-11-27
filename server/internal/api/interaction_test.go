package api_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// T3 offers no interaction operations: the generic routes answer 409
// operation_unavailable instead of limiting anything else.
func TestInteractionRoutesUnavailable(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/interaction")
	backlog := e.task(p.ID, "not started", nil)
	if code, body := e.raw("GET", fmt.Sprintf("/api/tasks/%d/requests", backlog.ID), ""); code != 409 || body["code"] == "operation_unavailable" {
		t.Fatalf("no session: %d %v", code, body)
	}
	a := e.task(p.ID, "running", map[string]any{"start": true})
	var caps execution.Capabilities
	e.must("GET", fmt.Sprintf("/api/connections/%d/capabilities", p.Bindings[0].ConnectionID), nil, 200, &caps)
	for _, c := range []struct {
		method, path, body string
		op                 execution.Op
	}{
		{"GET", fmt.Sprintf("/api/tasks/%d/requests", a.ID), "", execution.OpRespond},
		{"POST", fmt.Sprintf("/api/tasks/%d/requests/q1", a.ID), `{"option":"yes"}`, execution.OpRespond},
		{"POST", fmt.Sprintf("/api/tasks/%d/messages", a.ID), `{"text":"hello"}`, execution.OpSend},
	} {
		reason := caps.Operations[c.op].Reason
		code, body := e.raw(c.method, c.path, c.body)
		message, _ := body["error"].(string)
		if code != 409 || body["code"] != "operation_unavailable" || reason == "" || !strings.HasSuffix(message, reason) {
			t.Errorf("%s %s: %d %v", c.method, c.path, code, body)
		}
	}
	for _, c := range []struct{ path, body string }{
		{fmt.Sprintf("/api/tasks/%d/messages", a.ID), `{"text":" "}`},
		{fmt.Sprintf("/api/tasks/%d/messages", a.ID), `{"text":"x","extra":1}`},
		{fmt.Sprintf("/api/tasks/%d/requests/q1", a.ID), `{}`},
	} {
		if code, body := e.raw("POST", c.path, c.body); code != 400 {
			t.Errorf("POST %s %s: %d %v", c.path, c.body, code, body)
		}
	}
	if code, _ := e.raw("GET", "/api/tasks/99999/requests", ""); code != 404 {
		t.Errorf("unknown task: %d", code)
	}
}

// Only the user answers or sends: a request naming an agent's session, or
// forwarded by a worker's agent gateway, is refused before anything else;
// listing stays open.
func TestRespondRefusesAgents(t *testing.T) {
	e := gatewayEnv(t)
	j := e.join("laptop")
	p := e.project("/w/agents")
	a := e.task(p.ID, "running", map[string]any{"start": true})
	var out struct {
		Code string `json:"code"`
	}
	headers := map[string]string{buildinfo.APIVersionHeader: apiVersion, agentctx.CallerHeader: "1:agent-1"}
	for _, c := range []struct {
		path string
		body map[string]any
	}{
		{fmt.Sprintf("/api/tasks/%d/requests/q1", a.ID), map[string]any{"option": "allow"}},
		{fmt.Sprintf("/api/tasks/%d/messages", a.ID), map[string]any{"text": "hello"}},
	} {
		if rec := raw(t, e.h, "POST", c.path, headers, c.body, &out); rec.Code != 403 || out.Code != "agent_forbidden" {
			t.Fatalf("caller session %s: %d %s", c.path, rec.Code, rec.Body)
		}
		if rec := e.gw(j.Token, "POST", c.path, c.body, &out); rec.Code != 403 || out.Code != "agent_forbidden" {
			t.Fatalf("gateway %s: %d %s", c.path, rec.Code, rec.Body)
		}
	}
	if rec := e.gw(j.Token, "GET", fmt.Sprintf("/api/tasks/%d/requests", a.ID), nil, &out); rec.Code != 409 || out.Code != "operation_unavailable" {
		t.Fatalf("gateway listing: %d %s", rec.Code, rec.Body)
	}
}
