package api_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/t3"
)

type handoffRec struct {
	Attempt        int     `json:"attempt"`
	Status         string  `json:"status"`
	SourceTaskID   *int64  `json:"sourceTaskId"`
	SourceThreadID string  `json:"sourceThreadId"`
	SourceTitle    string  `json:"sourceTitle"`
	Text           string  `json:"text"`
	Reason         *string `json:"reason"`
}

type handoffTask struct {
	task
	Handoff         bool         `json:"handoff"`
	CurrentHandoffs []handoffRec `json:"currentHandoffs"`
}

// one is the current attempt's only handoff record (nil when none).
func (h handoffTask) one(t *testing.T) *handoffRec {
	t.Helper()
	switch len(h.CurrentHandoffs) {
	case 0:
		return nil
	case 1:
		return &h.CurrentHandoffs[0]
	}
	t.Fatalf("want at most one handoff, got %+v", h.CurrentHandoffs)
	return nil
}

func (e *env) handoffTask(id int64) handoffTask {
	e.t.Helper()
	var out handoffTask
	e.must("GET", fmt.Sprintf("/api/tasks/%d", id), nil, 200, &out)
	return out
}

// settle completes and settles thread, then lets sync and the release runner act
// (launch failures are asserted through task state).
func (e *env) acceptThread(thread string) {
	e.t.Helper()
	_ = e.fake.SetStatus(thread, t3.StatusCompleted)
	_ = e.fake.Settle(thread)
	e.poll()
	tasks, err := e.store.ListTasks(context.Background(), db.TaskFilter{Closed: "all"})
	if err != nil {
		e.t.Fatal(err)
	}
	for _, task := range tasks {
		if task.T3ThreadID != nil && *task.T3ThreadID == thread {
			e.complete(task.ID)
		}
	}
	_ = e.runner.Reconcile(context.Background())
}

// lastMessage is the prompt of the newest launch.
func (e *env) lastMessage() string {
	e.t.Helper()
	if len(e.fake.Launched) == 0 {
		e.t.Fatal("no launch")
	}
	return e.fake.Launched[len(e.fake.Launched)-1].Message
}

func (e *env) hasEvent(id int64, prefix string) bool {
	for _, ev := range e.getTask(id).Events {
		if strings.HasPrefix(ev.Detail, prefix) {
			return true
		}
	}
	return false
}

func TestHandoffAppendedForNeed(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/handoff")
	a := e.task(p.ID, "Build API", map[string]any{"start": true})
	b := e.task(p.ID, "Write docs", map[string]any{"startMode": "auto", "needs": []int64{a.ID}})
	if got := e.handoffTask(b.ID); !got.Handoff || got.CurrentHandoffs == nil || len(got.CurrentHandoffs) != 0 {
		t.Fatalf("task with needs: handoff must default on with no record yet: %+v", got)
	}
	thread := *a.T3ThreadID
	_ = e.fake.SetThread(thread, func(d *t3.ThreadDetail) {
		d.Branch, d.WorktreePath = "feat/api", "/w/wt-api"
		d.PullRequests = []t3.PullRequest{{Repository: "me/app", Number: 41, URL: "https://github.com/me/app/pull/41", State: "open"}}
	})
	_ = e.fake.SetReply(thread, t3.Reply{Text: "Shipped the API; see PR 41."})
	e.acceptThread(thread)

	got := e.handoffTask(b.ID)
	h := got.one(t)
	if got.State != "running" || h == nil {
		t.Fatalf("dependent must run with a handoff: %+v", got)
	}
	if h.Status != "sent" || h.Attempt != 1 || h.SourceTaskID == nil || *h.SourceTaskID != a.ID || h.SourceThreadID != thread {
		t.Fatalf("handoff record %+v", h)
	}
	if msg := e.lastMessage(); !strings.HasPrefix(msg, "reply OK\n\n"+h.Text+"\n\n<stage-journal>") {
		t.Fatalf("launch prompt must be prompt + stored block:\n%q\nblock:\n%q", msg, h.Text)
	}
	for _, want := range []string{
		`<handoff from="stage">`, fmt.Sprintf(`This task needs "Build API" (task #%d), completed `, a.ID), "Harness session: " + thread,
		"Branch: feat/api\nWorktree: /w/wt-api",
		"Cached final-reply excerpt:\nShipped the API; see PR 41.", "</handoff>",
	} {
		if !strings.Contains(h.Text, want) {
			t.Fatalf("block missing %q:\n%s", want, h.Text)
		}
	}
	if strings.Contains(h.Text, "[truncated") {
		t.Fatalf("short reply must not be marked truncated:\n%s", h.Text)
	}
	if !e.hasEvent(b.ID, fmt.Sprintf("handoff from #%d appended", a.ID)) {
		t.Fatal("missing handoff event")
	}
}

