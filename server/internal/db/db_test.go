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

func mustTask(t *testing.T, s *db.Store, ctx context.Context, projectID int64, title string, chainID *int64) db.Task {
	t.Helper()
	task, err := s.CreateTask(ctx, db.NewTask{
		ProjectID: projectID, Title: title, Prompt: "p", RuntimeMode: "full-access",
		InteractionMode: "default", Workspace: db.Workspace{Type: "root"}, ChainID: chainID,
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

	res, err := s.TransitionTask(ctx, db.Transition{TaskID: task.ID, From: []db.TaskState{db.StateRunning}, To: db.StateCompleted})
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
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateCompleted}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetTask(ctx, task.ID)
	if got.FinishedAt == nil || got.SettledAt != nil || got.LaunchedAt == nil || got.T3ThreadID == nil || *got.T3ThreadID != thread {
		t.Fatalf("after completed: %+v", got)
	}
	finished := *got.FinishedAt
	// needs_input then completed again: finishedAt keeps first entry.
	_, _ = s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateNeedsInput})
	_, _ = s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateCompleted})
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
	want := []db.TaskState{db.StateBacklog, db.StateLaunching, db.StateRunning, db.StateCompleted, db.StateNeedsInput, db.StateCompleted, db.StateSettled}
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

func TestChainSteps(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "proj-1")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	b := mustTask(t, s, ctx, p.ID, "b", nil)
	c := mustTask(t, s, ctx, p.ID, "c", nil)

	chain, err := s.CreateChain(ctx, "ch", []int64{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	if chain.State != db.ChainIdle || len(chain.Steps) != 2 || chain.Steps[0].ID != a.ID {
		t.Fatalf("chain %+v", chain)
	}
	var conflict *db.ConflictError
	if _, err := s.CreateChain(ctx, "other", []int64{a.ID}); !errors.As(err, &conflict) {
		t.Fatalf("task in another chain: %v", err)
	}
	// Appending via CreateTask lands last.
	d := mustTask(t, s, ctx, p.ID, "d", &chain.ID)
	if d.ChainPosition == nil || *d.ChainPosition != 3 {
		t.Fatalf("appended position %v", d.ChainPosition)
	}
	// Reorder + add c + remove d.
	chain, err = s.UpdateChain(ctx, chain.ID, nil, []int64{b.ID, c.ID, a.ID}, true)
	if err != nil {
		t.Fatal(err)
	}
	if ids := stepIDs(chain); fmt.Sprint(ids) != fmt.Sprint([]int64{b.ID, c.ID, a.ID}) {
		t.Fatalf("steps %v", ids)
	}
	if got, _ := s.GetTask(ctx, d.ID); got.ChainID != nil {
		t.Fatal("removed step still in chain")
	}
	// Non-backlog steps can't be removed or reordered relative to each other.
	_, _ = s.TransitionTask(ctx, db.Transition{TaskID: b.ID, To: db.StateSettled})
	_, _ = s.TransitionTask(ctx, db.Transition{TaskID: c.ID, To: db.StateRunning})
	if _, err := s.UpdateChain(ctx, chain.ID, nil, []int64{c.ID, a.ID}, true); !errors.As(err, &conflict) {
		t.Fatalf("remove settled step: %v", err)
	}
	if _, err := s.UpdateChain(ctx, chain.ID, nil, []int64{c.ID, b.ID, a.ID}, true); !errors.As(err, &conflict) {
		t.Fatalf("reorder locked steps: %v", err)
	}
	// A backlog step must not precede a launched one (it would launch in
	// parallel with c, or bypass c if c failed).
	if _, err := s.UpdateChain(ctx, chain.ID, nil, []int64{b.ID, a.ID, c.ID}, true); !errors.As(err, &conflict) {
		t.Fatalf("backlog step ahead of running step: %v", err)
	}
	if _, err := s.UpdateChain(ctx, chain.ID, nil, []int64{a.ID, b.ID, c.ID}, true); !errors.As(err, &conflict) {
		t.Fatalf("backlog step ahead of settled step: %v", err)
	}
	if _, err := s.UpdateChain(ctx, chain.ID, nil, []int64{b.ID, c.ID, a.ID}, true); err != nil {
		t.Fatalf("locked steps first: %v", err)
	}
	var invalid *db.InvalidError
	if _, err := s.UpdateChain(ctx, chain.ID, nil, []int64{b.ID, b.ID}, true); !errors.As(err, &invalid) {
		t.Fatalf("duplicate step: %v", err)
	}
	// Adding a non-backlog task is a conflict.
	if _, err := s.UpdateChain(ctx, chain.ID, nil, []int64{b.ID, c.ID, d.ID, a.ID}, true); err != nil {
		t.Fatal(err)
	}
	e := mustTask(t, s, ctx, p.ID, "e", nil)
	_, _ = s.TransitionTask(ctx, db.Transition{TaskID: e.ID, To: db.StateFailed})
	if _, err := s.UpdateChain(ctx, chain.ID, nil, []int64{b.ID, c.ID, d.ID, a.ID, e.ID}, true); !errors.As(err, &conflict) {
		t.Fatalf("add failed task: %v", err)
	}

	changed, cur, err := s.SetChainState(ctx, chain.ID, []db.ChainState{db.ChainRunning}, db.ChainPaused)
	if err != nil || changed || cur != db.ChainIdle {
		t.Fatalf("guarded chain state: %v %v %v", changed, cur, err)
	}
	if _, _, err := s.SetChainState(ctx, 9999, []db.ChainState{db.ChainIdle}, db.ChainRunning); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("missing chain: %v", err)
	}
}

