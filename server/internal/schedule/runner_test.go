package schedule_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/schedule"
)

var testDB dbtest.DB

type launch struct {
	ID     int64
	Detail string
}

// fakeLauncher records launches and moves the task to launching.
type fakeLauncher struct {
	store *db.Store
	mu    sync.Mutex
	got   []launch
}

func (f *fakeLauncher) LaunchScheduled(ctx context.Context, id int64, detail string) (db.Task, error) {
	f.mu.Lock()
	f.got = append(f.got, launch{id, detail})
	f.mu.Unlock()
	if _, err := f.store.TransitionTask(ctx, db.Transition{
		TaskID: id, From: []db.TaskState{db.StateBacklog}, To: db.StateLaunching, Detail: detail, BeginAttempt: true,
	}); err != nil {
		return db.Task{}, err
	}
	return f.store.GetTask(ctx, id)
}

func TestReconcileLaunchesDueStarts(t *testing.T) {
	store := db.NewStore(testDB.Reset(t))
	ctx := context.Background()
	p, err := store.InsertProject(ctx, db.Project{Title: "p", Bindings: []db.ProjectBinding{{ConnectionID: 1, ExternalProjectID: "p", Workspace: "/w/p"}}})
	if err != nil {
		t.Fatal(err)
	}
	sc, err := store.CreateSchedule(ctx, db.NewSchedule{
		ProjectID: p.ID, Title: "every5", Prompt: "p", RuntimeMode: "full-access", InteractionMode: "default",
		Workspace: db.Workspace{Type: "root"}, Cron: "*/5 * * * *", Tz: "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := sc.NextRunAt.Add(10 * time.Second)
	tz := "UTC"
	oneShot := func(title string, at time.Time) db.Task {
		task, err := store.CreateTask(ctx, db.NewTask{
			ProjectID: p.ID, Title: title, Prompt: "p", RuntimeMode: "full-access", InteractionMode: "default",
			Workspace: db.Workspace{Type: "root"}, StartAt: &at, StartTz: &tz,
		})
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	late := oneShot("late", now.Add(-12*time.Minute))
	onTime := oneShot("on time", now.Add(-10*time.Second))
	future := oneShot("future", now.Add(time.Minute))

	l := &fakeLauncher{store: store}
	r := schedule.NewRunner(store, l, nil)
	r.Now = func() time.Time { return now }
	if err := r.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSchedule(ctx, sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastTaskID == nil {
		t.Fatalf("schedule did not spawn: %+v", got)
	}
	// Oldest start first; the spawned task (start = fire time, 10s ago) is on time.
	want := []launch{
		{late.ID, "scheduled start, late by 12m"},
		{onTime.ID, "scheduled start"},
		{*got.LastTaskID, "scheduled start"},
	}
	l.mu.Lock()
	launched := slices.Clone(l.got)
	l.mu.Unlock()
	if !slices.Equal(launched, want) {
		t.Fatalf("launches = %+v, want %+v", launched, want)
	}
	if slices.ContainsFunc(launched, func(x launch) bool { return x.ID == future.ID }) {
		t.Fatalf("future task launched: %+v", launched)
	}
	n := len(launched)
	// A second pass launches nothing new (start times cleared, next fire in the future).
	if err := r.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.got) != n {
		t.Fatalf("second pass launched %+v", l.got[n:])
	}
}

func TestUnboundScheduleHoldsOneOccurrenceUntilManualStart(t *testing.T) {
	store := db.NewStore(testDB.Reset(t))
	ctx := context.Background()
	p, err := store.InsertProject(ctx, db.Project{Title: "Offline plan"})
	if err != nil {
		t.Fatal(err)
	}
	sc, err := store.CreateSchedule(ctx, db.NewSchedule{ProjectID: p.ID, Title: "Review", Prompt: "Review", RuntimeMode: "full-access", InteractionMode: "default", Workspace: db.Workspace{Type: "root"}, Cron: "*/5 * * * *", Tz: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	now := sc.NextRunAt.Add(time.Second)
	launcher := &fakeLauncher{store: store}
	runner := schedule.NewRunner(store, launcher, nil)
	runner.Now = func() time.Time { return now }
	if err = runner.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSchedule(ctx, sc.ID)
	if err != nil || got.LastTaskID == nil {
		t.Fatal(got, err)
	}
	task, err := store.GetTask(ctx, *got.LastTaskID)
	if err != nil || task.State != db.StateBacklog || task.StartAt != nil || task.LastError == nil || task.LaunchAttempt != 0 {
		t.Fatalf("not held %+v %v", task, err)
	}
	if _, err = store.BindProject(ctx, db.ProjectBinding{ProjectID: p.ID, ConnectionID: 1, ExternalProjectID: "repaired", Workspace: "/repaired"}); err != nil {
		t.Fatal(err)
	}
	now = got.NextRunAt.Add(time.Second)
	if err = runner.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := store.GetSchedule(ctx, sc.ID)
	if err != nil || after.LastTaskID == nil || *after.LastTaskID != task.ID {
		t.Fatal("spawned duplicate occurrence", after, err)
	}
	task, err = store.GetTask(ctx, task.ID)
	if err != nil || task.State != db.StateBacklog || task.LaunchAttempt != 0 {
		t.Fatal("repair auto-started task", task, err)
	}
	if _, err = launcher.LaunchScheduled(ctx, task.ID, "manual start"); err != nil {
		t.Fatal(err)
	}
}
