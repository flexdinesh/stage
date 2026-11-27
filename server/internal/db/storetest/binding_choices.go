package storetest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/schedule"
	"github.com/flexdinesh/stage/server/internal/t3"
)

func init() {
	register("TaskAndScheduleWorkspaceChoices", caseTaskAndScheduleWorkspaceChoices)
	register("ConnectionReplacementWorkspaceChoices", caseConnectionReplacementWorkspaceChoices)
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

type scheduledLaunchFunc func(context.Context, int64, string) (db.Task, error)

func (f scheduledLaunchFunc) LaunchScheduled(ctx context.Context, id int64, detail string) (db.Task, error) {
	return f(ctx, id, detail)
}

func caseConnectionReplacementWorkspaceChoices(t *testing.T, open Factory) {
	for _, tc := range []struct {
		name        string
		pinTask     bool
		pinSchedule bool
		forget      bool
	}{
		{name: "pinned", pinTask: true, pinSchedule: true},
		{name: "forgotten_task_pin", pinTask: true, forget: true},
		{name: "forgotten_schedule_pin", pinSchedule: true, forget: true},
		{name: "unpinned"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, ctx := setup(t, open)
			old, err := s.SwitchInstance(ctx, db.LocalMachine, t3.Descriptor, "env-a", "", 0)
			if err != nil {
				t.Fatal(err)
			}
			p, err := s.InsertProject(ctx, db.Project{Title: "Project", Bindings: []db.ProjectBinding{
				{ConnectionID: old.New, ExternalProjectID: "chosen", Workspace: "/old/chosen"},
			}})
			if err != nil {
				t.Fatal(err)
			}
			chosen := p.Bindings[0].ID
			var taskPin, schedulePin *int64
			if tc.pinTask {
				taskPin = &chosen
			}
			if tc.pinSchedule {
				schedulePin = &chosen
			}
			manual, err := s.CreateTask(ctx, db.NewTask{
				ProjectID: p.ID, ConnectionID: old.New, BindingID: taskPin, Title: "Manual", Prompt: "p",
			})
			if err != nil {
				t.Fatal(err)
			}
			sc, err := s.CreateSchedule(ctx, db.NewSchedule{
				ProjectID: p.ID, ConnectionID: old.New, BindingID: schedulePin,
				Title: "Schedule", Prompt: "p", Cron: "*/5 * * * *", Tz: "UTC",
			})
			if err != nil || sc.NextRunAt == nil {
				t.Fatalf("create schedule: %+v, %v", sc, err)
			}
			sw, err := s.SwitchInstance(ctx, db.LocalMachine, t3.Descriptor, "env-b", "", 0)
			if err != nil || !sw.Changed || sw.Old != old.New || sw.New == old.New {
				t.Fatalf("replace connection: %+v, %v", sw, err)
			}
			p, err = s.BindProject(ctx, db.ProjectBinding{ProjectID: p.ID, ConnectionID: sw.New, ExternalProjectID: "primary", Workspace: "/new/primary"})
			if err != nil {
				t.Fatal(err)
			}
			newPrimary := slices.IndexFunc(p.Bindings, func(b db.ProjectBinding) bool { return b.ConnectionID == sw.New && b.Primary })
			if newPrimary < 0 {
				t.Fatalf("replacement primary missing: %+v", p.Bindings)
			}
			primary := p.Bindings[newPrimary]
			if tc.forget {
				if err := s.ForgetConnection(ctx, sw.Old); err != nil {
					t.Fatal(err)
				}
			}

			start := scheduledLaunchFunc(func(ctx context.Context, id int64, detail string) (db.Task, error) {
				_, err := s.TransitionTask(ctx, db.Transition{
					TaskID: id, From: []db.TaskState{db.StateBacklog}, To: db.StateLaunching, Detail: detail, BeginAttempt: true,
				})
				if err != nil {
					return db.Task{}, err
				}
				return s.GetTask(ctx, id)
			})
			assertDestination := func(t *testing.T, id int64) {
				t.Helper()
				dest, err := s.LaunchDestination(ctx, id, 1)
				if err != nil || dest.ConnectionID != sw.New || dest.BindingID != primary.ID || dest.Workspace != primary.Workspace {
					t.Fatalf("launch destination: %+v, %v", dest, err)
				}
			}

			t.Run("manual", func(t *testing.T) {
				started, err := start(ctx, manual.ID, "manual start")
				if tc.pinTask {
					assertProjectConflict(t, err, "binding_unavailable")
					blocked, getErr := s.GetTask(ctx, manual.ID)
					if getErr != nil || blocked.ConnectionID != sw.New || blocked.BindingID == nil || *blocked.BindingID != chosen ||
						blocked.Workspace != nil || blocked.State != db.StateBacklog || blocked.LaunchAttempt != 0 {
						t.Fatalf("blocked manual task: %+v, %v", blocked, getErr)
					}
					if _, err := s.UpdateTask(ctx, manual.ID, db.TaskPatch{BindingID: db.Optional[int64]{Set: true, V: primary.ID}}); err != nil {
						t.Fatal(err)
					}
					started, err = start(ctx, manual.ID, "manual start after choosing a workspace")
				}
				if err != nil {
					t.Fatal(err)
				}
				if !tc.pinTask && started.BindingID != nil {
					t.Fatalf("unpinned task got binding %d", *started.BindingID)
				}
				assertDestination(t, manual.ID)
			})
			t.Run("due schedule", func(t *testing.T) {
				runner := schedule.NewRunner(s, start, nil)
				runner.ConnectionID = sw.New
				runner.Now = func() time.Time { return sc.NextRunAt.Add(time.Second) }
				if err := runner.Reconcile(ctx); err != nil {
					t.Fatal(err)
				}
				saved, err := s.GetSchedule(ctx, sc.ID)
				if err != nil || saved.ConnectionID != sw.New || saved.LastTaskID == nil {
					t.Fatalf("schedule after firing: %+v, %v", saved, err)
				}
				id := *saved.LastTaskID
				spawned, err := s.GetTask(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				if !tc.pinSchedule {
					if saved.BindingID != nil || spawned.BindingID != nil {
						t.Fatalf("unpinned schedule or task got a binding: %+v, %+v", saved, spawned)
					}
					assertDestination(t, id)
					return
				}
				if saved.BindingID == nil || *saved.BindingID != chosen || saved.Workspace != nil ||
					spawned.BindingID == nil || *spawned.BindingID != chosen || spawned.Workspace != nil ||
					spawned.State != db.StateBacklog || spawned.LaunchAttempt != 0 || spawned.StartAt != nil || spawned.LastError == nil {
					t.Fatalf("occurrence was not held: schedule %+v, task %+v", saved, spawned)
				}
				repaired, err := s.UpdateSchedule(ctx, sc.ID, db.SchedulePatch{BindingID: db.Optional[int64]{Set: true, Null: true}})
				if err != nil || repaired.BindingID != nil || repaired.Workspace == nil || repaired.Workspace.BindingID != primary.ID {
					t.Fatalf("schedule after choosing primary: %+v, %v", repaired, err)
				}
			})
		})
	}
}
