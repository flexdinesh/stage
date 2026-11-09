package api_test

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/flexdinesh/t3-stage/server/internal/db"
	"github.com/flexdinesh/t3-stage/server/internal/t3"
)

func externalSession(t *testing.T, e *env, p string, title string, settled bool) string {
	t.Helper()
	ctx := context.Background()
	out, err := e.fake.LaunchThread(ctx, t3.LaunchInput{ProjectID: p, Title: title})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.fake.SetStatus(out.ThreadID, t3.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	if settled {
		if err := e.fake.Settle(out.ThreadID); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.sync.RefreshSession(ctx, out.ThreadID); err != nil {
		t.Fatal(err)
	}
	return out.ThreadID
}

func TestContinuationHTTPContract(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/session-http")
	ctx := context.Background()
	a, b, c := externalSession(t, e, p.T3ProjectID, "A", true), externalSession(t, e, p.T3ProjectID, "B", false), externalSession(t, e, p.T3ProjectID, "C", false)
	body := map[string]any{"afterThreadId": a, "targetThreadId": b, "prompt": "  exact\n"}
	var saved struct {
		db.Continuation
		AfterThread struct {
			T3WebURL *string `json:"t3WebUrl"`
		} `json:"afterThread"`
		TargetThread struct {
			T3WebURL *string `json:"t3WebUrl"`
		} `json:"targetThread"`
	}
	e.must("POST", "/api/continuations", body, 201, &saved)
	if saved.ID == 0 || saved.Prompt != "  exact\n" || saved.State != db.ContinuationWaiting || saved.WaitReasons == nil || saved.RunID != nil || saved.MessageID != nil || saved.AfterThread.T3WebURL == nil || saved.TargetThread.T3WebURL == nil || len(e.fake.Sent) != 0 {
		t.Fatalf("saved=%+v sends=%v", saved, e.fake.Sent)
	}
	id := saved.ID
	path := fmt.Sprintf("/api/continuations/%d", id)
	e.must("PATCH", path, map[string]any{"prompt": ""}, 400, nil)
	e.must("PATCH", path, map[string]any{"prompt": nil}, 400, nil)
	e.must("PATCH", path, map[string]any{"prompt": "new\n"}, 200, &saved)
	e.must("POST", path+"/pause", nil, 200, &saved)
	if saved.State != db.ContinuationPaused {
		t.Fatal("pause failed")
	}
	if err := e.sync.ReconcileContinuations(ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.fake.Sent) != 0 {
		t.Fatal("paused continuation sent")
	}
	if err := e.fake.SetStatus(b, t3.StatusRunning); err != nil {
		t.Fatal(err)
	}
	e.must("POST", path+"/resume", nil, 200, &saved)
	if saved.State != db.ContinuationWaiting || !slices.Contains(saved.WaitReasons, db.WaitTargetRunning) {
		t.Fatalf("resume bypassed readiness: %+v", saved)
	}
	e.must("POST", "/api/continuations", body, 409, nil)
	e.must("POST", "/api/continuations", map[string]any{"afterThreadId": "missing", "targetThreadId": c, "prompt": "x"}, 404, nil)
	e.must("POST", "/api/continuations", map[string]any{"afterThreadId": a, "targetThreadId": a, "prompt": "x"}, 409, nil)
	e.must("POST", "/api/continuations", map[string]any{"afterThreadId": a, "targetThreadId": c, "prompt": " "}, 400, nil)
	e.must("PATCH", "/api/continuations/999999", map[string]any{"prompt": "x"}, 404, nil)
	e.must("POST", "/api/continuations/999999/pause", nil, 404, nil)
	var child db.Continuation
	e.must("POST", "/api/continuations", map[string]any{"afterThreadId": b, "targetThreadId": c, "prompt": "downstream"}, 201, &child)
	var detail struct {
		Thread        db.Thread         `json:"thread"`
		Children      []db.Thread       `json:"children"`
		Continuations []db.Continuation `json:"continuations"`
		Task          *db.Task          `json:"task"`
	}
	e.must("GET", "/api/threads/"+url.PathEscape(c), nil, 200, &detail)
	if detail.Thread.T3ThreadID != c || detail.Children == nil || len(detail.Continuations) != 2 || detail.Continuations[0].ID != id || detail.Task != nil {
		t.Fatalf("detail=%+v", detail)
	}
	e.must("POST", path+"/cancel", nil, 200, nil)
	var list struct {
		Continuations []db.Continuation `json:"continuations"`
	}
	e.must("GET", "/api/continuations?thread="+url.QueryEscape(c), nil, 200, &list)
	if len(list.Continuations) != 2 || list.Continuations[1].State != db.ContinuationBlocked || !slices.Contains(list.Continuations[1].WaitReasons, db.WaitUpstreamCancelled) {
		t.Fatalf("order=%+v", list)
	}
	if len(e.fake.Sent) != 0 {
		t.Fatal("GET or mutation sent a message")
	}
}

func TestSessionFiltersPaginationAndLiveFrozenTasks(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/live-sessions")
	ctx := context.Background()
	app := e.task(p.ID, "app session", nil)
	var started task
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", app.ID), nil, 200, &started)
	if started.T3ThreadID == nil {
		t.Fatal("task not started")
	}
	appID := *started.T3ThreadID
	if err := e.fake.SetStatus(appID, t3.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	follower := e.task(p.ID, "legacy backlog", nil)
	if _, err := e.store.UpdateTask(ctx, follower.ID, db.TaskPatch{AfterTaskID: db.Optional[int64]{Set: true, V: app.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.TransitionTask(ctx, db.Transition{TaskID: app.ID, To: db.StateSettled}); err != nil {
		t.Fatal(err)
	}
	if err := e.fake.SetStatus(appID, t3.StatusRunning); err != nil {
		t.Fatal(err)
	}
	if err := e.fake.SetThread(appID, func(d *t3.ThreadDetail) { d.CreatedAt = time.Now().AddDate(-2, 0, 0) }); err != nil {
		t.Fatal(err)
	}
	external := externalSession(t, e, p.T3ProjectID, "external input", false)
	if err := e.fake.SetThread(external, func(d *t3.ThreadDetail) { d.CreatedAt = time.Now().AddDate(-3, 0, 0); d.PendingRequestCount = 1 }); err != nil {
		t.Fatal(err)
	}
	child := externalSession(t, e, p.T3ProjectID, "inspectable child", false)
	if err := e.fake.SetThread(child, func(d *t3.ThreadDetail) { d.ParentThreadID = external; d.RelationshipToParent = "subagent" }); err != nil {
		t.Fatal(err)
	}
	archived := externalSession(t, e, p.T3ProjectID, "archived", false)
	if err := e.fake.SetThread(archived, func(d *t3.ThreadDetail) { d.Archived = true; d.PendingRequestCount = 1 }); err != nil {
		t.Fatal(err)
	}
	if _, err := e.sync.FullSync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.sync.WatchThreads(ctx); err != nil {
		t.Fatal(err)
	}
	var list struct {
		Threads []db.Thread `json:"threads"`
		Total   int         `json:"total"`
	}
	e.must("GET", "/api/threads?active=1&subagents=exclude&sort=activity&limit=1", nil, 200, &list)
	if list.Total != 2 || len(list.Threads) != 1 {
		t.Fatalf("active page=%+v", list)
	}
	e.must("GET", "/api/threads?active=1&subagents=exclude&sort=activity&limit=1&offset=1", nil, 200, &list)
	if list.Total != 2 || len(list.Threads) != 1 {
		t.Fatalf("active second page=%+v", list)
	}
	e.must("GET", "/api/threads?state=running&source=app", nil, 200, &list)
	if list.Total != 1 || list.Threads[0].T3ThreadID != appID || list.Threads[0].TaskID == nil || list.Threads[0].State != db.StateRunning {
		t.Fatalf("frozen task session hidden: %+v", list)
	}
	frozen, err := e.store.GetTask(ctx, app.ID)
	if err != nil || frozen.State != db.StateSettled {
		t.Fatalf("legacy state changed: %+v %v", frozen, err)
	}
	e.must("GET", "/api/threads?state=needs_input&state=failed&source=t3&q=external", nil, 200, &list)
	if list.Total != 1 || list.Threads[0].T3ThreadID != external {
		t.Fatalf("normalized filters=%+v", list)
	}
	e.must("GET", "/api/threads?parent="+url.QueryEscape(external), nil, 200, &list)
	if list.Total != 1 || list.Threads[0].T3ThreadID != child {
		t.Fatalf("children filter=%+v", list)
	}
	var detail struct {
		Thread db.Thread `json:"thread"`
		Task   *db.Task  `json:"task"`
	}
	e.must("GET", "/api/threads/"+url.PathEscape(appID), nil, 200, &detail)
	if detail.Task == nil || detail.Task.ID != app.ID || detail.Thread.ActiveRunID != nil { // fake restarted status has no new run id
		t.Fatalf("app detail=%+v", detail)
	}
	e.must("POST", "/api/continuations", map[string]any{"afterThreadId": external, "targetThreadId": child, "prompt": "x"}, 409, nil)
	e.must("POST", "/api/continuations", map[string]any{"afterThreadId": external, "targetThreadId": archived, "prompt": "x"}, 409, nil)
	for _, query := range []string{"state=bogus", "active=no", "sort=bogus", "parent=", "limit=0", "offset=-1"} {
		e.must("GET", "/api/threads?"+query, nil, 400, nil)
	}
}

func TestSessionIDPathSegment(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := "provider:session/with%id"
	now := time.Now().UTC()
	if err := e.store.UpsertThreads(ctx, []t3.Thread{{ThreadID: id, ProjectID: "p", Title: "encoded", Status: t3.StatusCompleted, CreatedAt: now, UpdatedAt: now}}); err != nil {
		t.Fatal(err)
	}
	// A connected T3 correctly returns 404 for a session it does not have,
	// demonstrating that ServeMux decoded the entire escaped segment once.
	e.must("GET", "/api/threads/"+url.PathEscape(id), nil, 404, nil)
}

func TestActiveSessionsOverrideStaleSettledFlags(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/stale-settled")
	ctx := context.Background()
	running := externalSession(t, e, p.T3ProjectID, "settled but running", true)
	input := externalSession(t, e, p.T3ProjectID, "settled but needs input", true)
	idle := externalSession(t, e, p.T3ProjectID, "idle but latest run active", true)
	for _, id := range []string{running, input, idle} {
		if err := e.fake.SetThread(id, func(d *t3.ThreadDetail) {
			switch id {
			case running:
				d.Status = t3.StatusRunning
				d.ActiveRunID = d.LatestRunID
			case input:
				d.PendingRequestCount = 1
			case idle:
				d.Status = t3.StatusIdle
				d.ActiveRunID = ""
				d.RecentRuns[0].Status = t3.StatusWaiting
			}
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.sync.FullSync(ctx); err != nil {
		t.Fatal(err)
	}
	var list struct {
		Threads []db.Thread `json:"threads"`
		Total   int         `json:"total"`
	}
	e.must("GET", "/api/threads?active=1&subagents=exclude", nil, 200, &list)
	if list.Total != 3 {
		t.Fatalf("stale settled flag hid active sessions: %+v", list)
	}
	for _, thread := range list.Threads {
		want := db.StateRunning
		if thread.T3ThreadID == input {
			want = db.StateNeedsInput
		}
		if !thread.Settled || thread.State != want {
			t.Fatalf("normalized state mismatch: %+v", thread)
		}
	}
}
