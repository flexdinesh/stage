package db_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
)

func mustSchedule(t *testing.T, s *db.Store, ctx context.Context, projectID int64, cron string) db.Schedule {
	t.Helper()
	sc, err := s.CreateSchedule(ctx, db.NewSchedule{
		ProjectID: projectID, Title: "nightly", Prompt: "p", RuntimeMode: "full-access",
		InteractionMode: "default", Workspace: db.Workspace{Type: "root"}, Tags: []string{"deps"},
		Cron: cron, Tz: "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sc.NextRunAt == nil {
		t.Fatal("new schedule has no nextRunAt")
	}
	return sc
}

func TestScheduleNotesValidatedBeforeSaving(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "notes")
	for _, notes := range []string{strings.Repeat("界", 5462), string([]byte{0xff})} {
		_, err := s.CreateSchedule(ctx, db.NewSchedule{ProjectID: p.ID, Title: "invalid", Prompt: "p", Cron: "*/5 * * * *", Tz: "UTC", Notes: notes})
		var invalid *db.InvalidError
		if !errors.As(err, &invalid) {
			t.Fatalf("create invalid template: %v", err)
		}
	}
	sc := mustSchedule(t, s, ctx, p.ID, "*/5 * * * *")
	_, err := s.UpdateSchedule(ctx, sc.ID, db.SchedulePatch{Notes: db.Optional[string]{Set: true, V: strings.Repeat("x", 16385)}})
	var invalid *db.InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("update invalid template: %v", err)
	}
	got, err := s.GetSchedule(ctx, sc.ID)
	if err != nil || got.Notes != "" {
		t.Fatalf("failed update changed template: %+v %v", got, err)
	}
}

func TestWhitespaceScheduleNotesDoNotBlockRuns(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "notes")
	sc := mustSchedule(t, s, ctx, p.ID, "*/5 * * * *")
	if _, err := s.UpdateSchedule(ctx, sc.ID, db.SchedulePatch{Notes: db.Optional[string]{Set: true, V: " \n\t "}}); err != nil {
		t.Fatal(err)
	}
	manual, err := s.RunScheduleNow(ctx, sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	auto := mustFire(t, s, ctx, sc.ID, *sc.NextRunAt)
	if auto.TaskID == nil || auto.Outcome != db.RunStarted {
		t.Fatalf("automatic run: %+v", auto)
	}
	for _, id := range []int64{manual.ID, *auto.TaskID} {
		page, err := s.ReadJournal(ctx, db.JournalOwner{TaskID: &id}, db.JournalQuery{})
		if err != nil || len(page.Entries) != 0 {
			t.Fatalf("whitespace template produced notes: %+v %v", page, err)
		}
	}
	got, err := s.GetSchedule(ctx, sc.ID)
	if err != nil || got.NextRunAt == nil || !got.NextRunAt.After(*sc.NextRunAt) {
		t.Fatalf("automatic run did not advance: %+v %v", got, err)
	}
}

