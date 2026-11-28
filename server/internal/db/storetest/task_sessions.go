package storetest

import (
	"errors"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func init() {
	register("TaskSessionHistory", caseTaskSessionHistory)
}

func caseTaskSessionHistory(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "history")
	task := mustTask(t, s, ctx, p.ID, "Runner", nil)
	other := mustTask(t, s, ctx, p.ID, "Other", nil)
	move := func(to db.TaskState, ref *db.SessionRef) {
		t.Helper()
		_, err := s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: to, BeginAttempt: to == db.StateLaunching, Session: ref})
		if err != nil {
			t.Fatal(err)
		}
	}
	beforeRelink, first, second := session("before-relink"), session("first"), session("second")
	move(db.StateLaunching, nil)
	move(db.StateRunning, &beforeRelink)
	// An authoritative relink in the same attempt replaces that attempt's row.
	move(db.StateNeedsInput, &first)
	row, err := s.TaskSession(ctx, task.ID, 1)
	if err != nil || row.Session != first || !row.Current || row.RecordedAt.IsZero() {
		t.Fatalf("current relink: %+v %v", row, err)
	}
	if _, err := s.TaskSessionOwner(ctx, beforeRelink); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("replaced ref still owned: %v", err)
	}
	move(db.StateFailed, &first)
	retried, err := s.TaskSession(ctx, task.ID, 1)
	if err != nil || !retried.RecordedAt.Equal(row.RecordedAt) {
		t.Fatalf("repeated ref changed recordedAt: %+v %v", retried, err)
	}
	move(db.StateLaunching, nil)
	past, err := s.TaskSession(ctx, task.ID, 1)
	if err != nil || past.Current {
		t.Fatalf("attempt became past while next launch was pending: %+v %v", past, err)
	}

	var conflict *db.ConflictError
	for _, id := range []int64{task.ID, other.ID} {
		if _, err := s.TransitionTask(ctx, db.Transition{TaskID: id, To: db.StateRunning, Session: &first}); !errors.As(err, &conflict) {
			t.Fatalf("duplicate session on task %d: %v", id, err)
		}
		if got, err := s.GetTask(ctx, id); err != nil || got.Session != nil {
			t.Fatalf("duplicate session did not roll back: %+v %v", got, err)
		}
	}
	move(db.StateRunning, &second)

	seen := time.Date(2026, 11, 29, 1, 0, 0, 0, time.UTC)
	o := execution.Observation{Session: first, State: "failed", Project: "history", UpdatedAt: seen}
	if err := s.PutObservationAt(ctx, o, seen); err != nil {
		t.Fatal(err)
	}
	history, err := s.TaskSessions(ctx, task.ID)
	if err != nil || len(history) != 2 || history[0].Attempt != 2 || history[0].Session != second || !history[0].Current ||
		history[1].Attempt != 1 || history[1].Session != first || history[1].Current || history[1].Observation.State != "failed" ||
		!history[1].Observation.CapturedAt.Equal(seen) {
		t.Fatalf("task history %+v: %v", history, err)
	}
	owner, err := s.TaskSessionOwner(ctx, first)
	if err != nil || owner.TaskID != task.ID || owner.Attempt != 1 {
		t.Fatalf("historical owner %+v: %v", owner, err)
	}
	if _, err := s.TaskBySession(ctx, first); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("historical session treated as current: %v", err)
	}
	adopted, created, err := s.AdoptSession(ctx, db.AdoptInput{Session: first})
	if err != nil || created || adopted.ID != task.ID {
		t.Fatalf("adopting past ref created a second owner: %+v %v %v", adopted, created, err)
	}
}
