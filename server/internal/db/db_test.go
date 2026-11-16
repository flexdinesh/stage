package db_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/t3"
)

var testDB dbtest.DB

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

func mustTask(t *testing.T, s *db.Store, ctx context.Context, projectID int64, title string, needs []int64) db.Task {
	t.Helper()
	task, err := s.CreateTask(ctx, db.NewTask{
		ProjectID: projectID, Title: title, Prompt: "p", RuntimeMode: "full-access",
		InteractionMode: "default", Workspace: db.Workspace{Type: "root"}, Needs: needs,
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
	if _, err := pool.ExecContext(ctx, `UPDATE tasks SET updated_at = strftime('%Y-%m-%dT%H:%M:%f000Z', 'now', '-2 hours'), finished_at = strftime('%Y-%m-%dT%H:%M:%f000Z', 'now', '-2 hours') WHERE id = $1`, ids["stale"]); err != nil {
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

func TestUpsertThreadPullRequestsAndRuns(t *testing.T) {
	s, ctx := setup(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	pr := func(n int, state string) t3.PullRequest {
		return t3.PullRequest{Host: "github.com", Repository: "o/r", Number: n, URL: fmt.Sprintf("https://github.com/o/r/pull/%d", n), Source: "created", State: state}
	}

	// Unread thread: arrays, never null.
	if err := s.UpsertThreads(ctx, []t3.Thread{{ThreadID: "th-pr", ProjectID: "proj-1", Title: "x", Status: t3.StatusRunning, CreatedAt: now, UpdatedAt: now}}); err != nil {
		t.Fatal(err)
	}
	th, err := s.GetThread(ctx, "th-pr")
	if err != nil {
		t.Fatal(err)
	}
	runs, err := s.ThreadRuns(ctx, "th-pr")
	if err != nil {
		t.Fatal(err)
	}
	if th.PullRequests == nil || len(th.PullRequests) != 0 || runs == nil || len(runs) != 0 || th.RunCount != 0 {
		t.Fatalf("unread thread prs=%#v runs=%#v count=%d", th.PullRequests, runs, th.RunCount)
	}

	// Detail read stores PRs, runs (newest first, capped at 50) and the count.
	started := now.Add(-3 * time.Minute)
	recent := make([]t3.Run, 0, 52)
	for i := 52; i >= 1; i-- {
		recent = append(recent, t3.Run{RunID: fmt.Sprintf("r%d", i), Ordinal: i, Status: t3.StatusCompleted, Model: "m", RequestedAt: started, StartedAt: &started, CompletedAt: &now})
	}
	recent[0].Status = t3.StatusRunning
	recent[0].ExecutedModel = "m-exec"
	recent[0].CompletedAt = nil
	d := t3.ThreadDetail{
		Thread:     t3.Thread{ThreadID: "th-pr", ProjectID: "proj-1", Title: "x", Status: t3.StatusRunning, CreatedAt: now, UpdatedAt: now, PullRequests: []t3.PullRequest{pr(1, "open")}},
		RunCount:   60,
		RecentRuns: recent,
	}
	if err := s.UpsertThreadDetail(ctx, d); err != nil {
		t.Fatal(err)
	}
	// A later list upsert refreshes PRs and keeps runs.
	d.Thread.PullRequests = []t3.PullRequest{pr(1, "merged"), pr(2, "open")}
	if err := s.UpsertThreads(ctx, []t3.Thread{d.Thread}); err != nil {
		t.Fatal(err)
	}
	th, err = s.GetThread(ctx, "th-pr")
	if err != nil {
		t.Fatal(err)
	}
	if len(th.PullRequests) != 2 || th.PullRequests[0] != pr(1, "merged") || th.PullRequests[1] != pr(2, "open") || th.RunCount != 60 {
		t.Fatalf("thread prs=%#v count=%d", th.PullRequests, th.RunCount)
	}
	runs, err = s.ThreadRuns(ctx, "th-pr")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 50 || runs[0].Ordinal != 52 || runs[49].Ordinal != 3 {
		t.Fatalf("runs len=%d first=%+v", len(runs), runs[0])
	}
	r := runs[0]
	if r.Status != "running" || r.Model == nil || *r.Model != "m" || r.ExecutedModel == nil || *r.ExecutedModel != "m-exec" ||
		r.StartedAt == nil || !r.StartedAt.Equal(started) || r.CompletedAt != nil || !r.RequestedAt.Equal(started) {
		t.Fatalf("run %+v", r)
	}
	if runs[1].ExecutedModel != nil {
		t.Fatalf("unreported executed model = %v", *runs[1].ExecutedModel)
	}
	if _, err := s.ThreadRuns(ctx, "nope"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("unknown thread err = %v", err)
	}
}

func TestTaskCarriesThreadPullRequests(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "proj-prs")
	task := mustTask(t, s, ctx, p.ID, "t", nil)
	got, err := s.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PullRequests == nil || len(got.PullRequests) != 0 {
		t.Fatalf("threadless task prs = %#v", got.PullRequests)
	}
	now := time.Now().UTC()
	open := t3.PullRequest{Host: "github.com", Repository: "o/r", Number: 7, URL: "https://github.com/o/r/pull/7", Source: "agent", State: "open"}
	if err := s.UpsertThreads(ctx, []t3.Thread{{ThreadID: "th-task-pr", ProjectID: p.T3ProjectID, Title: "t", Status: t3.StatusRunning, CreatedAt: now, UpdatedAt: now, PullRequests: []t3.PullRequest{open}}}); err != nil {
		t.Fatal(err)
	}
	thread := "th-task-pr"
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateRunning, ThreadID: &thread}); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.PullRequests) != 1 || got.PullRequests[0] != open {
		t.Fatalf("task prs = %#v", got.PullRequests)
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
	if _, err := testDB.Pool.ExecContext(ctx, `UPDATE task_attachments SET created_at = strftime('%Y-%m-%dT%H:%M:%f000Z', 'now', '-25 hours') WHERE id IN ($1, $2)`,
		old.ID, attachedOld.ID); err != nil {
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
