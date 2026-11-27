package api_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// T3 answers its threads' questions and takes follow-ups through the
// generic routes: questions map to question requests (option labels in,
// T3 option values out), a request no longer pending is 404, an answer
// that doesn't fit is 400, and a thread waiting on any request (a
// permission request T3 can't relay included) refuses a message (409
// session_busy) until it is answered.
func TestT3Interaction(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/interaction")
	backlog := e.task(p.ID, "not started", nil)
	if code, body := e.raw("GET", fmt.Sprintf("/api/tasks/%d/requests", backlog.ID), ""); code != 409 || body["code"] == "operation_unavailable" {
		t.Fatalf("no session: %d %v", code, body)
	}
	a := e.task(p.ID, "running", map[string]any{"start": true})
	if a.ExternalSessionID == nil {
		t.Fatal("no thread")
	}
	thread := *a.ExternalSessionID
	var caps execution.Capabilities
	e.must("GET", fmt.Sprintf("/api/connections/%d/capabilities", p.Bindings[0].ConnectionID), nil, 200, &caps)
	if !caps.Operations[execution.OpRespond].Available || !caps.Operations[execution.OpSend].Available {
		t.Fatalf("operations: %+v", caps.Operations)
	}
	var harnesses struct {
		Harnesses []struct {
			Kind             string `json:"kind"`
			UnlistedRequests string `json:"unlistedRequests"`
		} `json:"harnesses"`
	}
	e.must("GET", "/api/harnesses", nil, 200, &harnesses)
	for _, h := range harnesses.Harnesses {
		if h.Kind == t3.Kind && h.UnlistedRequests != "Approve permission requests in T3." {
			t.Fatalf("T3 unlisted requests: %q", h.UnlistedRequests)
		}
	}

	incremental, no := "incremental", false
	rid, err := e.fake.AddUserInput(thread, []t3.Question{
		{ID: "approach", Header: "Approach", Question: "Which rollout?", Options: []t3.QuestionOption{
			{Label: "Big bang", Description: "One change"},
			{Label: "Incremental", Value: &incremental, Preview: "flag(\"v2\")"},
		}},
		{ID: "checks", Question: "Which checks?", MultiSelect: true, AllowCustomAnswer: &no, Options: []t3.QuestionOption{{Label: "Unit"}, {Label: "E2E"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Requests []execution.Request `json:"requests"`
	}
	e.must("GET", fmt.Sprintf("/api/tasks/%d/requests", a.ID), nil, 200, &list)
	want := []execution.Question{
		{ID: "approach", Header: "Approach", Prompt: "Which rollout?", Custom: true, Options: []execution.QuestionOption{
			{Label: "Big bang", Description: "One change"}, {Label: "Incremental", Preview: "flag(\"v2\")"},
		}},
		{ID: "checks", Prompt: "Which checks?", Multi: true, Options: []execution.QuestionOption{{Label: "Unit"}, {Label: "E2E"}}},
	}
	if len(list.Requests) != 1 || list.Requests[0].ID != rid || list.Requests[0].Kind != execution.RequestQuestion ||
		list.Requests[0].Title != "T3 asks 2 questions" || !reflect.DeepEqual(list.Requests[0].Questions, want) {
		t.Fatalf("requests: %+v", list.Requests)
	}

	path := fmt.Sprintf("/api/tasks/%d/requests/%s", a.ID, rid)
	messages := fmt.Sprintf("/api/tasks/%d/messages", a.ID)
	if code, body := e.raw("POST", messages, `{"text":"hello"}`); code != 409 || body["code"] != "session_busy" || !strings.Contains(fmt.Sprint(body["error"]), "Answer them first") {
		t.Fatalf("send while asking: %d %v", code, body)
	}
	for _, bad := range []string{
		`{"option":"Incremental"}`,
		`{"answers":{"approach":["Incremental"]}}`,
		`{"answers":{"approach":["Incremental"],"checks":["Lint"]}}`,
		`{"answers":{"approach":["Big bang","Incremental"],"checks":["Unit"]}}`,
		`{"answers":{"approach":["Incremental"],"checks":["Unit"],"other":["x"]}}`,
	} {
		if code, body := e.raw("POST", path, bad); code != 400 {
			t.Errorf("answer %s: %d %v", bad, code, body)
		}
	}
	if len(e.fake.Responses) != 0 {
		t.Fatalf("an invalid answer reached T3: %+v", e.fake.Responses)
	}
	e.must("POST", path, map[string]any{"answers": map[string][]string{"approach": {"Incremental"}, "checks": {"Unit", "E2E"}}}, 200, nil)
	if len(e.fake.Responses) != 1 || !reflect.DeepEqual(e.fake.Responses[0].Answers, map[string]t3.Answer{
		"approach": t3.TextAnswer("incremental"), "checks": t3.ValuesAnswer([]string{"Unit", "E2E"}),
	}) {
		t.Fatalf("answers sent: %+v", e.fake.Responses)
	}
	if code, body := e.raw("POST", path, `{"answers":{"approach":["Big bang"],"checks":["Unit"]}}`); code != 404 {
		t.Fatalf("answered twice: %d %v", code, body)
	}

	// A permission request: not listed, answered in T3, and it holds messages.
	aid, err := e.fake.AddApproval(thread)
	if err != nil {
		t.Fatal(err)
	}
	e.must("GET", fmt.Sprintf("/api/tasks/%d/requests", a.ID), nil, 200, &list)
	if len(list.Requests) != 0 {
		t.Fatalf("approval listed: %+v", list.Requests)
	}
	if code, body := e.raw("POST", messages, `{"text":"hello"}`); code != 409 || body["code"] != "session_busy" || !strings.Contains(fmt.Sprint(body["error"]), "Approve it in T3") {
		t.Fatalf("send while approving: %d %v", code, body)
	}
	if err := e.fake.ResolveRequest(thread, aid, "resolved"); err != nil {
		t.Fatal(err)
	}
	e.must("POST", messages, map[string]string{"text": "hello"}, 200, nil)
	if len(e.fake.Sent) != 1 || e.fake.Sent[0] != (t3.FakeSend{ThreadID: thread, Message: "hello", Mode: t3.SendAuto}) {
		t.Fatalf("sent: %+v", e.fake.Sent)
	}
	if err := e.fake.SetThread(thread, func(d *t3.ThreadDetail) { d.Archived = true }); err != nil {
		t.Fatal(err)
	}
	if code, body := e.raw("POST", messages, `{"text":"again"}`); code != 409 || body["code"] != "session_busy" || !strings.Contains(fmt.Sprint(body["error"]), "archived") {
		t.Fatalf("send to archived: %d %v", code, body)
	}

	for _, c := range []struct{ path, body string }{
		{messages, `{"text":" "}`},
		{messages, `{"text":"x","extra":1}`},
		{path, `{}`},
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
// caller reaches the harness (T3: the request is not pending, the message
// is sent).
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
		reached   int
	}{
		{fmt.Sprintf("/api/tasks/%d/requests/q1", a.ID), "answer its own session's request", map[string]any{"option": "allow"}, 404},
		{fmt.Sprintf("/api/tasks/%d/messages", a.ID), "message its own session", map[string]any{"text": "hello"}, 200},
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
		if rec := raw(t, e.h, "POST", api.GatewayPrefix+c.path, gateway, c.body, nil); rec.Code != c.reached {
			t.Fatalf("gateway other session %s: %d %s", c.path, rec.Code, rec.Body)
		}
		if rec := e.gw(j.Token, "POST", c.path, c.body, nil); rec.Code != c.reached {
			t.Fatalf("gateway without caller %s: %d %s", c.path, rec.Code, rec.Body)
		}
	}
	if len(e.fake.Sent) != 2 {
		t.Fatalf("messages from other callers: %+v", e.fake.Sent)
	}
	if rec := e.gw(j.Token, "GET", fmt.Sprintf("/api/tasks/%d/requests", a.ID), nil, nil); rec.Code != 200 {
		t.Fatalf("gateway listing: %d %s", rec.Code, rec.Body)
	}
}
