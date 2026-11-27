package storetest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/t3"
)

func init() {
	register("TaskOriginSessionFilter", caseTaskOriginSessionFilter)
	register("TransitionTask", caseTransitionTask)
	register("UpdateTaskRestrictions", caseUpdateTaskRestrictions)
	register("TransitionLaunchGuards", caseTransitionLaunchGuards)
	register("ProjectLastActivity", caseProjectLastActivity)
}

// session is a session on the seeded T3 connection (dbtest.T3Connection).
func session(external string) db.SessionRef {
	return db.SessionRef{ConnectionID: dbtest.T3Connection, ExternalSessionID: external}
}

func mustProject(t *testing.T, s *db.Store, ctx context.Context, t3id string) db.Project {
	t.Helper()
	current, err := s.Workspaces(ctx, dbtest.T3Connection)
	if err != nil {
		t.Fatal(err)
	}
	inventory := []execution.WorkspaceInfo{{ID: t3id, WorkspaceRoot: "/w/" + t3id}}
	for _, workspace := range current {
		inventory = append(inventory, execution.WorkspaceInfo{ID: workspace.ExternalID, WorkspaceRoot: workspace.Workspace})
	}
	if _, err := s.RefreshWorkspaces(ctx, dbtest.T3Connection, inventory); err != nil {
		t.Fatal(err)
	}
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

func caseTaskOriginSessionFilter(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "proj-1")
	add := func(title, origin string, needs ...int64) db.Task {
		t.Helper()
		task, err := s.CreateTask(ctx, db.NewTask{ConnectionID: dbtest.T3Connection,
			ProjectID: p.ID, Title: title, Prompt: "p", OriginSession: ptr(session(origin)), Needs: needs,
		})
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	a := add("a", "plan")
	b := add("b", "plan", a.ID)
	c := add("c", "plan")
	dependent := mustTask(t, s, ctx, p.ID, "owner", []int64{b.ID})
	add("other", "elsewhere")

	got, err := s.ListTasks(ctx, db.TaskFilter{OriginSession: ptr(session("plan"))})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, len(got))
	for _, task := range got {
		ids = append(ids, task.ID)
		if task.ID == b.ID && !slices.Equal(task.Dependents, []int64{dependent.ID}) {
			t.Fatalf("task %d dependents: %v", task.ID, task.Dependents)
		}
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []int64{a.ID, b.ID, c.ID}) {
		t.Fatalf("tasks created by plan: %v", ids)
	}
	none, err := s.ListTasks(ctx, db.TaskFilter{OriginSession: ptr(session("nobody"))})
	if err != nil || len(none) != 0 {
		t.Fatalf("tasks created by nobody: %+v, %v", none, err)
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
	thread := "session-1"
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateRunning, Session: &db.SessionRef{ConnectionID: s.ActiveID(db.LocalMachine, "t3"), ExternalSessionID: thread}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateDone}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetTask(ctx, task.ID)
	if got.FinishedAt == nil || got.LaunchedAt == nil || got.ExternalSessionID == nil || *got.ExternalSessionID != thread {
		t.Fatalf("after done: %+v", got)
	}
	finished := *got.FinishedAt
	// needs_input then done again: finishedAt keeps first entry.
	_, _ = s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateNeedsInput})
	_, _ = s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateDone})
	got, _ = s.GetTask(ctx, task.ID)
	if !got.FinishedAt.Equal(finished) || got.State != db.StateDone {
		t.Fatalf("after done again: %+v", got)
	}
	// Same-state transition is a no-op.
	res, _ = s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateDone})
	if res.Changed {
		t.Fatal("same-state transition must not apply")
	}
	events, _ := s.TaskEvents(ctx, task.ID)
	want := []db.TaskState{db.StateBacklog, db.StateLaunching, db.StateRunning, db.StateDone, db.StateNeedsInput, db.StateDone}
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

	// Relaunch resets session and timestamps.
	res, _ = s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateLaunching, BeginAttempt: true})
	got, _ = s.GetTask(ctx, task.ID)
	if res.Attempt != 2 || got.ExternalSessionID != nil || got.FinishedAt != nil {
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

func caseProjectLastActivity(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "proj-1")
	if p.LastActivityAt != nil {
		t.Fatalf("new project activity %v", p.LastActivityAt)
	}
	// Observed sessions are not project activity: only Stage's tasks and
	// journal are.
	created := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	detail := t3.ThreadDetail{Thread: t3.Thread{ThreadID: "th-1", ProjectID: "proj-1", Status: t3.StatusCompleted, CreatedAt: created, UpdatedAt: created.Add(time.Hour)}}
	if err := s.PutObservation(ctx, detail.Observation()); err != nil {
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
