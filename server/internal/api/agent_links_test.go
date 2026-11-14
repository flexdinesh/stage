package api_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/flexdinesh/t3-stage/server/internal/t3"
)

type agentTask struct {
	task
	OriginThreadID    *string `json:"originThreadId"`
	OriginThreadTitle *string `json:"originThreadTitle"`
	AfterThreadID     *string `json:"afterThreadId"`
	AfterThreadTitle  *string `json:"afterThreadTitle"`
}

func (e *env) agentTask(projectID int64, title string, extra map[string]any, want int) agentTask {
	e.t.Helper()
	body := map[string]any{"projectId": projectID, "title": title, "prompt": "reply OK"}
	for k, v := range extra {
		body[k] = v
	}
	var out agentTask
	e.must("POST", "/api/tasks", body, want, &out)
	return out
}

func (e *env) getAgentTask(id int64) agentTask {
	e.t.Helper()
	var out agentTask
	e.must("GET", fmt.Sprintf("/api/tasks/%d", id), nil, 200, &out)
	return out
}

// runningSession launches an external (non-task) thread that is running.
func runningSession(t *testing.T, e *env, project, title string) string {
	t.Helper()
	ctx := context.Background()
	out, err := e.fake.LaunchThread(ctx, t3.LaunchInput{ProjectID: project, Title: title})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.sync.RefreshSession(ctx, out.ThreadID); err != nil {
		t.Fatal(err)
	}
	return out.ThreadID
}

func TestTaskRunsAfterSession(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/after-session")
	ctx := context.Background()
	s := runningSession(t, e, p.T3ProjectID, "Agent session")

	a := e.agentTask(p.ID, "a", map[string]any{"afterThreadId": s, "originThreadId": s, "start": true}, 201)
	b := e.agentTask(p.ID, "b", map[string]any{"afterThreadId": s}, 201) // fan-out
	if a.State != "backlog" || a.AfterThreadID == nil || *a.AfterThreadID != s || a.AfterTaskID != nil ||
		a.OriginThreadID == nil || *a.OriginThreadID != s || a.OriginThreadTitle == nil || *a.OriginThreadTitle != "Agent session" ||
		a.AfterThreadTitle == nil || *a.AfterThreadTitle != "Agent session" {
		t.Fatalf("a %+v", a)
	}

	// Done but unsettled, then failed: keeps waiting.
	_ = e.fake.SetStatus(s, t3.StatusCompleted)
	if err := e.sync.WatchThreads(ctx); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	if got := e.getAgentTask(a.ID); got.State != "backlog" {
		t.Fatalf("unsettled session must hold: %+v", got)
	}
	_ = e.fake.SetStatus(s, t3.StatusFailed)
	if err := e.sync.WatchThreads(ctx); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	if got := e.getAgentTask(b.ID); got.State != "backlog" {
		t.Fatalf("failed session must hold: %+v", got)
	}

	_ = e.fake.SetStatus(s, t3.StatusCompleted)
	_ = e.fake.Settle(s)
	if err := e.sync.WatchThreads(ctx); err != nil { // linked session refresh
		t.Fatal(err)
	}
	e.reconcile()
	for _, id := range []int64{a.ID, b.ID} {
		got := e.getAgentTask(id)
		if got.State != "running" || got.T3ThreadID == nil || *got.T3ThreadID == s || got.AfterThreadID == nil {
			t.Fatalf("after settle task %d must run, keeping its link: %+v", id, got)
		}
	}

	// Settled sessions cannot be linked; unknown sessions are 404.
	e.agentTask(p.ID, "c", map[string]any{"afterThreadId": s}, 409)
	e.agentTask(p.ID, "d", map[string]any{"afterThreadId": "nope"}, 404)
}

func TestAfterSessionOwnedByTaskBecomesTaskLink(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/after-task-session")
	a := e.agentTask(p.ID, "a", map[string]any{"start": true}, 201)
	b := e.agentTask(p.ID, "b", map[string]any{"afterThreadId": *a.T3ThreadID}, 201)
	if b.AfterTaskID == nil || *b.AfterTaskID != a.ID || b.AfterThreadID != nil {
		t.Fatalf("b must link to task %d: %+v", a.ID, b)
	}
	// Task links stay linear.
	e.agentTask(p.ID, "c", map[string]any{"afterThreadId": *a.T3ThreadID}, 409)
}

func TestOneWaitRules(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/one-wait")
	s := runningSession(t, e, p.T3ProjectID, "S")
	a := e.agentTask(p.ID, "a", nil, 201)
	e.agentTask(p.ID, "x", map[string]any{"afterThreadId": s, "afterTaskId": a.ID}, 400)
	e.agentTask(p.ID, "y", map[string]any{"afterThreadId": s, "startLocal": "2999-01-01T10:00", "startTz": "UTC"}, 400)

	b := e.agentTask(p.ID, "b", map[string]any{"afterTaskId": a.ID}, 201)
	path := fmt.Sprintf("/api/tasks/%d", b.ID)
	e.must("PATCH", path, map[string]any{"afterThreadId": s}, 400, nil)
	var got agentTask
	e.must("PATCH", path, map[string]any{"afterThreadId": s, "afterTaskId": nil}, 200, &got)
	if got.AfterTaskID != nil || got.AfterThreadID == nil || *got.AfterThreadID != s {
		t.Fatalf("switch to session link: %+v", got)
	}
	e.must("PATCH", path, map[string]any{"afterThreadId": nil}, 200, &got)
	if got.AfterThreadID != nil {
		t.Fatalf("unlink: %+v", got)
	}

	// Start now unlinks a waiting session link.
	c := e.agentTask(p.ID, "c", map[string]any{"afterThreadId": s}, 201)
	var started agentTask
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", c.ID), nil, 200, &started)
	if started.AfterThreadID != nil || started.State == "backlog" {
		t.Fatalf("start now: %+v", started)
	}
}

func TestScheduleOriginInherited(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/schedule-origin")
	s := runningSession(t, e, p.T3ProjectID, "S")
	var sc struct {
		ID             int64   `json:"id"`
		OriginThreadID *string `json:"originThreadId"`
	}
	e.must("POST", "/api/schedules", map[string]any{
		"projectId": p.ID, "title": "nightly", "prompt": "go", "cron": "0 2 * * *", "tz": "UTC", "originThreadId": s,
	}, 201, &sc)
	if sc.OriginThreadID == nil || *sc.OriginThreadID != s {
		t.Fatalf("schedule %+v", sc)
	}
	e.must("POST", "/api/schedules", map[string]any{
		"projectId": p.ID, "title": "n", "prompt": "go", "cron": "0 2 * * *", "tz": "UTC", "afterThreadId": s,
	}, 400, nil)
	var spawned agentTask
	e.must("POST", fmt.Sprintf("/api/schedules/%d/run", sc.ID), nil, 201, &spawned)
	if spawned.OriginThreadID == nil || *spawned.OriginThreadID != s {
		t.Fatalf("spawned %+v", spawned)
	}
}
