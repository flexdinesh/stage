package storetest

import (
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
)

func init() {
	register("StateRevision", caseStateRevision)
}

// caseStateRevision: every real state transition bumps state_revision once;
// same-state transitions and events that keep the state do not. A recorded
// guarded answer stores its revision (ADR 0013 first answer wins).
func caseStateRevision(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "proj-rev")
	task := mustTask(t, s, ctx, p.ID, "a", nil)
	revision := func(want int64) db.Task {
		t.Helper()
		got, err := s.GetTask(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.StateRevision != want {
			t.Fatalf("state revision %d in %s, want %d", got.StateRevision, got.State, want)
		}
		return got
	}
	revision(0)
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: task.ID, From: []db.TaskState{db.StateBacklog}, To: db.StateLaunching, BeginAttempt: true}); err != nil {
		t.Fatal(err)
	}
	revision(1)
	ref := db.SessionRef{ConnectionID: s.ActiveID(db.LocalMachine, "t3"), ExternalSessionID: "rev-1"}
	for _, to := range []db.TaskState{db.StateRunning, db.StateNeedsInput} {
		if _, err := s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: to, Session: &ref}); err != nil {
			t.Fatal(err)
		}
	}
	revision(3)
	// No-ops: the same state again, and events that keep the state.
	if res, err := s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateNeedsInput}); err != nil || res.Changed {
		t.Fatalf("same-state transition: %+v %v", res, err)
	}
	if err := s.AddTaskEvent(ctx, task.ID, "Follow-up sent from Stage"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordTaskOperation(ctx, task.ID, ref, "task_stopped", "Stop requested from Stage"); err != nil {
		t.Fatal(err)
	}
	got := revision(3)
	if got.AnsweredRevision != nil {
		t.Fatalf("answered before any answer: %d", *got.AnsweredRevision)
	}
	if err := s.RecordTaskAnswer(ctx, task.ID, ref, "session_response", "Answered a session request", ptr(got.StateRevision)); err != nil {
		t.Fatal(err)
	}
	got = revision(3)
	if got.AnsweredRevision == nil || *got.AnsweredRevision != 3 {
		t.Fatalf("answered revision %v, want 3", got.AnsweredRevision)
	}
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateRunning}); err != nil {
		t.Fatal(err)
	}
	revision(4)
}
