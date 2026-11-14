package db_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/t3"
)

var testDB *dbtest.DB

func TestMain(m *testing.M) {
	testDB = dbtest.Open("stage_test_db")
	if testDB.Skip != "" {
		fmt.Println("db integration tests skipped:", testDB.Skip)
	}
	code := m.Run()
	testDB.Close()
	os.Exit(code)
}

func setup(t *testing.T) (*db.Store, context.Context) {
	return db.NewStore(testDB.Reset(t)), context.Background()
}

func mustProject(t *testing.T, s *db.Store, ctx context.Context, t3id string) db.Project {
	t.Helper()
	p, err := s.InsertProject(ctx, db.Project{T3ProjectID: t3id, Title: t3id, WorkspaceRoot: "/w/" + t3id, Source: db.SourceApp})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func mustTask(t *testing.T, s *db.Store, ctx context.Context, projectID int64, title string, after *int64) db.Task {
	t.Helper()
	task, err := s.CreateTask(ctx, db.NewTask{
		ProjectID: projectID, Title: title, Prompt: "p", RuntimeMode: "full-access",
		InteractionMode: "default", Workspace: db.Workspace{Type: "root"}, AfterTaskID: after,
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestTokenStoreRoundTrip(t *testing.T) {
	s, ctx := setup(t)
	tok, err := s.LoadToken(ctx)
	if err != nil || tok != (t3.StoredToken{}) {
		t.Fatalf("empty token: %+v %v", tok, err)
	}
	exp := time.Date(2026, 12, 8, 10, 0, 0, 0, time.UTC)
	if err := s.SaveToken(ctx, t3.StoredToken{ClientID: "c1", Token: "secret", ExpiresAt: exp}); err != nil {
		t.Fatal(err)
	}
	tok, err = s.LoadToken(ctx)
	if err != nil || tok.ClientID != "c1" || tok.Token != "secret" || !tok.ExpiresAt.Equal(exp) {
		t.Fatalf("got %+v %v", tok, err)
	}
	if tok.ExpiresAt.Location() != time.UTC {
		t.Fatalf("timestamps must scan as UTC, got %v", tok.ExpiresAt.Location())
	}
	if err := s.SetLastError(ctx, "boom"); err != nil {
		t.Fatal(err)
	}
	c, _ := s.GetConnection(ctx)
	if c.LastError == nil || *c.LastError != "boom" {
		t.Fatalf("last error %v", c.LastError)
	}
	_ = s.SetLastError(ctx, "")
	c, _ = s.GetConnection(ctx)
	if c.LastError != nil {
		t.Fatalf("last error not cleared: %v", *c.LastError)
	}
}

func TestTransitionTask(t *testing.T) {
	s, ctx := setup(t)
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
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateRunning, ThreadID: &thread}); err != nil {
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

func TestUpdateTaskRestrictions(t *testing.T) {
	s, ctx := setup(t)
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
		Tags:  db.Optional[[]string]{Set: true, V: []string{"x"}},
	})
	if err != nil || got.Title != "renamed" || len(got.Tags) != 1 {
		t.Fatalf("title/tags edit: %+v %v", got, err)
	}
	if err := s.DeleteTask(ctx, task.ID); !errors.As(err, &conflict) {
		t.Fatalf("delete running: %v", err)
	}
	var invalid *db.InvalidError
	_, _ = s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateBacklog})
	if _, err := s.UpdateTask(ctx, task.ID, db.TaskPatch{Model: db.Optional[string]{Set: true, V: "m"}}); !errors.As(err, &invalid) {
		t.Fatalf("model without provider: %v", err)
	}
}

func TestTransitionLaunchGuards(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "proj-1")
	step := mustTask(t, s, ctx, p.ID, "step", nil)
	begin := db.Transition{TaskID: step.ID, From: []db.TaskState{db.StateBacklog, db.StateFailed}, To: db.StateLaunching, BeginAttempt: true}
	res, err := s.TransitionTask(ctx, begin)
	if err != nil || !res.Changed || res.Attempt != 1 {
		t.Fatalf("runner launch: %+v %v", res, err)
	}
	// Transport failure: outcome unknown, next start keeps attempt 1.
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: step.ID, To: db.StateFailed, ReuseAttempt: true}); err != nil {
		t.Fatal(err)
	}
	if res, err = s.TransitionTask(ctx, begin); err != nil || !res.Changed || res.Attempt != 1 {
		t.Fatalf("reuse attempt: %+v %v", res, err)
	}
	// Definite failure: next start is a new attempt.
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: step.ID, To: db.StateFailed}); err != nil {
		t.Fatal(err)
	}
	if res, err = s.TransitionTask(ctx, begin); err != nil || !res.Changed || res.Attempt != 2 {
		t.Fatalf("new attempt: %+v %v", res, err)
	}
}

