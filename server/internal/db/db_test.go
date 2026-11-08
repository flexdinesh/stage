package db_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/flexdinesh/t3-stage/server/internal/db"
	"github.com/flexdinesh/t3-stage/server/internal/db/dbtest"
	"github.com/flexdinesh/t3-stage/server/internal/t3"
)

var testDB *dbtest.DB

func TestMain(m *testing.M) {
	testDB = dbtest.Open("t3stage_test_db")
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

func TestUpsertT3ProjectsKeepsLocalTitle(t *testing.T) {
	s, ctx := setup(t)
	n, err := s.UpsertT3Projects(ctx, []t3.Project{
		{ID: "p1", Title: "One", WorkspaceRoot: "/one"},
		{ID: "p2", Title: "Gone", WorkspaceRoot: "/gone", DeletedAt: &time.Time{}},
	})
	if err != nil || n != 1 {
		t.Fatalf("upsert %d %v", n, err)
	}
	p, err := s.ProjectByWorkspaceRoot(ctx, "/one")
	if err != nil || p.Source != db.SourceT3 {
		t.Fatalf("project %+v %v", p, err)
	}
	title := "Local"
	_, _ = s.UpdateProject(ctx, p.ID, &title, nil)
	_, _ = s.UpsertT3Projects(ctx, []t3.Project{{ID: "p1", Title: "One", WorkspaceRoot: "/one-moved"}})
	p, _ = s.GetProject(ctx, p.ID)
	if p.Title != "Local" || p.WorkspaceRoot != "/one-moved" {
		t.Fatalf("after resync %+v", p)
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
	// Only a backlog task can be linked; a settled/cancelled predecessor is refused.
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
	// Ready followers: b once a settles.
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