func mustFire(t *testing.T, s *db.Store, ctx context.Context, id int64, now time.Time) db.FireResult {
	t.Helper()
	res, err := s.FireSchedule(ctx, id, now, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func lastRun(t *testing.T, s *db.Store, ctx context.Context, id int64) db.ScheduleRun {
	t.Helper()
	runs, err := s.ScheduleRuns(ctx, id, 1)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs %v %v", runs, err)
	}
	return runs[0]
}

func TestFireSchedule(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "proj")
	sc := mustSchedule(t, s, ctx, p.ID, "*/5 * * * *")
	fire := *sc.NextRunAt

	// Not due yet: nothing happens.
	if res := mustFire(t, s, ctx, sc.ID, fire.Add(-time.Second)); res.Outcome != "" {
		t.Fatalf("early fire = %+v", res)
	}

	// Due within grace: spawns a backlog task starting at the fire time.
	now := fire.Add(30 * time.Second)
	res := mustFire(t, s, ctx, sc.ID, now)
	if res.Outcome != db.RunStarted || res.TaskID == nil {
		t.Fatalf("fire = %+v", res)
	}
	task, err := s.GetTask(ctx, *res.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != db.StateBacklog || task.StartAt == nil || !task.StartAt.Equal(fire) || task.StartTz == nil || *task.StartTz != "UTC" ||
		task.ScheduleID == nil || *task.ScheduleID != sc.ID || task.ScheduleTitle == nil || *task.ScheduleTitle != "nightly" ||
		task.Title != "nightly" || len(task.Tags) != 1 {
		t.Fatalf("spawned = %+v", task)
	}
	got, _ := s.GetSchedule(ctx, sc.ID)
	if got.NextRunAt == nil || !got.NextRunAt.After(now) || got.LastRunAt == nil || !got.LastRunAt.Equal(fire) ||
		got.LastTaskID == nil || *got.LastTaskID != task.ID || got.LastTaskState == nil || *got.LastTaskState != db.StateBacklog {
		t.Fatalf("after fire = %+v", got)
	}
	if r := lastRun(t, s, ctx, sc.ID); r.Outcome != db.RunStarted || r.TaskID == nil || r.Task == nil || r.Task.ID != task.ID {
		t.Fatalf("run = %+v", r)
	}

	// Pending (backlog with start time) previous run skips the next fire.
	fire = *got.NextRunAt
	if res := mustFire(t, s, ctx, sc.ID, fire.Add(time.Second)); res.Outcome != db.RunSkipped {
		t.Fatalf("pending fire = %+v", res)
	}
	if r := lastRun(t, s, ctx, sc.ID); r.Detail != "#1 still pending" || r.TaskID != nil {
		t.Fatalf("skip run = %+v", r)
	}

	// Active previous run skips.
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateNeedsInput}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetSchedule(ctx, sc.ID)
	fire = *got.NextRunAt
	mustFire(t, s, ctx, sc.ID, fire.Add(time.Second))
	if r := lastRun(t, s, ctx, sc.ID); r.Outcome != db.RunSkipped || r.Detail != "#1 still needs input" {
		t.Fatalf("active skip = %+v", r)
	}

	// Done but unsettled does not block.
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateDone}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetSchedule(ctx, sc.ID)
	fire = *got.NextRunAt
	if res := mustFire(t, s, ctx, sc.ID, fire.Add(time.Second)); res.Outcome != db.RunStarted {
		t.Fatalf("after done = %+v", res)
	}

	// Server down for an hour: one missed row, nothing spawned, next is in the future.
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: 2, To: db.StateSettled}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetSchedule(ctx, sc.ID)
	fire = *got.NextRunAt
	now = fire.Add(time.Hour)
	if res := mustFire(t, s, ctx, sc.ID, now); res.Outcome != db.RunMissed || res.TaskID != nil {
		t.Fatalf("missed = %+v", res)
	}
	if r := lastRun(t, s, ctx, sc.ID); r.Detail != "missed 13 fires" || !r.FireAt.Equal(fire) {
		t.Fatalf("missed run = %+v", r)
	}
	got, _ = s.GetSchedule(ctx, sc.ID)
	if !got.NextRunAt.After(now) || got.NextRunAt.Sub(now) > 5*time.Minute {
		t.Fatalf("next after miss = %v (now %v)", got.NextRunAt, now)
	}
	tasks, _ := s.ListTasks(ctx, db.TaskFilter{})
	if len(tasks) != 2 {
		t.Fatalf("tasks = %d, want 2", len(tasks))
	}
}

func TestSchedulePauseAndDelete(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "proj")
	sc := mustSchedule(t, s, ctx, p.ID, "0 2 * * *")

	got, err := s.UpdateSchedule(ctx, sc.ID, db.SchedulePatch{Paused: db.Optional[bool]{Set: true, V: true}})
	if err != nil || !got.Paused || got.NextRunAt != nil {
		t.Fatalf("paused = %+v %v", got, err)
	}
	due, _ := s.ListSchedules(ctx, db.ScheduleFilter{DueBefore: new(time.Now().Add(48 * time.Hour))})
	if len(due) != 0 {
		t.Fatalf("paused schedule listed as due: %+v", due)
	}
	got, err = s.UpdateSchedule(ctx, sc.ID, db.SchedulePatch{Paused: db.Optional[bool]{Set: true, V: false}})
	if err != nil || got.Paused || got.NextRunAt == nil || !got.NextRunAt.After(time.Now()) {
		t.Fatalf("resumed = %+v %v", got, err)
	}
	if _, err := s.UpdateSchedule(ctx, sc.ID, db.SchedulePatch{Tz: db.Optional[string]{Set: true, V: "Nope/Zone"}}); err == nil {
		t.Fatal("bad tz accepted")
	}

	task, err := s.RunScheduleNow(ctx, sc.ID)
	if err != nil || task.ScheduleID == nil || task.StartAt != nil {
		t.Fatalf("run now = %+v %v", task, err)
	}
	if r := lastRun(t, s, ctx, sc.ID); r.Outcome != db.RunManual {
		t.Fatalf("manual run = %+v", r)
	}
	if err := s.DeleteSchedule(ctx, sc.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSchedule(ctx, sc.ID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("get deleted = %v", err)
	}
	task, err = s.GetTask(ctx, task.ID)
	if err != nil || task.ScheduleID != nil || task.ScheduleTitle != nil {
		t.Fatalf("task after delete = %+v %v", task, err)
	}
}

