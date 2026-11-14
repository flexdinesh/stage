package api_test

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/flexdinesh/stage/server/internal/t3"
)

func strp(s string) *string { return &s }

func questions() []t3.Question {
	no := false
	return []t3.Question{
		{ID: "scope", Header: "Scope", Question: "Which scope?", Options: []t3.QuestionOption{
			{Label: "Server", Description: "Go only"}, {Label: "Web", Description: "UI", Value: strp("web")},
		}},
		{ID: "extras", Header: "Extras", Question: "Extras?", MultiSelect: true, Options: []t3.QuestionOption{
			{Label: "Docs"}, {Label: "Tests"},
		}},
		{ID: "strict", Header: "Strict", Question: "Yes or no?", AllowCustomAnswer: &no, Options: []t3.QuestionOption{
			{Label: "Yes"}, {Label: "No"},
		}},
	}
}

type pendingJSON struct {
	PendingRequestCount int `json:"pendingRequestCount"`
	ApprovalCount       int `json:"approvalCount"`
	Requests            []struct {
		RequestID string        `json:"requestId"`
		Questions []t3.Question `json:"questions"`
	} `json:"requests"`
}

type errJSON struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

type countsJSON struct {
	State                string `json:"state"`
	PendingRequestCount  int    `json:"pendingRequestCount"`
	PendingQuestionCount int    `json:"pendingQuestionCount"`
}

func TestPendingQuestionsAnswerFlow(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/q")
	tk := e.task(p.ID, "Q", nil)
	var started task
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", tk.ID), nil, 200, &started)
	th := *started.T3ThreadID
	if _, err := e.fake.AddApproval(th); err != nil {
		t.Fatal(err)
	}
	req, _ := e.fake.AddUserInput(th, questions())
	e.poll()

	var tc countsJSON
	e.must("GET", fmt.Sprintf("/api/tasks/%d", tk.ID), nil, 200, &tc)
	if tc.State != "needs_input" || tc.PendingQuestionCount != 1 {
		t.Fatalf("task = %+v", tc)
	}
	var detail struct {
		Thread countsJSON `json:"thread"`
	}
	e.must("GET", "/api/threads/"+url.PathEscape(th), nil, 200, &detail)
	if detail.Thread.PendingRequestCount != 2 || detail.Thread.PendingQuestionCount != 1 {
		t.Fatalf("thread = %+v", detail.Thread)
	}

	var pending pendingJSON
	e.must("GET", "/api/threads/"+th+"/questions", nil, 200, &pending)
	if pending.PendingRequestCount != 2 || pending.ApprovalCount != 1 || len(pending.Requests) != 1 ||
		pending.Requests[0].RequestID != req || len(pending.Requests[0].Questions) != 3 || pending.Requests[0].Questions[1].MultiSelect != true {
		t.Fatalf("pending = %+v", pending)
	}

	path := "/api/threads/" + th + "/questions/" + req + "/answer"
	good := map[string]any{"scope": " Server ", "extras": []string{"Tests"}, "strict": "No"}
	for name, body := range map[string]map[string]any{
		"empty":           {},
		"missing":         {"scope": "Server", "extras": []string{"Docs"}},
		"unknown option":  {"scope": "Server", "extras": []string{"Nope"}, "strict": "No"},
		"custom disabled": {"scope": "Server", "extras": []string{"Docs"}, "strict": "Maybe"},
		"array on single": {"scope": []string{"Server"}, "extras": []string{"Docs"}, "strict": "No"},
	} {
		if code := e.do("POST", path, map[string]any{"answers": body}, nil); code != 400 {
			t.Errorf("%s: status %d", name, code)
		}
	}
	if len(e.fake.Responses) != 0 {
		t.Fatal("invalid answers reached T3")
	}

	var res struct {
		RequestID   string     `json:"requestId"`
		RespondedAt string     `json:"respondedAt"`
		Thread      countsJSON `json:"thread"`
	}
	e.must("POST", path, map[string]any{"answers": good}, 200, &res)
	if res.RequestID != req || res.RespondedAt == "" || res.Thread.PendingRequestCount != 1 || res.Thread.PendingQuestionCount != 0 {
		t.Fatalf("answer = %+v", res)
	}
	if len(e.fake.Responses) != 1 {
		t.Fatalf("responses = %d", len(e.fake.Responses))
	}
	sent := e.fake.Responses[0].Answers
	if sent["scope"].Text != "Server" || len(sent["extras"].Values) != 1 || sent["extras"].Values[0] != "Tests" {
		t.Fatalf("sent = %+v", sent)
	}
	// Approval still pending: still needs input, nothing answerable.
	e.must("GET", fmt.Sprintf("/api/tasks/%d", tk.ID), nil, 200, &tc)
	if tc.State != "needs_input" || tc.PendingQuestionCount != 0 {
		t.Fatalf("task after answer = %+v", tc)
	}

	// Idempotent retry: same answers, no second T3 call.
	var again struct {
		RespondedAt string `json:"respondedAt"`
	}
	e.must("POST", path, map[string]any{"answers": good}, 200, &again)
	if again.RespondedAt != res.RespondedAt || len(e.fake.Responses) != 1 {
		t.Fatalf("retry = %+v, responses %d", again, len(e.fake.Responses))
	}
	var conflict errJSON
	e.must("POST", path, map[string]any{"answers": map[string]any{"scope": "Web", "extras": []string{"Docs"}, "strict": "Yes"}}, 409, &conflict)
	if conflict.Code != "already_answered" {
		t.Fatalf("different answers = %+v", conflict)
	}

	// Answered in T3 meanwhile.
	elsewhere, _ := e.fake.AddUserInput(th, questions()[:1])
	_ = e.fake.ResolveRequest(th, elsewhere, "resolved")
	e.must("POST", "/api/threads/"+th+"/questions/"+elsewhere+"/answer", map[string]any{"answers": map[string]any{"scope": "Server"}}, 409, &conflict)
	if conflict.Code != "already_answered" {
		t.Fatalf("answered in T3 = %+v", conflict)
	}
	// Stale and unknown requests.
	expired, _ := e.fake.AddUserInput(th, questions()[:1])
	_ = e.fake.ResolveRequest(th, expired, "expired")
	for _, id := range []string{expired, "request-nope"} {
		conflict = errJSON{}
		e.must("POST", "/api/threads/"+th+"/questions/"+id+"/answer", map[string]any{"answers": map[string]any{"scope": "Server"}}, 409, &conflict)
		if conflict.Code != "request_stale" {
			t.Fatalf("%s = %+v", id, conflict)
		}
	}
	e.must("GET", "/api/threads/thread-nope/questions", nil, 404, nil)
	e.must("POST", "/api/threads/thread-nope/questions/r/answer", map[string]any{"answers": map[string]any{"scope": "Server"}}, 404, nil)

	// T3 offline: 503 before any T3 call.
	e.t3.offline.Store(true)
	e.must("GET", "/api/threads/"+th+"/questions", nil, 503, nil)
	e.must("POST", path, map[string]any{"answers": good}, 503, nil)
}

