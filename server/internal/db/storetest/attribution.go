package storetest

import (
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/principal"
)

func init() {
	register("AttributionAcrossWork", caseAttributionAcrossWork)
}

func caseAttributionAcrossWork(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	var memberID int64
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
	sc := mustSchedule(t, s, owner, p.ID, "*/5 * * * *")
	manual, err := s.RunScheduleNow(member, sc.ID)
	if err != nil || manual.CreatedBy != db.DefaultUser {
		t.Fatalf("manual task = %+v, %v", manual, err)
	}
	fired := mustFire(t, s, owner, sc.ID, *sc.NextRunAt)
	if fired.TaskID == nil {
		t.Fatalf("fire did not spawn: %+v", fired)
	}
	runs, err := s.ScheduleRuns(ctx, sc.ID, 5)
	if err != nil || len(runs) != 2 || runs[0].Actor.Kind != "cron" || runs[0].Actor.UserID != 0 ||
		runs[1].Actor.Kind != "human" || runs[1].Actor.UserID != memberID {
		t.Fatalf("run actors = %+v, %v", runs, err)
	}
	for i, id := range []int64{*fired.TaskID, manual.ID} {
		events, err := s.TaskEvents(ctx, id)
		if err != nil || len(events) != 1 || events[0].Actor != runs[i].Actor ||
			events[0].Cause == nil || events[0].Cause.ScheduleID != sc.ID {
			t.Fatalf("spawn events = %+v, %v", events, err)
		}
	}
	startAt := db.Optional[time.Time]{Set: true, V: time.Now().Add(time.Minute)}
	tz := db.Optional[string]{Set: true, V: "UTC"}
	if _, err := s.UpdateTask(member, manual.ID, db.TaskPatch{StartAt: startAt, StartTz: tz}); err != nil {
		t.Fatal(err)
	}
	delayed := db.WithWorkCause(ctx, db.Cause{Kind: "scheduled_start"})
	scheduled, err := s.TransitionTask(delayed, db.Transition{TaskID: manual.ID, To: db.StateLaunching, BeginAttempt: true})
	if err != nil || scheduled.Actor.Kind != "system" || scheduled.Actor.InitiatorUserID != memberID {
		t.Fatalf("scheduled start = %+v, %v", scheduled, err)
	}
}
