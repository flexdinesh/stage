package api_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// Settle in T3 is relayed, never automatic; the closure the poller then
// observes completes the task through the existing inference.
func TestSettleInT3ActionFlowsThroughInferredCompletion(t *testing.T) {
	e := newEnv(t)
	e.autoComplete(1, true)
	p := e.project("/w/settle-action")
	a := e.task(p.ID, "source", map[string]any{"start": true})
	thread := *a.ExternalSessionID
	path := taskPath(a.ID, "/actions")
	reason := func(id string) string {
		t.Helper()
		var actions []execution.Action
		e.must("GET", path, nil, http.StatusOK, &actions)
		i := slices.IndexFunc(actions, func(a execution.Action) bool { return a.ID == id })
		if i < 0 {
			t.Fatalf("%s not declared: %+v", id, actions)
		}
		return actions[i].Availability.Reason
	}

	e.poll()
	if got := reason("settle"); got != "Still running in T3. Stop it before settling." {
		t.Fatalf("running settle reason %q", got)
	}
	if msg := e.refused("POST", path+"/settle", json.RawMessage(`{}`), http.StatusConflict); msg != "Still running in T3. Stop it before settling." {
		t.Fatalf("settle while running: %q", msg)
	}
	_ = e.fake.SetStatus(thread, t3.StatusCompleted)
	e.poll()
	if got := reason("settle"); got != "" {
		t.Fatalf("finished settle reason %q", got)
	}
	var result execution.ActionResult
	e.must("POST", path+"/settle", json.RawMessage(`{}`), http.StatusOK, &result)
	if result.Message != "Settled in T3." || !slices.Equal(e.fake.Organized, []string{thread + ":settle"}) {
		t.Fatalf("settle %+v, organized %v", result, e.fake.Organized)
	}
	if len(e.acceptances(a.ID)) != 0 {
		t.Fatal("the action completed the task itself")
	}
	e.poll()
	if got := e.acceptances(a.ID); len(got) != 1 || got[0].Provenance != "harness_settled" {
		t.Fatalf("inferred %+v", got)
	}
	if got := reason("settle"); got != "Already settled in T3." {
		t.Fatalf("settled reason %q", got)
	}
	if got := reason("archive"); got != "" {
		t.Fatalf("archive of a settled thread: %q", got)
	}
	e.must("POST", path+"/archive", json.RawMessage(`{}`), http.StatusOK, &result)
	e.poll()
	if got := reason("archive"); got != "Already archived in T3." {
		t.Fatalf("archived reason %q", got)
	}
	e.must("POST", path+"/archive", json.RawMessage(`{}`), http.StatusConflict, nil)
	if !slices.Equal(e.fake.Organized, []string{thread + ":settle", thread + ":archive"}) {
		t.Fatalf("organized %v", e.fake.Organized)
	}
	events := 0
	for _, entry := range e.journal(taskPath(a.ID, "/journal?kind=events")).Entries {
		if entry.EventType == "session_action" {
			events++
		}
	}
	if events != 2 {
		t.Fatalf("session_action events = %d", events)
	}
}