func TestAnsweringLastQuestionReleasesTask(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/q2")
	tk := e.task(p.ID, "Q2", nil)
	var started task
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", tk.ID), nil, 200, &started)
	th := *started.T3ThreadID
	req, _ := e.fake.AddUserInput(th, questions()[:1])
	e.poll()
	if got := e.getTask(tk.ID); got.State != "needs_input" {
		t.Fatalf("state %s", got.State)
	}
	e.must("POST", "/api/threads/"+th+"/questions/"+req+"/answer", map[string]any{"answers": map[string]any{"scope": "web"}}, 200, nil)
	if got := e.getTask(tk.ID); got.State != "running" {
		t.Fatalf("state after answer %s", got.State)
	}
	if e.fake.Responses[0].Answers["scope"].Text != "web" {
		t.Fatalf("sent %+v", e.fake.Responses[0].Answers)
	}
}

func TestQuestionCountSurvivesListFailure(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/q3")
	tk := e.task(p.ID, "Q3", nil)
	var started task
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", tk.ID), nil, 200, &started)
	th := *started.T3ThreadID
	_, _ = e.fake.AddUserInput(th, questions()[:1])
	e.poll()
	e.t3.failQuestions.Store(true)
	_, _ = e.fake.AddApproval(th) // thread changes; the count cannot be refreshed
	e.poll()
	var tc countsJSON
	e.must("GET", fmt.Sprintf("/api/tasks/%d", tk.ID), nil, 200, &tc)
	if tc.State != "needs_input" || tc.PendingQuestionCount != 1 {
		t.Fatalf("task = %+v", tc)
	}
	e.must("GET", "/api/threads/"+th+"/questions", nil, 502, nil)
}
