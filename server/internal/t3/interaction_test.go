package t3

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// respondFails is the fake whose respond fails with err.
type respondFails struct {
	*Fake
	err error
}

func (c respondFails) RespondPendingQuestion(context.Context, string, string, map[string]Answer) error {
	return c.err
}

func interactionThread(t *testing.T, f *Fake) execution.SessionRef {
	t.Helper()
	p := f.AddProject("p", "/p")
	r, err := f.LaunchThread(t.Context(), LaunchInput{ProjectID: p.ID, Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	return execution.SessionRef{ConnectionID: 1, ExternalSessionID: r.ThreadID}
}

func TestRequestsMapQuestions(t *testing.T) {
	f := NewFake()
	a := NewAdapter(f)
	ref := interactionThread(t, f)
	if rs, err := a.Requests(t.Context(), ref); err != nil || len(rs) != 0 {
		t.Fatal("no requests", rs, err)
	}
	no, value := false, "v"
	id, _ := f.AddUserInput(ref.ExternalSessionID, []Question{{ID: "q", Header: "H", Question: "Pick?", AllowCustomAnswer: &no,
		Options: []QuestionOption{{Label: "A", Description: "first", Value: &value, Preview: "a := 1"}}}})
	_, _ = f.AddApproval(ref.ExternalSessionID)
	stale, _ := f.AddUserInput(ref.ExternalSessionID, []Question{{ID: "x", Question: "Gone?"}})
	_ = f.ResolveRequest(ref.ExternalSessionID, stale, "expired")
	rs, err := a.Requests(t.Context(), ref)
	want := []execution.Request{{ID: id, Kind: execution.RequestQuestion, Title: "T3 asks", Questions: []execution.Question{
		{ID: "q", Header: "H", Prompt: "Pick?", Options: []execution.QuestionOption{{Label: "A", Description: "first", Preview: "a := 1"}}},
	}}}
	if err != nil || len(rs) != 1 || rs[0].CreatedAt.IsZero() {
		t.Fatalf("requests: %+v %v", rs, err)
	}
	rs[0].CreatedAt = time.Time{}
	if !reflect.DeepEqual(rs, want) {
		t.Fatalf("requests: %+v", rs)
	}
	if _, err := a.Requests(t.Context(), execution.SessionRef{ConnectionID: 1, ExternalSessionID: "missing"}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatal("unknown thread", err)
	}
	if _, err := a.Requests(t.Context(), execution.SessionRef{ConnectionID: 2, ExternalSessionID: ref.ExternalSessionID}); !errors.Is(err, execution.ErrUnavailable) {
		t.Fatal("other connection", err)
	}
}

func TestToAnswers(t *testing.T) {
	no, value := false, "v"
	qs := []Question{
		{ID: "one", Question: "One?", Options: []QuestionOption{{Label: " Valued ", Value: &value}, {Label: "Plain"}}},
		{ID: "many", Question: "Many?", MultiSelect: true, Options: []QuestionOption{{Label: "X"}, {Label: "Y", Value: &value}}},
		{ID: "strict", Question: "Strict?", AllowCustomAnswer: &no, Options: []QuestionOption{{Label: "Yes"}}},
	}
	for _, tc := range []struct {
		name string
		in   map[string][]string
		want map[string]Answer
	}{
		{"labels map to values", map[string][]string{"one": {"Valued"}, "many": {"X", "Y"}, "strict": {"Yes"}},
			map[string]Answer{"one": TextAnswer("v"), "many": ValuesAnswer([]string{"X", "v"}), "strict": TextAnswer("Yes")}},
		{"custom text", map[string][]string{"one": {"my own"}, "many": {"other"}, "strict": {"Yes"}},
			map[string]Answer{"one": TextAnswer("my own"), "many": TextAnswer("other"), "strict": TextAnswer("Yes")}},
		{"custom where not allowed", map[string][]string{"one": {"Plain"}, "many": {"X"}, "strict": {"No"}}, nil},
		{"several on a single question", map[string][]string{"one": {"Plain", "Valued"}, "many": {"X"}, "strict": {"Yes"}}, nil},
		{"custom among options", map[string][]string{"one": {"Plain"}, "many": {"X", "zzz"}, "strict": {"Yes"}}, nil},
		{"unanswered", map[string][]string{"one": {"Plain"}, "strict": {"Yes"}}, nil},
		{"unknown question", map[string][]string{"one": {"Plain"}, "many": {"X"}, "strict": {"Yes"}, "who": {"x"}}, nil},
	} {
		got, err := toAnswers(qs, tc.in)
		if tc.want == nil {
			if err == nil {
				t.Errorf("%s: accepted %+v", tc.name, got)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %+v %v", tc.name, got, err)
		}
	}
}

func TestRespondOutcomes(t *testing.T) {
	f := NewFake()
	a := NewAdapter(f)
	ref := interactionThread(t, f)
	qs := []Question{{ID: "q", Question: "Pick?", Options: []QuestionOption{{Label: "A"}}}}
	id, _ := f.AddUserInput(ref.ExternalSessionID, qs)
	var invalid *execution.Invalid
	if err := a.Respond(t.Context(), ref, id, execution.Response{Option: "A"}); !errors.As(err, &invalid) {
		t.Fatal("option on a question", err)
	}
	if err := a.Respond(t.Context(), ref, id, execution.Response{Answers: map[string][]string{"q": {"A"}}}); err != nil {
		t.Fatal(err)
	}
	if len(f.Responses) != 1 || !reflect.DeepEqual(f.Responses[0].Answers, map[string]Answer{"q": TextAnswer("A")}) {
		t.Fatalf("responses: %+v", f.Responses)
	}
	if err := a.Respond(t.Context(), ref, id, execution.Response{Answers: map[string][]string{"q": {"A"}}}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatal("answered", err)
	}
	expired, _ := f.AddUserInput(ref.ExternalSessionID, qs)
	_ = f.ResolveRequest(ref.ExternalSessionID, expired, "expired")
	if err := a.Respond(t.Context(), ref, expired, execution.Response{Answers: map[string][]string{"q": {"A"}}}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatal("expired", err)
	}
	if err := a.Respond(t.Context(), ref, "unknown", execution.Response{Answers: map[string][]string{"q": {"A"}}}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatal("unknown", err)
	}
	// T3 refusing an answer is invalid; a race to answer it is not pending.
	pending, _ := f.AddUserInput(ref.ExternalSessionID, qs)
	refused := NewAdapter(respondFails{f, &ToolError{Tool: "t3_pending_request", Code: "invalid_request", Message: "Answer q is not an option."}})
	if err := refused.Respond(t.Context(), ref, pending, execution.Response{Answers: map[string][]string{"q": {"A"}}}); !errors.As(err, &invalid) || invalid.Reason != "Answer q is not an option." {
		t.Fatal("refused answer", err)
	}
	raced := NewAdapter(respondFails{f, &ToolError{Tool: "t3_pending_request", Code: "invalid_request", Message: "The user-input request was already answered."}})
	if err := raced.Respond(t.Context(), ref, pending, execution.Response{Answers: map[string][]string{"q": {"A"}}}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatal("raced answer", err)
	}
}

// sendFails is the fake whose send fails with err.
type sendFails struct {
	*Fake
	err error
}

func (c sendFails) SendMessage(context.Context, string, string, SendMode, string) error {
	return c.err
}

func TestSendQueuesWithRequestID(t *testing.T) {
	f := NewFake()
	a := NewAdapter(f)
	ref := interactionThread(t, f)
	thread := ref.ExternalSessionID
	// Running: queued behind the current run, never steered.
	if err := a.Send(t.Context(), ref, execution.Message{Text: "next", RequestID: "r1"}); err != nil {
		t.Fatal("running", err)
	}
	if d, _ := f.ReadThread(t.Context(), thread); d.RunCount != 1 || d.Status != StatusRunning {
		t.Fatalf("a queued message started a run: %+v", d)
	}
	// A retry with the same request ID sends once.
	if err := a.Send(t.Context(), ref, execution.Message{Text: "next", RequestID: "r1"}); err != nil {
		t.Fatal("retry", err)
	}
	_ = f.SetStatus(thread, StatusCompleted)
	if d, _ := f.ReadThread(t.Context(), thread); d.RunCount != 2 || d.Status != StatusRunning {
		t.Fatalf("the queued message did not run after the turn: %+v", d)
	}
	_ = f.SetStatus(thread, StatusCompleted)
	// Idle: starts a new turn.
	if err := a.Send(t.Context(), ref, execution.Message{Text: "follow up", RequestID: "r2"}); err != nil {
		t.Fatal("idle", err)
	}
	if d, _ := f.ReadThread(t.Context(), thread); d.RunCount != 3 || d.Status != StatusRunning {
		t.Fatalf("idle send did not start a run: %+v", d)
	}
	want := []FakeSend{{thread, "next", SendQueue, "r1"}, {thread, "follow up", SendQueue, "r2"}}
	if !reflect.DeepEqual(f.Sent, want) {
		t.Fatalf("sent: %+v", f.Sent)
	}
}

func TestSendRefusals(t *testing.T) {
	f := NewFake()
	a := NewAdapter(f)
	ref := interactionThread(t, f)
	thread := ref.ExternalSessionID
	msg := execution.Message{Text: "hi", RequestID: "r"}
	var busy *execution.Busy
	q, _ := f.AddUserInput(thread, []Question{{ID: "q", Question: "?"}})
	if err := a.Send(t.Context(), ref, msg); !errors.As(err, &busy) || busy.Reason != busyQuestions {
		t.Fatal("question pending", err)
	}
	_ = f.ResolveRequest(thread, q, "resolved")
	ap, _ := f.AddApproval(thread)
	if err := a.Send(t.Context(), ref, msg); !errors.As(err, &busy) || busy.Reason != busyApproval {
		t.Fatal("approval pending", err)
	}
	_ = f.ResolveRequest(thread, ap, "resolved")
	// T3 refusing the thread (its worktree can't be restored) is final.
	var refused *execution.Refused
	notSendable := NewAdapter(sendFails{f, &ToolError{Tool: "t3_thread_send", Code: "thread_not_sendable", Message: "Worktree is gone."}})
	if err := notSendable.Send(t.Context(), ref, msg); !errors.As(err, &refused) || refused.Reason != "Worktree is gone." {
		t.Fatal("not sendable", err)
	}
	_ = f.SetThread(thread, func(d *ThreadDetail) { d.Archived = true })
	if err := a.Send(t.Context(), ref, msg); !errors.As(err, &refused) || refused.Reason != refusedArchived {
		t.Fatal("archived", err)
	}
	if err := a.Send(t.Context(), execution.SessionRef{ConnectionID: 1, ExternalSessionID: "missing"}, msg); !errors.Is(err, execution.ErrNotFound) {
		t.Fatal("unknown thread", err)
	}
	if err := a.Send(t.Context(), execution.SessionRef{ConnectionID: 2, ExternalSessionID: thread}, msg); !errors.Is(err, execution.ErrUnavailable) {
		t.Fatal("other connection", err)
	}
	if len(f.Sent) != 0 {
		t.Fatalf("a refused message was sent: %+v", f.Sent)
	}
}

func TestRequestsCreatedAtFirstSeen(t *testing.T) {
	f := NewFake()
	a := NewAdapter(f)
	ref := interactionThread(t, f)
	thread := ref.ExternalSessionID
	first, _ := f.AddUserInput(thread, []Question{{ID: "q", Question: "?"}})
	rs, err := a.Requests(t.Context(), ref)
	if err != nil || len(rs) != 1 || rs[0].CreatedAt.IsZero() {
		t.Fatalf("first read: %+v %v", rs, err)
	}
	at := rs[0].CreatedAt
	second, _ := f.AddUserInput(thread, []Question{{ID: "q", Question: "?"}})
	rs, err = a.Requests(t.Context(), ref)
	if err != nil || len(rs) != 2 || !rs[0].CreatedAt.Equal(at) || rs[1].ID != second || rs[1].CreatedAt.Before(at) {
		t.Fatalf("second read keeps the first time: %+v %v", rs, err)
	}
	_ = f.ResolveRequest(thread, first, "resolved")
	_, _ = a.Requests(t.Context(), ref)
	if _, kept := a.asked.at[thread][first]; kept {
		t.Fatal("an answered request is still remembered")
	}
	_ = f.ResolveRequest(thread, second, "resolved")
	_, _ = a.Requests(t.Context(), ref)
	if _, kept := a.asked.at[thread]; kept {
		t.Fatal("a thread with no requests is still remembered")
	}
}

func TestRespondSendsOptionValues(t *testing.T) {
	f := NewFake()
	a := NewAdapter(f)
	ref := interactionThread(t, f)
	value := "incremental"
	id, _ := f.AddUserInput(ref.ExternalSessionID, []Question{
		{ID: "how", Question: "How?", Options: []QuestionOption{{Label: "Big bang"}, {Label: "Incremental", Value: &value}}},
		{ID: "checks", Question: "Checks?", MultiSelect: true, Options: []QuestionOption{{Label: "Unit"}, {Label: "E2E", Value: &value}}},
	})
	answers := map[string][]string{"how": {"Incremental"}, "checks": {"Unit", "E2E"}}
	if err := a.Respond(t.Context(), ref, id, execution.Response{Answers: answers}); err != nil {
		t.Fatal(err)
	}
	want := map[string]Answer{"how": TextAnswer("incremental"), "checks": ValuesAnswer([]string{"Unit", "incremental"})}
	if len(f.Responses) != 1 || !reflect.DeepEqual(f.Responses[0].Answers, want) {
		t.Fatalf("responses: %+v", f.Responses)
	}
}

func TestFakeRespondValidates(t *testing.T) {
	f := NewFake()
	ref := interactionThread(t, f)
	no := false
	id, _ := f.AddUserInput(ref.ExternalSessionID, []Question{{ID: "q", Question: "?", AllowCustomAnswer: &no, Options: []QuestionOption{{Label: "A"}}}})
	var te *ToolError
	if err := f.RespondPendingQuestion(t.Context(), ref.ExternalSessionID, id, map[string]Answer{"q": TextAnswer("B")}); !errors.As(err, &te) || te.Code != "invalid_request" {
		t.Fatal("custom answer where not allowed", err)
	}
	if len(f.Responses) != 0 {
		t.Fatalf("an invalid answer was recorded: %+v", f.Responses)
	}
}

func TestFeaturesOfferRespondAndSend(t *testing.T) {
	c, err := NewAdapter(NewFake()).Features(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []execution.Op{execution.OpRespond, execution.OpSend} {
		if !c.Operations[op].Available {
			t.Errorf("%s: %+v", op, c.Operations[op])
		}
	}
}