func TestHandoffCapsReplyAndMarksTruncation(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/cap")
	a := e.task(p.ID, "a", map[string]any{"start": true})
	b := e.task(p.ID, "b", map[string]any{"startMode": "auto", "needs": []int64{a.ID}})
	thread := *a.T3ThreadID
	_ = e.fake.SetReply(thread, t3.Reply{Text: strings.Repeat("x", 5000) + "TAIL"})
	e.acceptThread(thread)
	h := e.handoffTask(b.ID).one(t)
	if h == nil || h.Status != "sent" {
		t.Fatalf("handoff %+v", h)
	}
	if strings.Contains(h.Text, "TAIL") || !strings.Contains(h.Text, strings.Repeat("x", 4000)) ||
		strings.Contains(h.Text, strings.Repeat("x", 4001)) {
		t.Fatal("reply must be capped at 4000 chars")
	}
	if !strings.Contains(h.Text, "[Excerpt truncated;") {
		t.Fatalf("missing truncation marker:\n%s", h.Text[len(h.Text)-200:])
	}
}

func TestHandoffOffStillSendsJournalInstructions(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/off")
	a := e.task(p.ID, "a", map[string]any{"start": true})
	b := e.task(p.ID, "b", map[string]any{"startMode": "auto", "needs": []int64{a.ID}, "handoff": false})
	_ = e.fake.SetReply(*a.T3ThreadID, t3.Reply{Text: "done"})
	e.acceptThread(*a.T3ThreadID)
	got := e.handoffTask(b.ID)
	if got.State != "running" || got.Handoff || len(got.CurrentHandoffs) != 0 {
		t.Fatalf("handoff off: %+v", got)
	}
	if msg := e.lastMessage(); !strings.HasPrefix(msg, "reply OK\n\n<stage-journal>") {
		t.Fatalf("prompt %q", msg)
	}
}

func TestHandoffMissingReplyIsExplicit(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/fail")
	a := e.task(p.ID, "a", map[string]any{"start": true})
	b := e.task(p.ID, "b", map[string]any{"needs": []int64{a.ID}, "startMode": "auto"})
	e.t3.failReply.Store(true)
	e.acceptThread(*a.T3ThreadID)
	got := e.handoffTask(b.ID)
	h := got.one(t)
	if got.State != "running" || h == nil || h.Status != "sent" || !strings.Contains(h.Text, "Final reply: missing at acceptance.") {
		t.Fatalf("missing evidence must be explicit: %+v", got)
	}
}

func TestHandoffFromAdoptedSession(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/session")
	s := runningSession(t, e, p.T3ProjectID, "Planner")
	b := e.task(p.ID, "b", map[string]any{"startMode": "auto", "needsSession": e.t3Session(s)})
	planner := b.Needs[0]
	_ = e.fake.SetReply(s, t3.Reply{Text: "Plan ready."})
	e.acceptThread(s)
	got := e.handoffTask(b.ID)
	h := got.one(t)
	if got.State != "running" || h == nil || h.Status != "sent" || h.SourceTaskID == nil || *h.SourceTaskID != planner ||
		h.SourceThreadID != s || h.SourceTitle != "Planner" {
		t.Fatalf("session handoff %+v / %+v", got, h)
	}
	if !strings.Contains(h.Text, fmt.Sprintf(`"Planner" (task #%d)`, planner)) || !strings.Contains(h.Text, "Plan ready.") {
		t.Fatalf("block:\n%s", h.Text)
	}
	if !e.hasEvent(b.ID, fmt.Sprintf("handoff from #%d appended", planner)) {
		t.Fatal("missing event")
	}
}

// Fan-in: one block per finished need, by id, each capped at replyCap(n).
func TestHandoffFanInPerNeed(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/fanin")
	a := e.task(p.ID, "a", map[string]any{"start": true})
	b := e.task(p.ID, "b", map[string]any{"start": true})
	c := e.task(p.ID, "c", map[string]any{"start": true})
	d := e.task(p.ID, "d", map[string]any{"startMode": "auto", "needs": []int64{c.ID, a.ID, b.ID}})
	_ = e.fake.SetReply(*a.T3ThreadID, t3.Reply{Text: strings.Repeat("a", 3000) + "TAIL"})
	_ = e.fake.SetReply(*b.T3ThreadID, t3.Reply{Text: "b done"})
	_ = e.fake.SetReply(*c.T3ThreadID, t3.Reply{Text: "c done"})
	e.acceptThread(*a.T3ThreadID)
	e.acceptThread(*b.T3ThreadID)
	e.acceptThread(*c.T3ThreadID)
	got := e.handoffTask(d.ID)
	hs := got.CurrentHandoffs
	if got.State != "running" || len(hs) != 3 {
		t.Fatalf("fan-in %+v", got)
	}
	for i, src := range []int64{a.ID, b.ID, c.ID} {
		if hs[i].Status != "sent" || hs[i].SourceTaskID == nil || *hs[i].SourceTaskID != src {
			t.Fatalf("record %d %+v", i, hs[i])
		}
	}
	// 3 needs: each reply capped at min(4000, max(1000, 8000/3)) = 2666.
	if strings.Contains(hs[0].Text, "TAIL") || !strings.Contains(hs[0].Text, strings.Repeat("a", 2666)) ||
		strings.Contains(hs[0].Text, strings.Repeat("a", 2667)) ||
		!strings.Contains(hs[0].Text, "[Excerpt truncated;") {
		t.Fatal("fan-in reply must be capped at 2666 chars")
	}
	if want := "reply OK\n\n" + hs[0].Text + "\n\n" + hs[1].Text + "\n\n" + hs[2].Text; !strings.HasPrefix(e.lastMessage(), want+"\n\n<stage-journal>") {
		t.Fatalf("prompt must append the blocks by need id:\n%q", e.lastMessage())
	}
}

