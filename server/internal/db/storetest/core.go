package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/t3"
)

func init() {
	register("TokenStoreRoundTrip", caseTokenStoreRoundTrip)
	register("T3SettingsRoundTrip", caseT3SettingsRoundTrip)
	register("TransitionTask", caseTransitionTask)
	register("UpdateTaskRestrictions", caseUpdateTaskRestrictions)
	register("TransitionLaunchGuards", caseTransitionLaunchGuards)
	register("UpsertThreadsKeepsDetailColumns", caseUpsertThreadsKeepsDetailColumns)
	register("RunSpan", caseRunSpan)
	register("UpsertThreadDetailStoresRunSpan", caseUpsertThreadDetailStoresRunSpan)
	register("ProjectLastActivity", caseProjectLastActivity)
}

// session is a session on the seeded T3 connection (dbtest.T3Connection).
func session(external string) db.SessionRef {
	return db.SessionRef{ConnectionID: dbtest.T3Connection, ExternalSessionID: external}
}

func mustProject(t *testing.T, s *db.Store, ctx context.Context, t3id string) db.Project {
	t.Helper()
	p, err := s.InsertProject(ctx, db.Project{Title: t3id, Bindings: []db.ProjectBinding{{ConnectionID: dbtest.T3Connection, ExternalProjectID: t3id, Workspace: "/w/" + t3id}}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func mustTask(t *testing.T, s *db.Store, ctx context.Context, projectID int64, title string, needs []int64) db.Task {
	t.Helper()
	task, err := s.CreateTask(ctx, db.NewTask{ConnectionID: dbtest.T3Connection,
		ProjectID: projectID, Title: title, Prompt: "p",
		Needs: needs,
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func caseTokenStoreRoundTrip(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	tok, err := s.LoadToken(ctx, db.LocalMachine)
	if err != nil || tok != (t3.StoredToken{}) {
		t.Fatalf("empty token: %+v %v", tok, err)
	}
	exp := time.Date(2026, 12, 8, 10, 0, 0, 0, time.UTC)
	if err := s.SaveToken(ctx, db.LocalMachine, t3.StoredToken{ClientID: "c1", Token: "secret", ExpiresAt: exp}); err != nil {
		t.Fatal(err)
	}
	tok, err = s.LoadToken(ctx, db.LocalMachine)
	if err != nil || tok.ClientID != "c1" || tok.Token != "secret" || !tok.ExpiresAt.Equal(exp) {
		t.Fatalf("got %+v %v", tok, err)
	}
	if tok.ExpiresAt.Location() != time.UTC {
		t.Fatalf("timestamps must scan as UTC, got %v", tok.ExpiresAt.Location())
	}
	if err := s.SetLastError(ctx, db.LocalMachine, "boom"); err != nil {
		t.Fatal(err)
	}
	c, _ := s.GetConnection(ctx, db.LocalMachine)
	if c.LastError == nil || *c.LastError != "boom" {
		t.Fatalf("last error %v", c.LastError)
	}
	_ = s.SetLastError(ctx, db.LocalMachine, "")
	c, _ = s.GetConnection(ctx, db.LocalMachine)
	if c.LastError != nil {
		t.Fatalf("last error not cleared: %v", *c.LastError)
	}
}

func caseT3SettingsRoundTrip(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	c, err := s.GetConnection(ctx, db.LocalMachine)
	if err != nil || c.T3URL != nil || c.EnvironmentID != nil {
		t.Fatalf("fresh connection %+v %v", c, err)
	}
	if err := s.SetT3URL(ctx, db.LocalMachine, "http://127.0.0.1:4000"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SwitchInstance(ctx, db.LocalMachine, t3.Descriptor, "env-1", "box", 0); err != nil {
		t.Fatal(err)
	}
	c, _ = s.GetConnection(ctx, db.LocalMachine)
	if c.T3URL == nil || *c.T3URL != "http://127.0.0.1:4000" || c.EnvironmentID == nil || *c.EnvironmentID != "env-1" || c.ConnectionID != s.ActiveID(db.LocalMachine, "t3") {
		t.Fatalf("saved %+v", c)
	}
	// Saving a token leaves the settings alone.
	if err := s.SaveToken(ctx, db.LocalMachine, t3.StoredToken{ClientID: "c", Token: "t", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetT3URL(ctx, db.LocalMachine, ""); err != nil {
		t.Fatal(err)
	}
	c, _ = s.GetConnection(ctx, db.LocalMachine)
	if c.T3URL != nil || c.EnvironmentID == nil {
		t.Fatalf("cleared %+v", c)
	}
}

func caseTransitionTask(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "proj-1")
	task := mustTask(t, s, ctx, p.ID, "a", nil)
	if task.State != db.StateBacklog || task.LaunchAttempt != 0 {
		t.Fatalf("new task %+v", task)
	}

	res, err := s.TransitionTask(ctx, db.Transition{TaskID: task.ID, From: []db.TaskState{db.StateRunning}, To: db.StateDone})
	if err != nil || res.Changed || res.From != db.StateBacklog {
		t.Fatalf("guarded transition should not apply: %+v %v", res, err)
	}

	res, err = s.TransitionTask(ctx, db.Transition{TaskID: task.ID, From: []db.TaskState{db.StateBacklog}, To: db.StateLaunching, BeginAttempt: true})
	if err != nil || !res.Changed || res.Attempt != 1 {
		t.Fatalf("begin attempt: %+v %v", res, err)
	}
	thread := "thread-1"
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateRunning, Session: &db.SessionRef{ConnectionID: s.ActiveID(db.LocalMachine, "t3"), ExternalSessionID: thread}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateDone}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetTask(ctx, task.ID)
	if got.FinishedAt == nil || got.SettledAt != nil || got.LaunchedAt == nil || got.T3ThreadID == nil || *got.T3ThreadID != thread {
		t.Fatalf("after done: %+v", got)
	}
	finished := *got.FinishedAt
	// needs_input then done again: finishedAt keeps first entry.
	_, _ = s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateNeedsInput})
	_, _ = s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateDone})
	_, _ = s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateSettled, Detail: "thread settled"})
	got, _ = s.GetTask(ctx, task.ID)
	if !got.FinishedAt.Equal(finished) || got.SettledAt == nil || got.State != db.StateSettled {
		t.Fatalf("after settled: %+v", got)
	}
	// Same-state transition is a no-op.
	res, _ = s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateSettled})
	if res.Changed {
		t.Fatal("same-state transition must not apply")
	}
	events, _ := s.TaskEvents(ctx, task.ID)
	want := []db.TaskState{db.StateBacklog, db.StateLaunching, db.StateRunning, db.StateDone, db.StateNeedsInput, db.StateDone, db.StateSettled}
	if len(events) != len(want) {
		t.Fatalf("events %d, want %d: %+v", len(events), len(want), events)
	}
	for i, e := range events {
		if e.ToState != want[i] {
			t.Fatalf("event %d to %s, want %s", i, e.ToState, want[i])
		}
	}
	if events[0].FromState != nil || events[0].Detail != "created" {
		t.Fatalf("creation event %+v", events[0])
	}

	// Relaunch resets thread and timestamps.
	res, _ = s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateLaunching, BeginAttempt: true})
	got, _ = s.GetTask(ctx, task.ID)
	if res.Attempt != 2 || got.T3ThreadID != nil || got.FinishedAt != nil || got.SettledAt != nil {
		t.Fatalf("relaunch: %+v", got)
	}

	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: 9999, To: db.StateRunning}); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("missing task: %v", err)
	}
}

