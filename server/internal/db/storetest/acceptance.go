package storetest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
)

func init() {
	register("AcceptedScheduledTaskDoesNotLaunch", caseAcceptedScheduledTaskDoesNotLaunch)
	register("AutomaticClaimRechecksReleaseEligibility", caseAutomaticClaimRechecksReleaseEligibility)
	register("SettlementNeverAcceptsNewWork", caseSettlementNeverAcceptsNewWork)
}

func acceptTask(t *testing.T, s *db.Store, ctx context.Context, id int64) {
	t.Helper()
	p, err := s.CompletionPreview(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ChangeAcceptance(ctx, id, db.AcceptanceRequest{RequestID: fmt.Sprintf("accept-%d-%d", id, p.Revision), Revision: p.Revision, EvidenceVersion: p.EvidenceVersion}, false); err != nil {
		t.Fatal(err)
	}
}

func caseAcceptedScheduledTaskDoesNotLaunch(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "p")
	at := time.Now().Add(-time.Minute)
	tz := "UTC"
	task, err := s.CreateTask(ctx, db.NewTask{ConnectionID: dbtest.T3Connection, ProjectID: p.ID, Title: "scheduled", Prompt: "work", StartAt: &at, StartTz: &tz, Workspace: db.Workspace{Type: "root"}})
	if err != nil {
		t.Fatal(err)
	}
	acceptTask(t, s, ctx, task.ID)
	due, err := s.DueOneShots(ctx, time.Now())
	if err != nil || len(due) != 0 {
		t.Fatalf("accepted task due: %v %v", due, err)
	}
	reopenTask(t, s, ctx, task.ID)
	due, err = s.DueOneShots(ctx, time.Now())
	if err != nil || len(due) != 1 {
		t.Fatalf("reopened task not due: %v %v", due, err)
	}
}

func caseAutomaticClaimRechecksReleaseEligibility(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "p")
	a := mustTask(t, s, ctx, p.ID, "source", nil)
	b := mustTask(t, s, ctx, p.ID, "dependent", []int64{a.ID})
	acceptTask(t, s, ctx, a.ID)
	if _, err := s.UpdateTask(ctx, b.ID, db.TaskPatch{StartMode: db.Optional[db.StartMode]{Set: true, V: db.StartAuto}}); err != nil {
		t.Fatal(err)
	}
	// A stale runner must not launch after the task became a root.
	if _, err := s.SetNeeds(ctx, b.ID, nil); err != nil {
		t.Fatal(err)
	}
	_, err := s.TransitionTask(ctx, db.Transition{TaskID: b.ID, From: []db.TaskState{db.StateBacklog}, To: db.StateLaunching, BeginAttempt: true, RequireAuto: true})
	if err == nil {
		t.Fatal("automatic claim launched root")
	}
	if got := getTask(t, s, ctx, b.ID); got.LaunchAttempt != 0 {
		t.Fatal("rejected claim consumed attempt")
	}
}
func reopenTask(t *testing.T, s *db.Store, ctx context.Context, id int64) {
	t.Helper()
	p, err := s.CompletionPreview(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ChangeAcceptance(ctx, id, db.AcceptanceRequest{RequestID: fmt.Sprintf("reopen-%d-%d", id, p.Revision), Revision: p.Revision}, true); err != nil {
		t.Fatal(err)
	}
}

func caseSettlementNeverAcceptsNewWork(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "p")
	a := mustTask(t, s, ctx, p.ID, "source", nil)
	b := mustTask(t, s, ctx, p.ID, "dependent", []int64{a.ID})
	if _, err := s.UpdateTree(ctx, *b.TreeID, db.TreePatch{StartMode: db.Optional[db.StartMode]{Set: true, V: db.StartManual}}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []db.TaskState{db.StateDone, db.StateSettled, db.StateInterrupted, db.StateFailed, db.StateCancelled} {
		setState(t, s, ctx, a.ID, state)
		if got := getTask(t, s, ctx, b.ID); readiness(got) != "blocked" {
			t.Fatalf("%s released task", state)
		}
	}
	acceptTask(t, s, ctx, a.ID)
	if got := getTask(t, s, ctx, b.ID); readiness(got) != "ready" {
		t.Fatal("acceptance did not release manual readiness")
	}
	setState(t, s, ctx, a.ID, db.StateRunning)
	if got := getTask(t, s, ctx, b.ID); readiness(got) != "ready" {
		t.Fatal("activity revoked acceptance")
	}
	reopenTask(t, s, ctx, a.ID)
	if got := getTask(t, s, ctx, b.ID); readiness(got) != "blocked" {
		t.Fatal("reopen did not block unclaimed start")
	}
}
