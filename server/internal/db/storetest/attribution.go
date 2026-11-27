package storetest

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/principal"
)

func init() {
	register("AttributionAcrossWork", caseAttributionAcrossWork)
}

func caseAttributionAcrossWork(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	var memberID, runnerID int64
	if err := s.WriteAccess(ctx, func(r store.Access) error {
		if _, err := db.ConfigureAccessTx(ctx, r, ptr(true)); err != nil {
			return err
		}
		if err := r.ClaimOwner(ctx, store.AccountInput{DisplayName: "Owner", Email: "owner@example.test"}); err != nil {
			return err
		}
		if err := r.MarkClaimed(ctx, time.Now()); err != nil {
			return err
		}
		var err error
		memberID, err = r.InsertMember(ctx, store.AccountInput{DisplayName: "Reviewer", Email: "reviewer@example.test"})
		if err != nil {
			return err
		}
		runnerID, err = r.InsertMember(ctx, store.AccountInput{DisplayName: "Runner", Email: "runner@example.test"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	owner := principal.With(ctx, principal.Actor{Kind: principal.Admin, AccountID: db.DefaultUser})
	member := principal.With(db.WithUser(ctx, memberID), principal.Actor{Kind: principal.Human, AccountID: memberID})
	p := mustProject(t, s, ctx, "attribution")
	first := mustTask(t, s, owner, p.ID, "source", nil)
	next := mustTask(t, s, owner, p.ID, "dependent", []int64{first.ID})
	if _, err := s.UpdateTask(owner, next.ID, db.TaskPatch{StartMode: db.Optional[db.StartMode]{Set: true, V: db.StartAuto}}); err != nil {
		t.Fatal(err)
	}
	acceptTask(t, s, member, first.ID)
	accepted, err := s.Acceptances(ctx, first.ID)
	if err != nil || len(accepted) != 1 || accepted[0].Actor.Kind != "human" ||
		accepted[0].Actor.UserID != memberID || accepted[0].Actor.DisplayName != "Reviewer" {
		t.Fatalf("acceptance = %+v, %v", accepted, err)
	}
	claim, err := s.TransitionTask(ctx, db.Transition{TaskID: next.ID, To: db.StateLaunching, BeginAttempt: true, RequireAuto: true})
	if err != nil || !claim.Changed {
		t.Fatalf("release = %+v, %v", claim, err)
	}
	a, err := s.Attempt(ctx, next.ID, claim.Attempt)
	if err != nil || a.Actor.Kind != "system" || a.Actor.UserID != 0 || a.Actor.InitiatorUserID != memberID ||
		a.Cause == nil || a.Cause.TaskID != first.ID || a.Cause.AcceptanceID != accepted[0].ID {
		t.Fatalf("released attempt = %+v, %v", a, err)
	}
	// Reconciliation from an unrelated context keeps the initiator from the
	// durable attempt, even after the source is reopened by someone else.
	reopenTask(t, s, owner, first.ID)
	if err := s.RecoverAttempts(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.RejectAttempt(owner, a, "Adapter declined the attempt"); err != nil {
		t.Fatal(err)
	}
	page := journal(t, s, ctx, db.JournalOwner{TaskID: &next.ID}, db.JournalQuery{Kind: "events"})
	outcome := page.Entries[0]
	if outcome.Actor.Kind != "system" || outcome.Actor.UserID != 0 || outcome.Actor.InitiatorUserID != memberID ||
		outcome.Cause == nil || outcome.Cause.TaskID != next.ID || outcome.Cause.Attempt != claim.Attempt {
		t.Fatalf("reconciled outcome = %+v", outcome)
	}
	page = journal(t, s, ctx, db.JournalOwner{TaskID: &first.ID}, db.JournalQuery{Kind: "events"})
	if e := page.Entries[0]; e.EventType != "task_reopened" || e.Actor.UserID != db.DefaultUser {
		t.Fatalf("reopened event = %+v", e)
	}
	creatorMachine := pairMachine(t, s, ctx, "creator").MachineID
	runnerMachine := pairMachine(t, s, ctx, "runner").MachineID
	creator := db.WithMachine(member, creatorMachine)
	runner := principal.With(db.WithUser(db.WithMachine(ctx, runnerMachine), runnerID), principal.Actor{Kind: principal.Human, AccountID: runnerID})
	origin := session("schedule-source")
	selector := db.WorkspaceID(dbtest.T3Connection, "attribution", "/w/attribution")
	sc, err := s.CreateSchedule(creator, db.NewSchedule{
		ConnectionID: dbtest.T3Connection, WorkspaceID: selector, ProjectID: p.ID, OriginSession: &origin,
		Title: "nightly", Prompt: "p", Notes: "remember", Cron: "*/5 * * * *", Tz: "UTC",
	})
	if err != nil || sc.CreatedBy != memberID || sc.SourceMachineID == nil || *sc.SourceMachineID != creatorMachine || sc.NextRunAt == nil {
		t.Fatalf("schedule = %+v, %v", sc, err)
	}
	manual, err := s.RunScheduleNow(runner, sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	fired := mustFire(t, s, runner, sc.ID, *sc.NextRunAt)
	if fired.TaskID == nil {
		t.Fatalf("fire did not spawn: %+v", fired)
	}
	runs, err := s.ScheduleRuns(ctx, sc.ID, 5)
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
	byTask := make(map[int64]db.ScheduleRun, len(runs))
	for _, run := range runs {
		if run.TaskID == nil {
			t.Fatalf("run has no task: %+v", run)
		}
		byTask[*run.TaskID] = run
	}
	cronRun, manualRun := byTask[*fired.TaskID], byTask[manual.ID]
	if cronRun.Outcome != db.RunStarted || cronRun.Actor.Kind != "cron" || cronRun.Actor.UserID != 0 || cronRun.SourceMachine != nil ||
		manualRun.Outcome != db.RunManual || manualRun.Actor.Kind != "human" || manualRun.Actor.UserID != runnerID ||
		manualRun.SourceMachine == nil || *manualRun.SourceMachine != runnerMachine {
		t.Fatalf("run attribution = %+v", runs)
	}
	for _, task := range []db.Task{getTask(t, s, ctx, *fired.TaskID), manual} {
		if task.CreatedBy != memberID || task.SourceMachineID != nil || task.OriginSession == nil ||
			*task.OriginSession != origin || task.WorkspaceID != selector {
			t.Fatalf("spawned task = %+v", task)
		}
		run := byTask[task.ID]
		events, err := s.TaskEvents(ctx, task.ID)
		if err != nil || len(events) != 1 || !reflect.DeepEqual(events[0].Attribution, run.Attribution) ||
			events[0].Cause == nil || *events[0].Cause != (db.Cause{Kind: "schedule", ScheduleID: sc.ID}) {
			t.Fatalf("spawn events = %+v, %v", events, err)
		}
		notes := journal(t, s, ctx, db.JournalOwner{TaskID: &task.ID}, db.JournalQuery{Kind: "notes"})
		if len(notes.Entries) != 1 || notes.Entries[0].CreatedBy != memberID || !reflect.DeepEqual(notes.Entries[0].Attribution, run.Attribution) ||
			notes.Entries[0].SourceSession == nil || *notes.Entries[0].SourceSession != origin {
			t.Fatalf("spawn notes = %+v", notes.Entries)
		}
	}

	delayed := db.WithWorkCause(ctx, db.Cause{Kind: "scheduled_start"})
	cronClaim, err := s.TransitionTask(delayed, db.Transition{TaskID: *fired.TaskID, To: db.StateLaunching, BeginAttempt: true})
	cronCause := db.Cause{Kind: "scheduled_start", TaskID: *fired.TaskID, ScheduleID: sc.ID}
	if err != nil || !cronClaim.Changed || cronClaim.RequestID == "" || cronClaim.Actor.Kind != "cron" || cronClaim.Actor.InitiatorUserID != 0 ||
		cronClaim.Cause == nil || *cronClaim.Cause != cronCause {
		t.Fatalf("cron claim = %+v, %v", cronClaim, err)
	}
	manualCtx := db.WithWorkCause(runner, db.Cause{Kind: "schedule", ScheduleID: sc.ID})
	claim, err = s.TransitionTask(manualCtx, db.Transition{TaskID: manual.ID, To: db.StateLaunching, BeginAttempt: true})
	if err != nil || !claim.Changed || claim.RequestID == "" || claim.RequestID == cronClaim.RequestID {
		t.Fatalf("manual claim = %+v, %v", claim, err)
	}
	frozen, err := s.LaunchDestination(ctx, manual.ID, claim.Attempt)
	if err != nil || frozen.ConnectionID != sc.ConnectionID || frozen.ExternalProjectID != "attribution" || frozen.Workspace != "/w/attribution" {
		t.Fatalf("manual destination = %+v, %v", frozen, err)
	}
	intent := execution.Intent{RequestID: claim.RequestID, ConnectionID: frozen.ConnectionID, Project: frozen.ExternalProjectID, Directory: frozen.Workspace}
	if err := s.PrepareAttempt(ctx, manual.ID, claim.Attempt, intent); err != nil {
		t.Fatal(err)
	}
	a, err = s.Attempt(ctx, manual.ID, claim.Attempt)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Unconfirmed(owner, a, errors.New("outcome unknown"), nil); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverAttempts(ctx); err != nil {
		t.Fatal(err)
	}
	a, err = s.Attempt(ctx, manual.ID, claim.Attempt)
	if err != nil || a.RetryCount != 1 || a.Intent == nil || a.Intent.RequestID != claim.RequestID ||
		a.Actor.Kind != "human" || a.Actor.UserID != runnerID || a.SourceMachine == nil || *a.SourceMachine != runnerMachine ||
		a.Cause == nil || *a.Cause != (db.Cause{Kind: "schedule", ScheduleID: sc.ID}) {
		t.Fatalf("retried attempt = %+v, %v", a, err)
	}
	if d, err := s.LaunchDestination(ctx, manual.ID, claim.Attempt); err != nil || d != frozen || a.Intent.ConnectionID != d.ConnectionID || a.Intent.Project != d.ExternalProjectID || a.Intent.Directory != d.Workspace {
		t.Fatalf("retried destination = %+v, %v", d, err)
	}
	if err := s.RejectAttempt(owner, a, "adapter declined the attempt"); err != nil {
		t.Fatal(err)
	}
	page = journal(t, s, ctx, db.JournalOwner{TaskID: &manual.ID}, db.JournalQuery{Kind: "events"})
	outcome = page.Entries[0]
	if outcome.Actor.Kind != "system" || outcome.Actor.UserID != 0 || outcome.Actor.InitiatorUserID != runnerID ||
		outcome.Attribution.SourceMachine == nil || *outcome.Attribution.SourceMachine != runnerMachine || outcome.Cause == nil ||
		*outcome.Cause != (db.Cause{Kind: "launch_attempt", TaskID: manual.ID, Attempt: claim.Attempt, RequestID: claim.RequestID}) {
		t.Fatalf("manual retry outcome = %+v", outcome)
	}

	startAt := db.Optional[time.Time]{Set: true, V: time.Now().Add(time.Minute)}
	tz := db.Optional[string]{Set: true, V: "UTC"}
	if _, err := s.UpdateTask(member, manual.ID, db.TaskPatch{StartAt: startAt, StartTz: tz}); err != nil {
		t.Fatal(err)
	}
	scheduled, err := s.TransitionTask(delayed, db.Transition{TaskID: manual.ID, To: db.StateLaunching, BeginAttempt: true})
	if err != nil || scheduled.Actor.Kind != "system" || scheduled.Actor.InitiatorUserID != memberID ||
		scheduled.Attempt != claim.Attempt+1 || scheduled.RequestID == "" || scheduled.RequestID == claim.RequestID {
		t.Fatalf("scheduled start = %+v, %v", scheduled, err)
	}
}
