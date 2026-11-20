package api_test

import (
	"fmt"
	"testing"
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
	for _, c := range []struct{ method, path, body string }{
		{"GET", fmt.Sprintf("/api/tasks/%d/requests", a.ID), ""},
		{"POST", fmt.Sprintf("/api/tasks/%d/requests/q1", a.ID), `{"option":"yes"}`},
		{"POST", fmt.Sprintf("/api/tasks/%d/messages", a.ID), `{"text":"hello"}`},
	} {
		if code, body := e.raw(c.method, c.path, c.body); code != 409 || body["code"] != "operation_unavailable" {
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
