package storetest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func init() {
	register("AcceptedScheduledTaskDoesNotLaunch", caseAcceptedScheduledTaskDoesNotLaunch)
	register("AutomaticClaimRechecksReleaseEligibility", caseAutomaticClaimRechecksReleaseEligibility)
	register("ExecutionNeverAcceptsNewWork", caseExecutionNeverAcceptsNewWork)
	register("CompletionEvidenceSessionLink", caseCompletionEvidenceSessionLink)
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
	task, err := s.CreateTask(ctx, db.NewTask{ConnectionID: dbtest.T3Connection, ProjectID: p.ID, Title: "scheduled", Prompt: "work", StartAt: &at, StartTz: &tz})
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

func caseExecutionNeverAcceptsNewWork(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "p")
	a := mustTask(t, s, ctx, p.ID, "source", nil)
	b := mustTask(t, s, ctx, p.ID, "dependent", []int64{a.ID})
	if _, err := s.UpdateTree(ctx, *b.TreeID, db.TreePatch{StartMode: db.Optional[db.StartMode]{Set: true, V: db.StartManual}}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []db.TaskState{db.StateDone, db.StateInterrupted, db.StateFailed, db.StateCancelled} {
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

func caseCompletionEvidenceSessionLink(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "project")
	task := mustTask(t, s, ctx, p.ID, "task", nil)
	ref := session("observed-session")
	launch(t, s, ctx, task.ID, ref.ExternalSessionID, db.StateDone)
	web := "https://example.com/session"
	native := "example-app://session"
	beforeCompletion := time.Now().Add(-time.Minute)
	at := time.Now()

	for _, tc := range []struct {
		links execution.Links
		want  *string
	}{
		{execution.Links{Command: "resume"}, nil},
		{execution.Links{Native: native, Command: "resume"}, &native},
		{execution.Links{Web: web, Native: native}, &web},
	} {
		at = at.Add(time.Second)
		if err := s.PutObservation(ctx, execution.Observation{
			Session: ref, Project: "project", State: "done", Links: tc.links, UpdatedAt: at,
		}); err != nil {
			t.Fatal(err)
		}
		p, err := s.CompletionPreview(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		e := p.Evidence
		if e.ExternalSessionID == nil || *e.ExternalSessionID != ref.ExternalSessionID {
			t.Fatalf("evidence session: %+v", e)
		}
		switch {
		case tc.want == nil && e.SessionLink == nil:
		case tc.want != nil && e.SessionLink != nil && *e.SessionLink == *tc.want:
		default:
			t.Fatalf("evidence link from %+v: %+v", tc.links, e)
		}
	}
	filter := db.TaskFilter{CompletedFrom: &beforeCompletion}
	if tasks, err := s.ListTasks(ctx, filter); err != nil || len(tasks) != 0 {
		t.Fatalf("completed filter before acceptance: %+v %v", tasks, err)
	}
	acceptTask(t, s, ctx, task.ID)
	if tasks, err := s.ListTasks(ctx, filter); err != nil || len(tasks) != 1 || tasks[0].ID != task.ID {
		t.Fatalf("completed filter after acceptance: %+v %v", tasks, err)
	}
	history, err := s.Acceptances(ctx, task.ID)
	if err != nil || len(history) != 1 || history[0].Evidence.SessionLink == nil || *history[0].Evidence.SessionLink != web {
		t.Fatalf("recorded evidence: %+v %v", history, err)
	}
}