func caseUpdateTaskRestrictions(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "proj-1")
	task := mustTask(t, s, ctx, p.ID, "a", nil)
	prompt := db.Optional[string]{Set: true, V: "new prompt"}
	if _, err := s.UpdateTask(ctx, task.ID, db.TaskPatch{Prompt: prompt}); err != nil {
		t.Fatal(err)
	}
	_, _ = s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateRunning})
	var conflict *db.ConflictError
	if _, err := s.UpdateTask(ctx, task.ID, db.TaskPatch{Prompt: prompt}); !errors.As(err, &conflict) {
		t.Fatalf("prompt edit outside backlog: %v", err)
	}
	got, err := s.UpdateTask(ctx, task.ID, db.TaskPatch{
		Title: db.Optional[string]{Set: true, V: "renamed"},
	})
	if err != nil || got.Title != "renamed" {
		t.Fatalf("title edit: %+v %v", got, err)
	}
	if err := s.DeleteTask(ctx, task.ID); !errors.As(err, &conflict) {
		t.Fatalf("delete running: %v", err)
	}
}

func caseTransitionLaunchGuards(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "proj-1")
	step := mustTask(t, s, ctx, p.ID, "step", nil)
	begin := db.Transition{TaskID: step.ID, From: []db.TaskState{db.StateBacklog, db.StateFailed}, To: db.StateLaunching, BeginAttempt: true}
	res, err := s.TransitionTask(ctx, begin)
	if err != nil || !res.Changed || res.Attempt != 1 {
		t.Fatalf("runner launch: %+v %v", res, err)
	}
	// Definite failure: next start is a new attempt.
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: step.ID, To: db.StateFailed}); err != nil {
		t.Fatal(err)
	}
	if res, err = s.TransitionTask(ctx, begin); err != nil || !res.Changed || res.Attempt != 2 {
		t.Fatalf("new attempt: %+v %v", res, err)
	}
}

func caseUpsertThreadsKeepsDetailColumns(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	now := time.Now().UTC()
	d := t3.ThreadDetail{
		Thread:              t3.Thread{ThreadID: "th-1", ProjectID: "proj-1", Title: "x", Status: t3.StatusRunning, CreatedAt: now, UpdatedAt: now},
		Branch:              "feat",
		WorktreePath:        "/wt",
		PendingRequestCount: 2,
	}
	if err := s.UpsertThreadDetail(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertThreads(ctx, t3.Observations([]t3.Thread{{ThreadID: "th-1", ProjectID: "proj-1", Title: "renamed", Status: t3.StatusCompleted, CreatedAt: now, UpdatedAt: now}})); err != nil {
		t.Fatal(err)
	}
	o, err := s.Observation(ctx, session("th-1"))
	if err != nil {
		t.Fatal(err)
	}
	if o.Title != "renamed" || o.State != "done" || o.Branch != "feat" || o.Directory != "/wt" {
		t.Fatalf("observation %+v", o)
	}
	if _, err := s.TaskBySession(ctx, session("th-1")); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("an observed session is not a task: %v", err)
	}
}

