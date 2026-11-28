package api_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// needsInput moves task id to needs_input (as an observation would) and
// returns its state revision.
func (e *env) needsInput(id int64) int64 {
	e.t.Helper()
	if _, err := e.store.TransitionTask(context.Background(), db.Transition{TaskID: id, To: db.StateNeedsInput}); err != nil {
		e.t.Fatal(err)
	}
	got, err := e.store.GetTask(context.Background(), id)
	if err != nil || got.State != db.StateNeedsInput {
		e.t.Fatalf("task %d: %+v %v", id, got, err)
	}
	return got.StateRevision
}

// lastEvent is task id's latest journal event of type event.
func (e *env) lastEvent(id int64, event string) db.JournalEntry {
	e.t.Helper()
	page, err := e.store.ReadJournal(context.Background(), db.JournalOwner{TaskID: &id}, db.JournalQuery{Kind: "events", Limit: 100})
	if err != nil {
		e.t.Fatal(err)
	}
	var last *db.JournalEntry
	for i, en := range page.Entries {
		if en.Kind == "event" && en.EventType == event && (last == nil || en.ID > last.ID) {
			last = &page.Entries[i]
		}
	}
	if last == nil {
		e.t.Fatalf("task %d: no %s event in %+v", id, event, page.Entries)
	}
	return *last
}