func TestPollableTasksThrottlesStaleCompleted(t *testing.T) {
	pool := testDB.Reset(t)
	s, ctx := db.NewStore(pool), context.Background()
	p := mustProject(t, s, ctx, "proj-1")
	ids := map[string]int64{}
	for i, name := range []string{"running", "fresh", "stale"} {
		task := mustTask(t, s, ctx, p.ID, name, nil)
		thread := fmt.Sprintf("thread-%d", i)
		_, _ = s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateRunning, ThreadID: &thread})
		if name != "running" {
			_, _ = s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateDone})
		}
		ids[name] = task.ID
	}
	if _, err := pool.Exec(ctx, `UPDATE tasks SET updated_at = now() - interval '2 hours', finished_at = now() - interval '2 hours' WHERE id = $1`, ids["stale"]); err != nil {
		t.Fatal(err)
	}
	titles := func(includeStale bool) []string {
		tasks, err := s.PollableTasks(ctx, includeStale)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, task := range tasks {
			out = append(out, task.Title)
		}
		return out
	}
	if got := fmt.Sprint(titles(false)); got != "[running fresh]" {
		t.Fatalf("frequent poll %s", got)
	}
	if got := fmt.Sprint(titles(true)); got != "[running fresh stale]" {
		t.Fatalf("stale poll %s", got)
	}
}

func TestUpsertThreadsKeepsDetailColumns(t *testing.T) {
	s, ctx := setup(t)
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
	if err := s.UpsertThreads(ctx, []t3.Thread{{ThreadID: "th-1", ProjectID: "proj-1", Title: "renamed", Status: t3.StatusCompleted, CreatedAt: now, UpdatedAt: now}}); err != nil {
		t.Fatal(err)
	}
	th, err := s.GetThread(ctx, "th-1")
	if err != nil {
		t.Fatal(err)
	}
	if th.Title != "renamed" || th.Status != "completed" || th.Branch == nil || *th.Branch != "feat" || th.PendingRequestCount != 2 {
		t.Fatalf("thread %+v", th)
	}
	if th.Source != db.SourceT3 || th.ProjectID != nil {
		t.Fatalf("unlinked thread source/project: %+v", th)
	}
}