// A failed read skips that need only; a done but unsettled need (start
// early) still hands off; a need still running does not.
func TestUnacceptedNeedsCannotHandOff(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/blocked")
	a := e.task(p.ID, "a", map[string]any{"start": true})
	b := e.task(p.ID, "b", map[string]any{"needs": []int64{a.ID}})
	_ = e.fake.SetStatus(*a.T3ThreadID, t3.StatusCompleted)
	e.poll()
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", b.ID), nil, 409, nil)
	if got := e.handoffTask(b.ID); len(got.CurrentHandoffs) != 0 || got.LaunchAttempt != 0 {
		t.Fatalf("unaccepted launch: %+v", got)
	}
}

func TestHandoffNoReplyStillSendsContext(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/noreply")
	a := e.task(p.ID, "a", map[string]any{"start": true})
	b := e.task(p.ID, "b", map[string]any{"startMode": "auto", "needs": []int64{a.ID}})
	e.acceptThread(*a.T3ThreadID)
	h := e.handoffTask(b.ID).one(t)
	if h == nil || h.Status != "sent" || !strings.Contains(h.Text, "Final reply: missing at acceptance.") ||
		strings.Contains(h.Text, "Branch:") || strings.Contains(h.Text, "Pull requests:") {
		t.Fatalf("no-reply handoff %+v", h)
	}
}

func TestHandoffAcceptedSnapshotSurvivesNewAttempt(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/restart")
	a := e.task(p.ID, "a", map[string]any{"start": true})
	b := e.task(p.ID, "b", map[string]any{"startMode": "auto", "needs": []int64{a.ID}})
	thread := *a.T3ThreadID
	_ = e.fake.SetReply(thread, t3.Reply{ItemID: "r1", Text: "first"})
	e.t3.failLaunch.Store(true)
	e.acceptThread(thread)
	if got := e.handoffTask(b.ID); got.State != "backlog" || got.one(t) == nil || got.one(t).Attempt != 1 {
		t.Fatalf("first attempt %+v", got)
	}
	e.t3.failLaunch.Store(false)
	_ = e.fake.SetReply(thread, t3.Reply{ItemID: "r1", Text: "second"})
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", b.ID), nil, 200, nil)
	got := e.handoffTask(b.ID)
	h := got.one(t)
	if got.State != "running" || h == nil || h.Attempt != 2 || !strings.Contains(h.Text, "Cached final-reply excerpt:\nfirst") {
		t.Fatalf("new attempt must retain accepted snapshot: %+v / %+v", got, h)
	}
	if !strings.Contains(e.lastMessage(), "first") {
		t.Fatal("restart prompt must carry the accepted block")
	}
}

func TestHandoffReusedForSameAttempt(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/reuse")
	a := e.task(p.ID, "a", map[string]any{"start": true})
	b := e.task(p.ID, "b", map[string]any{"startMode": "auto", "needs": []int64{a.ID}})
	thread := *a.T3ThreadID
	_ = e.fake.SetReply(thread, t3.Reply{ItemID: "r1", Text: "original"})
	e.t3.lostLaunches.Store(2) // launch + same-id retry both lose the response
	e.acceptThread(thread)
	first := e.handoffTask(b.ID)
	if first.State != "launching" || first.one(t) == nil {
		t.Fatalf("lost launch %+v", first)
	}
	_ = e.fake.SetReply(thread, t3.Reply{ItemID: "r1", Text: "changed"})
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", b.ID), nil, 200, nil)
	got := e.handoffTask(b.ID)
	if got.LaunchAttempt != first.LaunchAttempt || got.one(t) == nil || got.one(t).Text != first.one(t).Text {
		t.Fatalf("same attempt must resend the stored block: %+v", got.CurrentHandoffs)
	}
	if msg := e.lastMessage(); !strings.Contains(msg, "original") || strings.Contains(msg, "changed") {
		t.Fatalf("prompt %q", msg)
	}
}

func TestHandoffToggleRules(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/toggle")
	a := e.task(p.ID, "a", nil)
	if got := e.handoffTask(a.ID); !got.Handoff {
		t.Fatal("handoff defaults on")
	}
	var out handoffTask
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", a.ID), map[string]any{"handoff": false}, 200, &out)
	if out.Handoff {
		t.Fatal("PATCH handoff false")
	}
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", a.ID), map[string]any{"handoff": nil}, 400, nil)
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", a.ID), nil, 200, nil)
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", a.ID), map[string]any{"handoff": true}, 409, nil)
}