func TestCompleteChain(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "proj-1")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	b := mustTask(t, s, ctx, p.ID, "b", nil)
	chain, err := s.CreateChain(ctx, "ch", []int64{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.TransitionTask(ctx, db.Transition{TaskID: a.ID, To: db.StateSettled})
	_, _ = s.TransitionTask(ctx, db.Transition{TaskID: b.ID, To: db.StateCancelled})
	if done, err := s.CompleteChain(ctx, chain.ID); err != nil || done {
		t.Fatalf("idle chain must not complete: %v %v", done, err)
	}
	if _, _, err := s.SetChainState(ctx, chain.ID, []db.ChainState{db.ChainIdle}, db.ChainRunning); err != nil {
		t.Fatal(err)
	}
	// A step added after the runner decided "done" keeps the chain open.
	mustTask(t, s, ctx, p.ID, "late", &chain.ID)
	if done, err := s.CompleteChain(ctx, chain.ID); err != nil || done {
		t.Fatalf("chain with a backlog step must not complete: %v %v", done, err)
	}
	if got, _ := s.GetChain(ctx, chain.ID); got.State != db.ChainRunning {
		t.Fatalf("state %s", got.State)
	}
}

func TestTransitionLaunchGuards(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "proj-1")
	step := mustTask(t, s, ctx, p.ID, "step", nil)
	chain, err := s.CreateChain(ctx, "ch", []int64{step.ID})
	if err != nil {
		t.Fatal(err)
	}
	begin := db.Transition{TaskID: step.ID, From: []db.TaskState{db.StateBacklog, db.StateFailed}, To: db.StateLaunching, BeginAttempt: true, StandaloneOnly: true}
	res, err := s.TransitionTask(ctx, begin)
	if err != nil || res.Changed || res.ChainID == nil || *res.ChainID != chain.ID {
		t.Fatalf("standalone start of backlog chain step must not apply: %+v %v", res, err)
	}
	begin.StandaloneOnly = false // chain runner path
	if res, err = s.TransitionTask(ctx, begin); err != nil || !res.Changed || res.Attempt != 1 {
		t.Fatalf("runner launch: %+v %v", res, err)
	}
	// Transport failure: outcome unknown, next start keeps attempt 1.
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: step.ID, To: db.StateFailed, ReuseAttempt: true}); err != nil {
		t.Fatal(err)
	}
	begin.StandaloneOnly = true // a failed chain step may be restarted manually
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
			_, _ = s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateCompleted})
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

func stepIDs(c db.Chain) []int64 {
	out := make([]int64, len(c.Steps))
	for i, s := range c.Steps {
		out[i] = s.ID
	}
	return out
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