func caseRunSpan(t *testing.T, open Factory) {
	base := time.Date(2026, 11, 10, 1, 0, 0, 0, time.UTC)
	at := func(m int) *time.Time { v := base.Add(time.Duration(m) * time.Minute); return &v }
	run := func(req int, start, done *time.Time) t3.Run {
		return t3.Run{RequestedAt: *at(req), StartedAt: start, CompletedAt: done}
	}
	created := base.Add(-time.Minute)
	cases := []struct {
		name       string
		d          t3.ThreadDetail
		start, end *time.Time
	}{
		{"no runs", t3.ThreadDetail{}, nil, nil},
		{"one run", t3.ThreadDetail{RunCount: 1, RecentRuns: []t3.Run{run(0, at(1), at(5))}}, at(1), at(5)},
		{"first start to last completion", t3.ThreadDetail{RunCount: 3, RecentRuns: []t3.Run{
			run(30, at(30), at(35)), run(20, nil, at(21)), run(0, at(1), at(2)),
		}}, at(1), at(35)},
		{"oldest never started uses requestedAt", t3.ThreadDetail{RunCount: 2, RecentRuns: []t3.Run{
			run(10, at(10), at(12)), run(0, nil, at(1)),
		}}, at(0), at(12)},
		{"newest incomplete", t3.ThreadDetail{RunCount: 1, RecentRuns: []t3.Run{run(0, at(1), nil)}}, at(1), nil},
		{"older runs not read fall back to createdAt", t3.ThreadDetail{
			Thread: t3.Thread{CreatedAt: created}, RunCount: 60, RecentRuns: []t3.Run{run(30, at(30), at(40))},
		}, &created, at(40)},
	}
	eq := func(a, b *time.Time) bool { return (a == nil) == (b == nil) && (a == nil || a.Equal(*b)) }
	for _, c := range cases {
		start, end := db.RunSpan(c.d)
		if !eq(start, c.start) || !eq(end, c.end) {
			t.Errorf("%s: span %v..%v, want %v..%v", c.name, start, end, c.start, c.end)
		}
	}
}

func caseUpsertThreadDetailStoresRunSpan(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	now := time.Now().UTC().Truncate(time.Millisecond)
	started, done := now.Add(-10*time.Minute), now.Add(-2*time.Minute)
	d := t3.ThreadDetail{
		Thread:     t3.Thread{ThreadID: "th-span", ProjectID: "proj-1", Title: "x", Status: t3.StatusCompleted, CreatedAt: started, UpdatedAt: now},
		RunCount:   1,
		RecentRuns: []t3.Run{{RunID: "r1", Status: t3.StatusCompleted, RequestedAt: started, StartedAt: &started, CompletedAt: &done}},
	}
	if err := s.UpsertThreadDetail(ctx, d); err != nil {
		t.Fatal(err)
	}
	// A list upsert leaves the span alone.
	if err := s.UpsertThreads(ctx, t3.Observations([]t3.Thread{d.Thread})); err != nil {
		t.Fatal(err)
	}
	o, err := s.Observation(ctx, session("th-span"))
	if err != nil {
		t.Fatal(err)
	}
	if o.StartedAt == nil || !o.StartedAt.Equal(started) || o.EndedAt == nil || !o.EndedAt.Equal(done) {
		t.Fatalf("span %v..%v, want %v..%v", o.StartedAt, o.EndedAt, started, done)
	}
}

func caseProjectLastActivity(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "proj-1")
	if p.LastActivityAt != nil {
		t.Fatalf("new project activity %v", p.LastActivityAt)
	}
	// Observed sessions are not project activity: only Stage's tasks and
	// journal are.
	created := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	if err := s.UpsertThreads(ctx, t3.Observations([]t3.Thread{
		{ThreadID: "th-1", ProjectID: "proj-1", Status: t3.StatusCompleted, CreatedAt: created, UpdatedAt: created.Add(time.Hour)},
	})); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetProject(ctx, p.ID)
	if err != nil || p.LastActivityAt != nil {
		t.Fatalf("observed session activity: %v %v", p.LastActivityAt, err)
	}
	task := mustTask(t, s, ctx, p.ID, "a", nil)
	latest := task.UpdatedAt
	for _, e := range journal(t, s, ctx, db.JournalOwner{TaskID: &task.ID}, db.JournalQuery{Kind: "all"}).Entries {
		if e.CreatedAt.After(latest) {
			latest = e.CreatedAt
		}
	}
	ps, err := s.ListProjects(ctx, "false")
	if err != nil || len(ps) != 1 || ps[0].LastActivityAt == nil || !ps[0].LastActivityAt.Equal(latest) {
		t.Fatalf("task and journal activity: want %v, got %+v %v", latest, ps, err)
	}
}
