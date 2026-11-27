package api_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/api"
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

// Agents answer and message other tasks' sessions, never their own: a
// request whose caller session (header, directly or through a worker's
// agent gateway) owns the task is refused before anything else; another
// caller reaches the harness (T3: operation_unavailable).
func TestRespondRefusesOwnSession(t *testing.T) {
	e := gatewayEnv(t)
	j := e.join("laptop")
	p := e.project("/w/agents")
	a := e.task(p.ID, "running", map[string]any{"start": true})
	other := e.task(p.ID, "other", map[string]any{"start": true})
	sessionHeader := func(id int64) string {
		st, err := e.store.GetTask(t.Context(), id)
		if err != nil || st.Session == nil {
			t.Fatalf("task %d session: %v", id, err)
		}
		return fmt.Sprintf("%d:%s", st.Session.ConnectionID, st.Session.ExternalSessionID)
	}
	own, foreign := sessionHeader(a.ID), sessionHeader(other.ID)
	var out struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	for _, c := range []struct {
		path, msg string
		body      map[string]any
	}{
		{fmt.Sprintf("/api/tasks/%d/requests/q1", a.ID), "answer its own session's request", map[string]any{"option": "allow"}},
		{fmt.Sprintf("/api/tasks/%d/messages", a.ID), "message its own session", map[string]any{"text": "hello"}},
	} {
		direct := map[string]string{buildinfo.APIVersionHeader: apiVersion, agentctx.CallerHeader: own}
		if rec := raw(t, e.h, "POST", c.path, direct, c.body, &out); rec.Code != 403 || out.Code != "self_action_forbidden" || !strings.Contains(out.Error, c.msg) {
			t.Fatalf("own session %s: %d %s", c.path, rec.Code, rec.Body)
		}
		gateway := workerHeaders(j.Token)
		gateway[agentctx.CallerHeader] = own
		if rec := raw(t, e.h, "POST", api.GatewayPrefix+c.path, gateway, c.body, &out); rec.Code != 403 || out.Code != "self_action_forbidden" {
			t.Fatalf("gateway own session %s: %d %s", c.path, rec.Code, rec.Body)
		}
		gateway[agentctx.CallerHeader] = foreign
		if rec := raw(t, e.h, "POST", api.GatewayPrefix+c.path, gateway, c.body, &out); rec.Code != 409 || out.Code != "operation_unavailable" {
			t.Fatalf("gateway other session %s: %d %s", c.path, rec.Code, rec.Body)
		}
		if rec := e.gw(j.Token, "POST", c.path, c.body, &out); rec.Code != 409 || out.Code != "operation_unavailable" {
			t.Fatalf("gateway without caller %s: %d %s", c.path, rec.Code, rec.Body)
		}
	}
	if rec := e.gw(j.Token, "GET", fmt.Sprintf("/api/tasks/%d/requests", a.ID), nil, &out); rec.Code != 409 || out.Code != "operation_unavailable" {
		t.Fatalf("gateway listing: %d %s", rec.Code, rec.Body)
	}
}