func TestRunSpan(t *testing.T) {
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

func TestUpsertThreadDetailStoresRunSpan(t *testing.T) {
	s, ctx := setup(t)
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
	if err := s.UpsertThreads(ctx, []t3.Thread{d.Thread}); err != nil {
		t.Fatal(err)
	}
	th, err := s.GetThread(ctx, "th-span")
	if err != nil {
		t.Fatal(err)
	}
	if th.RunStartedAt == nil || !th.RunStartedAt.Equal(started) || th.RunEndedAt == nil || !th.RunEndedAt.Equal(done) {
		t.Fatalf("span %v..%v, want %v..%v", th.RunStartedAt, th.RunEndedAt, started, done)
	}
}

func TestUpsertT3ProjectsFollowsT3(t *testing.T) {
	s, ctx := setup(t)
	upsert := func(ps ...t3.Project) int {
		t.Helper()
		n, err := s.UpsertT3Projects(ctx, ps)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	get := func(id int64) db.Project {
		t.Helper()
		p, err := s.GetProject(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	gone := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	if n := upsert(
		t3.Project{ID: "p1", Title: "main", WorkspaceRoot: "/one/main"},
		t3.Project{ID: "p2", Title: "Gone", WorkspaceRoot: "/gone", DeletedAt: &gone},
		t3.Project{ID: "p3", WorkspaceRoot: "/untitled"},
	); n != 2 {
		t.Fatalf("live count %d", n)
	}
	if _, err := s.ProjectByWorkspaceRoot(ctx, "/gone"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("never-mirrored deleted project inserted: %v", err)
	}
	untitled, err := s.ProjectByWorkspaceRoot(ctx, "/untitled")
	if err != nil || untitled.Title != "/untitled" {
		t.Fatalf("empty title fallback %+v %v", untitled, err)
	}
	p, err := s.ProjectByWorkspaceRoot(ctx, "/one/main")
	if err != nil || p.Source != db.SourceT3 || p.Title != "main" || p.ArchivedAt != nil {
		t.Fatalf("project %+v %v", p, err)
	}

	// Unchanged resync keeps updated_at.
	upsert(t3.Project{ID: "p1", Title: "main", WorkspaceRoot: "/one/main"})
	if got := get(p.ID); !got.UpdatedAt.Equal(p.UpdatedAt) {
		t.Fatalf("updated_at moved on no-op sync: %v -> %v", p.UpdatedAt, got.UpdatedAt)
	}

	// T3 rename, move and archive follow.
	archived := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	upsert(t3.Project{ID: "p1", Title: "one", WorkspaceRoot: "/one-moved", ArchivedAt: &archived})
	got := get(p.ID)
	if got.Title != "one" || got.WorkspaceRoot != "/one-moved" || got.ArchivedAt == nil || !got.ArchivedAt.Equal(archived) {
		t.Fatalf("after rename/archive %+v", got)
	}
	if !got.UpdatedAt.After(p.UpdatedAt) {
		t.Fatalf("updated_at not bumped: %v", got.UpdatedAt)
	}

	// T3 unarchive follows.
	upsert(t3.Project{ID: "p1", Title: "one", WorkspaceRoot: "/one-moved"})
	if got := get(p.ID); got.ArchivedAt != nil {
		t.Fatalf("after unarchive %+v", got)
	}

	// T3 delete: row stays, archived at deletedAt, root released.
	upsert(t3.Project{ID: "p1", Title: "one", WorkspaceRoot: "/one-moved", DeletedAt: &gone})
	got = get(p.ID)
	if got.ArchivedAt == nil || !got.ArchivedAt.Equal(gone) {
		t.Fatalf("deleted not archived %+v", got)
	}
	if _, err := s.ProjectByWorkspaceRoot(ctx, "/one-moved"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("deleted project still claims root: %v", err)
	}
	// Resync of the deleted project keeps updated_at.
	upsert(t3.Project{ID: "p1", Title: "one", WorkspaceRoot: "/one-moved", DeletedAt: &gone})
	if again := get(p.ID); !again.UpdatedAt.Equal(got.UpdatedAt) {
		t.Fatalf("updated_at moved on deleted no-op sync")
	}
}

func TestProjectLastActivity(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "proj-1")
	if p.LastActivityAt != nil {
		t.Fatalf("new project activity %v", p.LastActivityAt)
	}
	created := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	settled := created.Add(3 * time.Hour)
	if err := s.UpsertThreads(ctx, []t3.Thread{
		{ThreadID: "th-1", ProjectID: "proj-1", Status: t3.StatusCompleted, CreatedAt: created, UpdatedAt: created.Add(time.Hour), Settled: true, SettledAt: &settled},
		{ThreadID: "th-2", ProjectID: "proj-1", Status: t3.StatusCompleted, CreatedAt: created, UpdatedAt: created.Add(2 * time.Hour)},
		{ThreadID: "th-x", ProjectID: "proj-other", Status: t3.StatusCompleted, CreatedAt: created, UpdatedAt: settled.Add(time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetProject(ctx, p.ID)
	if err != nil || p.LastActivityAt == nil || !p.LastActivityAt.Equal(settled) {
		t.Fatalf("thread activity: want settle %v, got %v %v", settled, p.LastActivityAt, err)
	}
	task := mustTask(t, s, ctx, p.ID, "a", nil)
	ps, err := s.ListProjects(ctx, "false")
	if err != nil || len(ps) != 1 || ps[0].LastActivityAt == nil || !ps[0].LastActivityAt.Equal(task.UpdatedAt) {
		t.Fatalf("task activity: want %v, got %+v %v", task.UpdatedAt, ps, err)
	}
}

func TestListThreadsAndStatsExcludeSubagents(t *testing.T) {
	s, ctx := setup(t)
	now := time.Now().UTC()
	threads := []t3.Thread{
		{ThreadID: "root-1", ProjectID: "proj-1", Title: "root", Status: t3.StatusCompleted, CreatedAt: now.Add(-2 * time.Minute)},
		{ThreadID: "sub-1", ProjectID: "proj-1", Title: "sub", Status: t3.StatusRunning, CreatedAt: now.Add(-time.Minute),
			ParentThreadID: "root-1", RelationshipToParent: "subagent"},
	}
	if err := s.UpsertThreads(ctx, threads); err != nil {
		t.Fatal(err)
	}

	all, total, err := s.ListThreads(ctx, db.ThreadFilter{Limit: 10})
	if err != nil || total != 2 || len(all) != 2 {
		t.Fatalf("include: %d %d %v", total, len(all), err)
	}
	roots, total, err := s.ListThreads(ctx, db.ThreadFilter{Limit: 10, ExcludeSubagents: true})
	if err != nil || total != 1 || len(roots) != 1 || roots[0].T3ThreadID != "root-1" {
		t.Fatalf("exclude: %d %+v %v", total, roots, err)
	}

	from, to := now.Add(-time.Hour), now.Add(time.Hour)
	sum := func(st db.Stats) (day, project, status int) {
		for _, c := range st.ThreadsByDay {
			day += c.Count
		}
		for _, c := range st.ThreadsByProject {
			project += c.Count
		}
		for _, c := range st.ThreadsByStatus {
			status += c.Count
		}
		return
	}
	st, err := s.Stats(ctx, from, to, "UTC", false)
	if err != nil {
		t.Fatal(err)
	}
	if d, p, ss := sum(st); d != 2 || p != 2 || ss != 2 || len(st.ThreadsByStatus) != 2 {
		t.Fatalf("stats include: day=%d project=%d status=%d %+v", d, p, ss, st.ThreadsByStatus)
	}
	st, err = s.Stats(ctx, from, to, "UTC", true)
	if err != nil {
		t.Fatal(err)
	}
	if d, p, ss := sum(st); d != 1 || p != 1 || ss != 1 || st.ThreadsByStatus[0].Status != "completed" {
		t.Fatalf("stats exclude: day=%d project=%d status=%d %+v", d, p, ss, st.ThreadsByStatus)
	}
}

func TestLinks(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "proj-1")
	q := mustProject(t, s, ctx, "proj-2")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	b := mustTask(t, s, ctx, q.ID, "b", &a.ID) // links may span projects
	if b.AfterTaskID == nil || *b.AfterTaskID != a.ID {
		t.Fatalf("created link %+v", b)
	}
	if got, _ := s.GetTask(ctx, a.ID); got.NextTaskID == nil || *got.NextTaskID != b.ID {
		t.Fatalf("next of a %+v", got)
	}
	var conflict *db.ConflictError
	// a already has a follower: refuse.
	c := mustTask(t, s, ctx, p.ID, "c", nil)
	if _, err := s.UpdateTask(ctx, c.ID, db.TaskPatch{AfterTaskID: db.Optional[int64]{Set: true, V: a.ID}}); !errors.As(err, &conflict) {
		t.Fatalf("second follower: %v", err)
	}
	if _, err := s.CreateTask(ctx, db.NewTask{ProjectID: p.ID, Title: "x", Prompt: "p", RuntimeMode: "full-access",
		InteractionMode: "default", Workspace: db.Workspace{Type: "root"}, AfterTaskID: &a.ID}); !errors.As(err, &conflict) {
		t.Fatalf("create with taken predecessor: %v", err)
	}
	// Cycle: a after b (b already runs after a).
	if _, err := s.UpdateTask(ctx, a.ID, db.TaskPatch{AfterTaskID: db.Optional[int64]{Set: true, V: b.ID}}); !errors.As(err, &conflict) {
		t.Fatalf("cycle: %v", err)
	}
	if _, err := s.UpdateTask(ctx, c.ID, db.TaskPatch{AfterTaskID: db.Optional[int64]{Set: true, V: c.ID}}); !errors.As(err, &conflict) {
		t.Fatalf("self: %v", err)
	}
	if _, err := s.UpdateTask(ctx, c.ID, db.TaskPatch{AfterTaskID: db.Optional[int64]{Set: true, V: 9999}}); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("missing predecessor: %v", err)
	}
	// c after b: a → b → c. Sequence is head first from any member.
	if _, err := s.UpdateTask(ctx, c.ID, db.TaskPatch{AfterTaskID: db.Optional[int64]{Set: true, V: b.ID}}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{a.ID, b.ID, c.ID} {
		seq, err := s.Sequence(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprint(titlesOf(seq)); got != "[a b c]" {
			t.Fatalf("sequence from %d: %s", id, got)
		}
	}
	// Only a backlog task can be linked; a settled predecessor is refused.
	_, _ = s.TransitionTask(ctx, db.Transition{TaskID: a.ID, To: db.StateRunning})
	d := mustTask(t, s, ctx, p.ID, "d", nil)
	if _, err := s.UpdateTask(ctx, a.ID, db.TaskPatch{AfterTaskID: db.Optional[int64]{Set: true, V: d.ID}}); !errors.As(err, &conflict) {
		t.Fatalf("link a running task: %v", err)
	}
	e := mustTask(t, s, ctx, p.ID, "e", nil)
	_, _ = s.TransitionTask(ctx, db.Transition{TaskID: d.ID, To: db.StateSettled})
	if _, err := s.UpdateTask(ctx, e.ID, db.TaskPatch{AfterTaskID: db.Optional[int64]{Set: true, V: d.ID}}); !errors.As(err, &conflict) {
		t.Fatalf("settled predecessor: %v", err)
	}
	// A cancelled predecessor can be linked; its follower waits (only settled passes).
	h := mustTask(t, s, ctx, p.ID, "h", nil)
	g := mustTask(t, s, ctx, p.ID, "g", nil)
	_, _ = s.TransitionTask(ctx, db.Transition{TaskID: h.ID, To: db.StateCancelled})
	if _, err := s.UpdateTask(ctx, g.ID, db.TaskPatch{AfterTaskID: db.Optional[int64]{Set: true, V: h.ID}}); err != nil {
		t.Fatalf("cancelled predecessor: %v", err)
	}
	// Ready followers: b once a settles; never g.
	if ids, _ := s.ReadyFollowers(ctx); len(ids) != 0 {
		t.Fatalf("ready before settle %v", ids)
	}
	_, _ = s.TransitionTask(ctx, db.Transition{TaskID: a.ID, To: db.StateSettled})
	if ids, _ := s.ReadyFollowers(ctx); fmt.Sprint(ids) != fmt.Sprint([]int64{b.ID}) {
		t.Fatalf("ready after settle %v", ids)
	}
	// Delete splices: a → b → c, delete b ⇒ a → c.
	if err := s.DeleteTask(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetTask(ctx, c.ID); got.AfterTaskID == nil || *got.AfterTaskID != a.ID {
		t.Fatalf("splice %+v", got)
	}
	// Unlink.
	if got, err := s.UpdateTask(ctx, c.ID, db.TaskPatch{AfterTaskID: db.Optional[int64]{Set: true, Null: true}}); err != nil || got.AfterTaskID != nil {
		t.Fatalf("unlink %+v %v", got, err)
	}
}

func TestStartNowUnlinksWaitingTask(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "proj-1")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	b := mustTask(t, s, ctx, p.ID, "b", &a.ID)
	c := mustTask(t, s, ctx, p.ID, "c", &b.ID)
	begin := func(id int64, unlink bool) {
		t.Helper()
		if res, err := s.TransitionTask(ctx, db.Transition{TaskID: id, From: []db.TaskState{db.StateBacklog}, To: db.StateLaunching,
			BeginAttempt: true, UnlinkWaiting: unlink}); err != nil || !res.Changed {
			t.Fatalf("begin %d: %+v %v", id, res, err)
		}
	}
	begin(b.ID, true) // a is unfinished: b is unlinked; c stays after b
	if got, _ := s.GetTask(ctx, b.ID); got.AfterTaskID != nil {
		t.Fatalf("b still linked %+v", got)
	}
	if got, _ := s.GetTask(ctx, c.ID); got.AfterTaskID == nil || *got.AfterTaskID != b.ID {
		t.Fatalf("c lost its link %+v", got)
	}
	// A passed predecessor keeps the link (history), even on a manual start.
	_, _ = s.TransitionTask(ctx, db.Transition{TaskID: b.ID, To: db.StateSettled})
	begin(c.ID, true)
	if got, _ := s.GetTask(ctx, c.ID); got.AfterTaskID == nil {
		t.Fatalf("c unlinked from settled predecessor %+v", got)
	}
	// A cancelled predecessor is unfinished: Start now unlinks.
	x := mustTask(t, s, ctx, p.ID, "x", nil)
	y := mustTask(t, s, ctx, p.ID, "y", &x.ID)
	_, _ = s.TransitionTask(ctx, db.Transition{TaskID: x.ID, To: db.StateCancelled})
	begin(y.ID, true)
	if got, _ := s.GetTask(ctx, y.ID); got.AfterTaskID != nil {
		t.Fatalf("y still linked to cancelled predecessor %+v", got)
	}
}

func titlesOf(ts []db.Task) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Title
	}
	return out
}

func TestSweepStagedAttachments(t *testing.T) {
	s, ctx := setup(t)
	old, err := s.CreateAttachment(ctx, "old.png", "image/png", []byte{1})
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := s.CreateAttachment(ctx, "fresh.png", "image/png", []byte{2})
	if err != nil {
		t.Fatal(err)
	}
	attachedOld, err := s.CreateAttachment(ctx, "attached.png", "image/png", []byte{3})
	if err != nil {
		t.Fatal(err)
	}
	p := mustProject(t, s, ctx, "p-sweep").ID
	if _, err := s.CreateTask(ctx, db.NewTask{ProjectID: p, Title: "t", Prompt: "p", RuntimeMode: "full-access", InteractionMode: "default",
		Workspace: db.Workspace{Type: "root"}, AttachmentIDs: []int64{attachedOld.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := testDB.Pool.Exec(ctx, `UPDATE task_attachments SET created_at = now() - interval '25 hours' WHERE id = ANY($1)`,
		[]int64{old.ID, attachedOld.ID}); err != nil {
		t.Fatal(err)
	}
	n, err := s.SweepStagedAttachments(ctx, 24*time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("swept %d, %v; want 1", n, err)
	}
	for id, want := range map[int64]bool{old.ID: false, fresh.ID: true, attachedOld.ID: true} {
		_, err := s.GetAttachment(ctx, id)
		if (err == nil) != want {
			t.Fatalf("attachment %d exists=%v, want %v", id, err == nil, want)
		}
	}
}

func TestSnippetsMigration(t *testing.T) {
	old := dbtest.OpenAt("stage_test_migrate_snippets", 9)
	defer old.Close()
	if old.Pool == nil {
		t.Skip(old.Skip)
	}
	pool := old.Pool
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
INSERT INTO templates (name, prompt, model, interaction_mode) VALUES
  ('keep', 'review the diff', 'm1', 'plan'),
  ('preset-only', '', 'm2', 'default')`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	got, err := db.NewStore(pool).ListSnippets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "keep" || got[0].Body != "review the diff" || got[0].ProjectID != nil {
		t.Fatalf("migrated %+v", got)
	}
}

func TestLaunchRequestIDMigration(t *testing.T) {
	old := dbtest.OpenAt("stage_test_migrate_launch_request", 10)
	defer old.Close()
	if old.Pool == nil {
		t.Skip(old.Skip)
	}
	pool := old.Pool
	ctx := context.Background()
	var projectID, launched, reused, fresh int64
	if err := pool.QueryRow(ctx, `INSERT INTO projects (t3_project_id, title, workspace_root, source) VALUES ('tp', 'p', '/w/p', 'app') RETURNING id`).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO tasks (project_id, title, prompt, launch_attempt, launch_reuse) VALUES ($1, 't', 'p', $2, $3) RETURNING id`
	for _, c := range []struct {
		id      *int64
		attempt int
		reuse   bool
	}{{&launched, 2, false}, {&reused, 1, true}, {&fresh, 0, false}} {
		if err := pool.QueryRow(ctx, insert, projectID, c.attempt, c.reuse).Scan(c.id); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	s := db.NewStore(pool)
	// A pending reuse keeps the pre-migration clientRequestId (T3 may hold
	// a thread for it).
	res, err := s.TransitionTask(ctx, db.Transition{TaskID: reused, To: db.StateLaunching, BeginAttempt: true})
	if err != nil || res.Attempt != 1 || res.RequestID != fmt.Sprintf("task-%d-1", reused) {
		t.Fatalf("reused %+v, %v", res, err)
	}
	// Otherwise a new attempt gets a fresh task-<id>-<uuid>.
	for id, attempt := range map[int64]int{launched: 3, fresh: 1} {
		res, err := s.TransitionTask(ctx, db.Transition{TaskID: id, To: db.StateLaunching, BeginAttempt: true})
		prefix := fmt.Sprintf("task-%d-", id)
		if err != nil || res.Attempt != attempt || !strings.HasPrefix(res.RequestID, prefix) || len(res.RequestID) != len(prefix)+36 {
			t.Fatalf("task %d %+v, %v", id, res, err)
		}
	}
}
