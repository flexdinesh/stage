package storetest

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func init() {
	register("TaskAndScheduleWorkspaceChoices", caseTaskAndScheduleWorkspaceChoices)
}

func caseTaskAndScheduleWorkspaceChoices(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	const connection = dbtest.T3Connection
	primary := execution.WorkspaceInfo{ID: "primary", WorkspaceRoot: "/w/primary"}
	pathless := execution.WorkspaceInfo{ID: "pathless"}
	if _, err := s.RefreshWorkspaces(ctx, connection, []execution.WorkspaceInfo{primary, pathless}); err != nil {
		t.Fatal(err)
	}
	p, err := s.InsertProject(ctx, db.Project{Title: "Two workspaces", Bindings: []db.ProjectBinding{
		{ConnectionID: connection, ExternalProjectID: primary.ID, Workspace: primary.WorkspaceRoot},
		{ConnectionID: connection, ExternalProjectID: pathless.ID},
	}})
	if err != nil {
		t.Fatal(err)
	}
	primaryAt := slices.IndexFunc(p.Bindings, func(b db.ProjectBinding) bool { return b.ExternalProjectID == primary.ID })
	chosenAt := slices.IndexFunc(p.Bindings, func(b db.ProjectBinding) bool { return b.ExternalProjectID == pathless.ID })
	if primaryAt < 0 || chosenAt < 0 {
		t.Fatalf("project bindings %+v", p.Bindings)
	}
	primaryID, chosenID := p.Bindings[primaryAt].ID, p.Bindings[chosenAt].ID
	wantPrimary := store.TaskWorkspace{BindingID: primaryID, Path: primary.WorkspaceRoot, Source: "primary", ExternalID: primary.ID}
	wantChosen := store.TaskWorkspace{BindingID: chosenID, Source: "chosen", ExternalID: pathless.ID}

	other := mustProject(t, s, ctx, "other")
	newTask := db.NewTask{ProjectID: p.ID, ConnectionID: connection, Title: "Task", Prompt: "p"}
	newSchedule := db.NewSchedule{ProjectID: p.ID, ConnectionID: connection, Title: "Schedule", Prompt: "p", Cron: "*/5 * * * *", Tz: "UTC"}
	newTask.BindingID, newSchedule.BindingID = &other.Bindings[0].ID, &other.Bindings[0].ID
	if _, err := s.CreateTask(ctx, newTask); !errors.Is(err, db.ErrInvalidBinding) {
		t.Fatalf("foreign task binding: %v", err)
	}
	if _, err := s.CreateSchedule(ctx, newSchedule); !errors.Is(err, db.ErrInvalidBinding) {
		t.Fatalf("foreign schedule binding: %v", err)
	}
	newTask.BindingID, newSchedule.BindingID = &chosenID, &chosenID

	task, err := s.CreateTask(ctx, newTask)
	if err != nil || task.BindingID == nil || *task.BindingID != chosenID || task.Workspace == nil || *task.Workspace != wantChosen {
		t.Fatalf("chosen task workspace %+v, %v", task, err)
	}
	dup, err := s.DuplicateTask(ctx, task.ID)
	if err != nil || dup.BindingID == nil || *dup.BindingID != chosenID {
		t.Fatalf("duplicate task binding %+v, %v", dup, err)
	}
	sc, err := s.CreateSchedule(ctx, newSchedule)
	if err != nil || sc.Workspace == nil || *sc.Workspace != wantChosen || sc.NextRunAt == nil {
		t.Fatalf("schedule workspace %+v, %v", sc, err)
	}
	fire, err := s.FireSchedule(ctx, sc.ID, *sc.NextRunAt, time.Minute)
	if err != nil || fire.TaskID == nil {
		t.Fatalf("schedule fire %+v, %v", fire, err)
	}
	spawned, err := s.GetTask(ctx, *fire.TaskID)
	if err != nil || spawned.BindingID == nil || *spawned.BindingID != chosenID {
		t.Fatalf("spawned task binding %+v, %v", spawned, err)
	}

	start := func(id int64) error {
		_, err := s.TransitionTask(ctx, db.Transition{TaskID: id, From: []db.TaskState{db.StateBacklog}, To: db.StateLaunching, BeginAttempt: true})
		return err
	}
	if err := start(task.ID); err != nil {
		t.Fatal(err)
	}
	dest, err := s.LaunchDestination(ctx, task.ID, 1)
	if err != nil || dest.BindingID != chosenID || dest.ExternalProjectID != pathless.ID || dest.Workspace != "" {
		t.Fatalf("pathless launch destination %+v, %v", dest, err)
	}
	if _, err := s.RefreshWorkspaces(ctx, connection, []execution.WorkspaceInfo{primary}); err != nil {
		t.Fatal(err)
	}
	pinned, err := s.GetTask(ctx, task.ID)
	wantPinned := store.TaskWorkspace{BindingID: chosenID, Source: "pinned", ExternalID: pathless.ID}
	if err != nil || pinned.Workspace == nil || *pinned.Workspace != wantPinned {
		t.Fatalf("pinned task workspace %+v, %v", pinned.Workspace, err)
	}
	blocked, err := s.CreateTask(ctx, newTask)
	if err != nil || blocked.BindingID == nil || *blocked.BindingID != chosenID || blocked.Workspace != nil {
		t.Fatalf("missing choice %+v, %v", blocked, err)
	}
	var conflict *db.ConflictError
	if err := start(blocked.ID); !errors.As(err, &conflict) || conflict.Code != "binding_unavailable" {
		t.Fatalf("missing chosen workspace launch: %v", err)
	}
	clear := db.Optional[int64]{Set: true, Null: true}
	blocked, err = s.UpdateTask(ctx, blocked.ID, db.TaskPatch{BindingID: clear})
	if err != nil || blocked.BindingID != nil || blocked.Workspace == nil || *blocked.Workspace != wantPrimary {
		t.Fatalf("task after clearing choice %+v, %v", blocked, err)
	}
	sc, err = s.UpdateSchedule(ctx, sc.ID, db.SchedulePatch{ProjectID: db.Optional[int64]{Set: true, V: other.ID}})
	if err != nil || sc.BindingID != nil {
		t.Fatalf("schedule after changing project %+v, %v", sc, err)
	}

	p, err = s.UnbindProject(ctx, p.ID, chosenID)
	if err != nil || slices.ContainsFunc(p.Bindings, func(b db.ProjectBinding) bool { return b.ID == chosenID }) {
		t.Fatalf("unbound workspace still present: %+v, %v", p.Bindings, err)
	}
	pinned, err = s.GetTask(ctx, task.ID)
	if err != nil || pinned.Workspace == nil || *pinned.Workspace != wantPinned {
		t.Fatalf("pinned task after unbinding: %+v, %v", pinned.Workspace, err)
	}
}