// Two guarded sends racing on one revision: the coordinator's per-task
// lock lets exactly one reach the harness; the other is 409
// already_answered with the task's state and revision. Unguarded sends
// keep working.
func TestGuardedSendFirstAnswerWins(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/first-answer")
	a := e.task(p.ID, "asks", map[string]any{"start": true})
	if a.StateRevision < 2 {
		t.Fatalf("started task revision %d", a.StateRevision)
	}
	rev := e.needsInput(a.ID)
	e.endT3Turn(a.ExternalSessionID)
	messages := fmt.Sprintf("/api/tasks/%d/messages", a.ID)
	body := map[string]any{"text": "go", "expectedStateRevision": rev}

	codes := make([]int, 2)
	bodies := make([]map[string]any, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range codes {
		wg.Go(func() {
			<-start
			var out map[string]any
			rec := raw(t, e.h, "POST", messages, map[string]string{buildinfo.APIVersionHeader: apiVersion}, body, &out)
			codes[i], bodies[i] = rec.Code, out
		})
	}
	close(start)
	wg.Wait()
	wins, lost := 0, -1
	for i, c := range codes {
		switch c {
		case 200:
			wins++
		case 409:
			lost = i
		}
	}
	if wins != 1 || lost < 0 {
		t.Fatalf("codes %v, bodies %v", codes, bodies)
	}
	if b := bodies[lost]; b["code"] != "already_answered" || b["state"] == nil || b["stateRevision"] == nil {
		t.Fatalf("loser body: %v", b)
	}
	if len(e.fake.Sent) != 1 {
		t.Fatalf("harness got %d messages: %+v", len(e.fake.Sent), e.fake.Sent)
	}
	got, err := e.store.GetTask(context.Background(), a.ID)
	if err != nil || got.AnsweredRevision == nil || *got.AnsweredRevision != rev {
		t.Fatalf("answered revision: %+v %v", got.AnsweredRevision, err)
	}

	// Stale revision, and a task not waiting: refused before the harness.
	if code, b := e.raw("POST", messages, fmt.Sprintf(`{"text":"stale","expectedStateRevision":%d}`, rev-1)); code != 409 || b["code"] != "already_answered" {
		t.Fatalf("stale: %d %v", code, b)
	}
	if len(e.fake.Sent) != 1 {
		t.Fatalf("refused message reached the harness: %+v", e.fake.Sent)
	}
	// Without the precondition a follow-up goes as before.
	e.endT3Turn(a.ExternalSessionID)
	e.must("POST", messages, map[string]string{"text": "unguarded"}, 200, nil)
	if len(e.fake.Sent) != 2 {
		t.Fatalf("unguarded follow-up: %+v", e.fake.Sent)
	}
	// A new wait is a new revision: answerable once more.
	if _, err := e.store.TransitionTask(context.Background(), db.Transition{TaskID: a.ID, To: db.StateRunning}); err != nil {
		t.Fatal(err)
	}
	rev = e.needsInput(a.ID)
	if rev <= *got.AnsweredRevision {
		t.Fatalf("new wait revision %d, answered %d", rev, *got.AnsweredRevision)
	}
	e.endT3Turn(a.ExternalSessionID)
	e.must("POST", messages, map[string]any{"text": "again", "expectedStateRevision": rev}, 200, nil)
}

// A guarded respond: stale and repeated answers are 409 already_answered;
// the winning answer records the revision.
func TestGuardedRespond(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/first-answer-respond")
	a := e.task(p.ID, "asks", map[string]any{"start": true})
	thread := *a.ExternalSessionID
	rid, err := e.fake.AddUserInput(thread, []t3.Question{{ID: "q", Question: "Ship?", Options: []t3.QuestionOption{{Label: "Yes"}, {Label: "No"}}}})
	if err != nil {
		t.Fatal(err)
	}
	rev := e.needsInput(a.ID)
	path := fmt.Sprintf("/api/tasks/%d/requests/%s", a.ID, rid)
	answer := func(expected int64) (int, map[string]any) {
		return e.raw("POST", path, fmt.Sprintf(`{"answers":{"q":["Yes"]},"expectedStateRevision":%d}`, expected))
	}
	if code, b := answer(rev + 1); code != 409 || b["code"] != "already_answered" || b["state"] != string(db.StateNeedsInput) || b["stateRevision"] != float64(rev) {
		t.Fatalf("future revision: %d %v", code, b)
	}
	if len(e.fake.Responses) != 0 {
		t.Fatalf("refused answer reached the harness: %+v", e.fake.Responses)
	}
	if code, b := answer(rev); code != 200 {
		t.Fatalf("answer: %d %v", code, b)
	}
	if len(e.fake.Responses) != 1 {
		t.Fatalf("responses: %+v", e.fake.Responses)
	}
	if code, b := answer(rev); code != 409 || b["code"] != "already_answered" {
		t.Fatalf("answered twice: %d %v", code, b)
	}
	if len(e.fake.Responses) != 1 {
		t.Fatalf("second answer reached the harness: %+v", e.fake.Responses)
	}
}

// Send and respond record the caller session as the cause and keep the
// target task in Cause.TaskID; without a caller only the target is named.
func TestAnswerRecordsCallerCause(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/first-answer-cause")
	a := e.task(p.ID, "target", map[string]any{"start": true})
	steerer := e.task(p.ID, "steerer", map[string]any{"start": true})
	caller := db.SessionRef{ConnectionID: e.store.ActiveID(db.LocalMachine, "t3"), ExternalSessionID: *steerer.ExternalSessionID}
	headers := map[string]string{buildinfo.APIVersionHeader: apiVersion, agentctx.CallerHeader: fmt.Sprintf("%d:%s", caller.ConnectionID, caller.ExternalSessionID)}
	rev := e.needsInput(a.ID)
	e.endT3Turn(a.ExternalSessionID)
	if rec := raw(t, e.h, "POST", fmt.Sprintf("/api/tasks/%d/messages", a.ID), headers, map[string]any{"text": "go", "expectedStateRevision": rev}, nil); rec.Code != 200 {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	cause := e.lastEvent(a.ID, "task_event").Cause
	if cause == nil || cause.Kind != "session_message" || cause.TaskID != a.ID || cause.Session == nil || *cause.Session != caller {
		t.Fatalf("send cause: %+v", cause)
	}

	rid, err := e.fake.AddUserInput(*a.ExternalSessionID, []t3.Question{{ID: "q", Question: "Ship?", Options: []t3.QuestionOption{{Label: "Yes"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if rec := raw(t, e.h, "POST", fmt.Sprintf("/api/tasks/%d/requests/%s", a.ID, rid), headers, map[string]any{"answers": map[string][]string{"q": {"Yes"}}}, nil); rec.Code != 200 {
		t.Fatalf("respond: %d %s", rec.Code, rec.Body)
	}
	cause = e.lastEvent(a.ID, "session_response").Cause
	if cause == nil || cause.Kind != "session_request" || cause.TaskID != a.ID || cause.RequestID != rid || cause.Session == nil || *cause.Session != caller {
		t.Fatalf("respond cause: %+v", cause)
	}

	rid, err = e.fake.AddUserInput(*a.ExternalSessionID, []t3.Question{{ID: "q", Question: "Again?", Options: []t3.QuestionOption{{Label: "Yes"}}}})
	if err != nil {
		t.Fatal(err)
	}
	e.must("POST", fmt.Sprintf("/api/tasks/%d/requests/%s", a.ID, rid), map[string]any{"answers": map[string][]string{"q": {"Yes"}}}, 200, nil)
	if cause = e.lastEvent(a.ID, "session_response").Cause; cause == nil || cause.Session != nil || cause.TaskID != a.ID {
		t.Fatalf("respond cause without caller: %+v", cause)
	}
}
