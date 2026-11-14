package api_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

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
	Handoff        bool        `json:"handoff"`
	AfterThreadID  *string     `json:"afterThreadId"`
	CurrentHandoff *handoffRec `json:"currentHandoff"`
}

func (e *env) handoffTask(id int64) handoffTask {
	e.t.Helper()
	var out handoffTask
	e.must("GET", fmt.Sprintf("/api/tasks/%d", id), nil, 200, &out)
	return out
}

// settle completes and settles thread, then lets sync and the link runner act
// (launch failures are asserted through task state).
func (e *env) settle(thread string) {
	e.t.Helper()
	_ = e.fake.SetStatus(thread, t3.StatusCompleted)
	_ = e.fake.Settle(thread)
	e.poll()
	if err := e.sync.WatchThreads(context.Background()); err != nil {
		e.t.Fatal(err)
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

func TestHandoffAppendedForTaskLink(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/handoff")
	a := e.task(p.ID, "Build API", map[string]any{"start": true})
	b := e.task(p.ID, "Write docs", map[string]any{"afterTaskId": a.ID})
	if got := e.handoffTask(b.ID); !got.Handoff || got.CurrentHandoff != nil {
		t.Fatalf("linked task: handoff must default on with no record yet: %+v", got)
	}
	thread := *a.T3ThreadID
	_ = e.fake.SetThread(thread, func(d *t3.ThreadDetail) {
		d.Branch, d.WorktreePath = "feat/api", "/w/wt-api"
		d.PullRequests = []t3.PullRequest{{Repository: "me/app", Number: 41, URL: "https://github.com/me/app/pull/41", State: "open"}}
	})
	_ = e.fake.SetReply(thread, t3.Reply{Text: "Shipped the API; see PR 41."})
	e.settle(thread)

	got := e.handoffTask(b.ID)
	if got.State != "running" || got.CurrentHandoff == nil {
		t.Fatalf("follower must run with a handoff: %+v", got)
	}
	h := got.CurrentHandoff
	if h.Status != "sent" || h.Attempt != 1 || h.SourceTaskID == nil || *h.SourceTaskID != a.ID || h.SourceThreadID != thread {
		t.Fatalf("handoff record %+v", h)
	}
	if msg := e.lastMessage(); msg != "reply OK\n\n"+h.Text {
		t.Fatalf("launch prompt must be prompt + stored block:\n%q\nblock:\n%q", msg, h.Text)
	}
	for _, want := range []string{
		`<handoff from="stage">`, fmt.Sprintf(`"Build API" (task #%d)`, a.ID), "T3 thread: " + thread,
		"Branch: feat/api · Worktree: /w/wt-api", "me/app#41 (open) https://github.com/me/app/pull/41",
		"Final reply:\nShipped the API; see PR 41.", "</handoff>",
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
	b := e.task(p.ID, "b", map[string]any{"afterTaskId": a.ID})
	thread := *a.T3ThreadID
	_ = e.fake.SetReply(thread, t3.Reply{Text: strings.Repeat("x", 5000) + "TAIL"})
	e.settle(thread)
	h := e.handoffTask(b.ID).CurrentHandoff
	if h == nil || h.Status != "sent" {
		t.Fatalf("handoff %+v", h)
	}
	if strings.Contains(h.Text, "TAIL") || !strings.Contains(h.Text, strings.Repeat("x", 4000)) ||
		strings.Contains(h.Text, strings.Repeat("x", 4001)) {
		t.Fatal("reply must be capped at 4000 chars")
	}
	if !strings.Contains(h.Text, "[truncated at 4000 chars; full reply via t3_thread_read "+thread+"]") {
		t.Fatalf("missing truncation marker:\n%s", h.Text[len(h.Text)-200:])
	}
}

func TestHandoffOffSendsBarePrompt(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/off")
	a := e.task(p.ID, "a", map[string]any{"start": true})
	b := e.task(p.ID, "b", map[string]any{"afterTaskId": a.ID, "handoff": false})
	_ = e.fake.SetReply(*a.T3ThreadID, t3.Reply{Text: "done"})
	e.settle(*a.T3ThreadID)
	got := e.handoffTask(b.ID)
	if got.State != "running" || got.Handoff || got.CurrentHandoff != nil {
		t.Fatalf("handoff off: %+v", got)
	}
	if msg := e.lastMessage(); msg != "reply OK" {
		t.Fatalf("prompt %q", msg)
	}
}

func TestHandoffReadFailureLaunchesWithout(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/fail")
	a := e.task(p.ID, "a", map[string]any{"start": true})
	b := e.task(p.ID, "b", map[string]any{"afterTaskId": a.ID})
	e.t3.failReply.Store(true)
	e.settle(*a.T3ThreadID)
	got := e.handoffTask(b.ID)
	if got.State != "running" {
		t.Fatalf("a failed handoff read must not block the launch: %+v", got)
	}
	if msg := e.lastMessage(); msg != "reply OK" {
		t.Fatalf("prompt %q", msg)
	}
	h := got.CurrentHandoff
	if h == nil || h.Status != "skipped" || h.Text != "" || h.Reason == nil || !strings.Contains(*h.Reason, "t3 read timed out") {
		t.Fatalf("skipped record %+v", h)
	}
	if !e.hasEvent(b.ID, "handoff skipped: ") {
		t.Fatal("missing skip event")
	}
}

func TestHandoffForSessionLink(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/session")
	s := runningSession(t, e, p.T3ProjectID, "Planner")
	b := e.task(p.ID, "b", map[string]any{"afterThreadId": s})
	_ = e.fake.SetReply(s, t3.Reply{Text: "Plan ready."})
	e.settle(s)
	got := e.handoffTask(b.ID)
	h := got.CurrentHandoff
	if got.State != "running" || h == nil || h.Status != "sent" || h.SourceTaskID != nil || h.SourceThreadID != s || h.SourceTitle != "Planner" {
		t.Fatalf("session handoff %+v / %+v", got, h)
	}
	if !strings.Contains(h.Text, `"Planner" (session)`) || !strings.Contains(h.Text, "Plan ready.") {
		t.Fatalf("block:\n%s", h.Text)
	}
	if !e.hasEvent(b.ID, `handoff from session "Planner" appended`) {
		t.Fatal("missing event")
	}
}

func TestHandoffNoReplyStillSendsContext(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/noreply")
	a := e.task(p.ID, "a", map[string]any{"start": true})
	b := e.task(p.ID, "b", map[string]any{"afterTaskId": a.ID})
	e.settle(*a.T3ThreadID)
	h := e.handoffTask(b.ID).CurrentHandoff
	if h == nil || h.Status != "sent" || !strings.Contains(h.Text, "Final reply: none found.") ||
		strings.Contains(h.Text, "Branch:") || strings.Contains(h.Text, "Pull requests:") {
		t.Fatalf("no-reply handoff %+v", h)
	}
}

func TestHandoffRecomposedOnRestart(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/restart")
	a := e.task(p.ID, "a", map[string]any{"start": true})
	b := e.task(p.ID, "b", map[string]any{"afterTaskId": a.ID})
	thread := *a.T3ThreadID
	_ = e.fake.SetReply(thread, t3.Reply{ItemID: "r1", Text: "first"})
	e.t3.failLaunch.Store(true)
	e.settle(thread)
	if got := e.handoffTask(b.ID); got.State != "failed" || got.CurrentHandoff == nil || got.CurrentHandoff.Attempt != 1 {
		t.Fatalf("first attempt %+v", got)
	}
	e.t3.failLaunch.Store(false)
	_ = e.fake.SetReply(thread, t3.Reply{ItemID: "r1", Text: "second"})
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", b.ID), nil, 200, nil)
	got := e.handoffTask(b.ID)
	h := got.CurrentHandoff
	if got.State != "running" || h == nil || h.Attempt != 2 || !strings.Contains(h.Text, "Final reply:\nsecond") {
		t.Fatalf("restart must recompose: %+v / %+v", got, h)
	}
	if !strings.Contains(e.lastMessage(), "second") {
		t.Fatal("restart prompt must carry the fresh block")
	}
}

func TestHandoffReusedForSameAttempt(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/reuse")
	a := e.task(p.ID, "a", map[string]any{"start": true})
	b := e.task(p.ID, "b", map[string]any{"afterTaskId": a.ID})
	thread := *a.T3ThreadID
	_ = e.fake.SetReply(thread, t3.Reply{ItemID: "r1", Text: "original"})
	e.t3.lostLaunches.Store(2) // launch + same-id retry both lose the response
	e.settle(thread)
	first := e.handoffTask(b.ID)
	if first.State != "failed" || first.CurrentHandoff == nil {
		t.Fatalf("lost launch %+v", first)
	}
	_ = e.fake.SetReply(thread, t3.Reply{ItemID: "r1", Text: "changed"})
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", b.ID), nil, 200, nil)
	got := e.handoffTask(b.ID)
	if got.LaunchAttempt != first.LaunchAttempt || got.CurrentHandoff == nil || got.CurrentHandoff.Text != first.CurrentHandoff.Text {
		t.Fatalf("same attempt must resend the stored block: %+v", got.CurrentHandoff)
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