func TestTaskStartAt(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "proj")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	at := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	tz := "Asia/Kolkata"
	b, err := s.CreateTask(ctx, db.NewTask{
		ProjectID: p.ID, Title: "b", Prompt: "p", RuntimeMode: "full-access", InteractionMode: "default",
		Workspace: db.Workspace{Type: "root"}, StartAt: &at, StartTz: &tz,
	})
	if err != nil || b.StartAt == nil || !b.StartAt.Equal(at) || *b.StartTz != tz {
		t.Fatalf("create = %+v %v", b, err)
	}
	// A start time excludes needs, both ways; clearing one allows the other.
	var invalid *db.InvalidError
	if _, err := s.SetNeeds(ctx, b.ID, []int64{a.ID}); !errors.As(err, &invalid) {
		t.Fatalf("needs with start = %v", err)
	}
	if got, _ := s.GetTask(ctx, a.ID); got.TreeID != nil {
		t.Fatalf("refused needs must roll back the tree: %+v", got)
	}
	b, err = s.UpdateTask(ctx, b.ID, db.TaskPatch{
		StartAt: db.Optional[time.Time]{Set: true, Null: true}, StartTz: db.Optional[string]{Set: true, Null: true},
	})
	if err != nil || b.StartAt != nil {
		t.Fatalf("clear start = %+v %v", b, err)
	}
	if b, err = s.SetNeeds(ctx, b.ID, []int64{a.ID}); err != nil || !slices.Equal(b.Needs, []int64{a.ID}) {
		t.Fatalf("needs after clearing start = %+v %v", b, err)
	}
	if _, err := s.UpdateTask(ctx, b.ID, db.TaskPatch{StartAt: db.Optional[time.Time]{Set: true, V: at}, StartTz: db.Optional[string]{Set: true, V: tz}}); !errors.As(err, &invalid) {
		t.Fatalf("start with needs = %v", err)
	}
	if _, err := s.SetNeeds(ctx, b.ID, nil); err != nil {
		t.Fatal(err)
	}
	b, err = s.UpdateTask(ctx, b.ID, db.TaskPatch{StartAt: db.Optional[time.Time]{Set: true, V: at}, StartTz: db.Optional[string]{Set: true, V: tz}})
	if err != nil || b.StartAt == nil || len(b.Needs) != 0 {
		t.Fatalf("switch back to start = %+v %v", b, err)
	}
	due, _ := s.DueOneShots(ctx, at.Add(-time.Second))
	if len(due) != 0 {
		t.Fatalf("early due = %+v", due)
	}
	due, _ = s.DueOneShots(ctx, at)
	if len(due) != 1 || due[0].ID != b.ID {
		t.Fatalf("due = %+v", due)
	}
	// A launch clears the start time; duplicate never copies it.
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: b.ID, From: []db.TaskState{db.StateBacklog}, To: db.StateLaunching, BeginAttempt: true}); err != nil {
		t.Fatal(err)
	}
	b, _ = s.GetTask(ctx, b.ID)
	if b.StartAt != nil || b.StartTz != nil {
		t.Fatalf("launch kept start = %+v", b)
	}
	// Start fields are backlog-only.
	var conflict *db.ConflictError
	if _, err := s.UpdateTask(ctx, b.ID, db.TaskPatch{StartAt: db.Optional[time.Time]{Set: true, V: at}, StartTz: db.Optional[string]{Set: true, V: tz}}); !errors.As(err, &conflict) {
		t.Fatalf("start outside backlog = %v", err)
	}
}
