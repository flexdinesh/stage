package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// PutObservation keeps normalized state scoped by connection. A newer
// stored observation wins; a reply the harness did not repeat is kept
// within the same execution.
func (s *Store) PutObservation(ctx context.Context, o execution.Observation) error {
	return s.PutObservationAt(ctx, o, time.Time{})
}

// PutObservationAt also records when Stage captured the observation.
func (s *Store) PutObservationAt(ctx context.Context, o execution.Observation, capturedAt time.Time) error {
	if err := o.Valid(); err != nil {
		return err
	}
	return s.e.Write(ctx, func(tx store.Tx) error {
		// Write may rerun fn (Postgres retry): start each run from scratch.
		o := o
		id, err := ensureSession(ctx, tx, o.Session)
		if err != nil {
			return err
		}
		previous, err := tx.Observations().Get(ctx, id)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if previous.UpdatedAt.After(o.UpdatedAt) {
			return nil
		}
		if o.Reply == nil && o.ExecutionID == previous.ExecutionID {
			o.Reply = previous.Reply
		}
		// Usage is cumulative per session: a read without it (an unreadable
		// transcript) keeps the last one.
		if o.Usage == nil {
			o.Usage = previous.Usage
		}
		return tx.Observations().Put(ctx, id, store.ObservationSnapshot{Observation: o, CapturedAt: capturedAt})
	})
}
func (s *Store) Observation(ctx context.Context, ref execution.SessionRef) (execution.Observation, error) {
	o, err := s.ObservationSnapshot(ctx, ref)
	return o.Observation, err
}

// ObservationSnapshot includes the capture time for ordering observations when
// the harness reports the same UpdatedAt.
func (s *Store) ObservationSnapshot(ctx context.Context, ref execution.SessionRef) (store.ObservationSnapshot, error) {
	return read(ctx, s, func(tx store.Tx) (store.ObservationSnapshot, error) { return tx.Observations().ByRef(ctx, ref) })
}
func (s *Store) ObservationUnavailable(ctx context.Context, ref execution.SessionRef, reason string) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Observations().Unavailable(ctx, ref, reason) })
}

type (
	Attempt         = store.Attempt
	ObservedSession = store.ObservedSession
)

// Observed is the attempt record of the session o names. It keeps o's
// resume command (Links.Command) in case recording the observation failed.
func Observed(o execution.Observation) *ObservedSession {
	s := &ObservedSession{SessionRef: o.Session, Title: o.Title, State: o.State}
	if o.Links.Command != "" {
		s.Links = &execution.Links{Command: o.Links.Command}
	}
	return s
}

func (s *Store) Attempt(ctx context.Context, id int64, n int) (Attempt, error) {
	return read(ctx, s, func(tx store.Tx) (Attempt, error) { return tx.Attempts().Get(ctx, id, n) })
}
func (s *Store) PrepareAttempt(ctx context.Context, id int64, n int, in execution.Intent) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Attempts().Prepare(ctx, id, n, in) })
}
func (s *Store) SaveCheckpoint(ctx context.Context, a *Attempt, c execution.Checkpoint) error {
	saved, err := write(ctx, s, func(tx store.Tx) (bool, error) { return tx.Attempts().SaveCheckpoint(ctx, *a, c) })
	if err != nil {
		return err
	}
	if !saved {
		return Conflict("launch changed; reload the task")
	}
	a.Revision++
	a.Checkpoint = c
	a.Phase = "unconfirmed"
	return nil
}

// Unconfirmed keeps the attempt open with cause and the next retry (backing
// off to a minute). seen, when the harness returned a session Stage could not
// record, replaces the attempt's observed session; nil keeps it.
func (s *Store) Unconfirmed(ctx context.Context, a Attempt, cause error, seen *ObservedSession) error {
	delay := 5 * time.Second
	for i := 0; i < a.RetryCount && delay < time.Minute; i++ {
		delay = min(delay*2, time.Minute)
	}
	// Keep only the resume command as a fallback for the observation (Observed).
	var observed *ObservedSession
	if seen != nil {
		stored := *seen
		stored.Links = nil
		if seen.Links != nil && seen.Links.Command != "" {
			stored.Links = &execution.Links{Command: seen.Links.Command}
		}
		observed = &stored
	}
	return s.e.Write(ctx, func(tx store.Tx) error {
		kept, err := tx.Attempts().Unconfirmed(ctx, a, cause.Error(), time.Now().UTC().Add(delay), observed)
		if err != nil {
			return err
		}
		if !kept {
			return Conflict("launch changed; reload the task")
		}
		return tx.Tasks().SetLaunchError(ctx, a, cause.Error())
	})
}
func (s *Store) RecoverAttempts(ctx context.Context) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Attempts().Recover(ctx) })
}

// FinishAttempt atomically attaches execution and closes recovery. User linking
// and automatic reconciliation use the same revision guard and ownership check.
func (s *Store) FinishAttempt(ctx context.Context, a Attempt, o execution.Observation, manual bool) error {
	if o.State == "idle" {
		return Conflict("Wait for the harness to report execution before linking this launch.")
	}
	if err := o.Valid(); err != nil {
		return err
	}
	return s.e.Write(ctx, func(tx store.Tx) error {
		d, err := tx.Attempts().Destination(ctx, a.TaskID, a.Number)
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("task %d attempt %d has no launch destination", a.TaskID, a.Number)
		}
		if err != nil {
			return err
		}
		connection, project := d.ConnectionID, d.ExternalProjectID
		if o.Session.ConnectionID != connection || o.Project != project {
			return Conflict("session belongs to another launch destination")
		}
		if o.Child || o.Archived {
			return Conflict("select an active top-level session")
		}
		sid, err := ensureSession(ctx, tx, o.Session)
		if err != nil {
			return err
		}
		occupied, err := tx.Tasks().SessionOccupied(ctx, sid, a.TaskID)
		if err != nil {
			return err
		}
		if occupied {
			return Conflict("session already belongs to another task")
		}
		confirmed, err := tx.Attempts().Confirm(ctx, a)
		if err != nil {
			return err
		}
		if !confirmed {
			return Conflict("launch already changed; reload the preview")
		}
		attached, err := tx.Tasks().Confirm(ctx, a, o.State, sid, o.EndedAt)
		if err != nil {
			return err
		}
		if !attached {
			return Conflict("task no longer has this unresolved launch")
		}
		detail := "execution confirmed"
		eventCtx := ctx
		if manual {
			detail = "existing session linked by user; no launch sent"
			eventCtx = WithWorkCause(ctx, Cause{Kind: "launch_attempt", TaskID: a.TaskID, Attempt: a.Number, Session: &o.Session})
		} else {
			eventCtx = AttemptOutcome(ctx, a, &o.Session)
		}
		from := StateLaunching
		return addTaskEvent(eventCtx, tx, a.TaskID, &from, TaskState(o.State), detail)
	})
}
func (s *Store) RejectAttempt(ctx context.Context, a Attempt, reason string) error {
	ctx = AttemptOutcome(ctx, a, nil)
	return s.e.Write(ctx, func(tx store.Tx) error {
		rejected, err := tx.Attempts().Reject(ctx, a, reason)
		if err != nil {
			return err
		}
		if !rejected {
			return Conflict("launch changed")
		}
		if err := tx.Tasks().Reject(ctx, a, reason); err != nil {
			return err
		}
		from := StateLaunching
		return addTaskEvent(ctx, tx, a.TaskID, &from, StateBacklog, fmt.Sprintf("launch rejected before execution: %s", reason))
	})
}

// AttemptOutcome derives the automation stamp from the persisted claim,
// including when a later request or process reconciles it.
func AttemptOutcome(ctx context.Context, a Attempt, ref *SessionRef) context.Context {
	cause := Cause{Kind: "launch_attempt", TaskID: a.TaskID, Attempt: a.Number, Session: ref}
	if a.Intent != nil {
		cause.RequestID = a.Intent.RequestID
	}
	return AutomatedWork(ctx, "system", cause, a.Attribution)
}

// WatchedSessions retains unowned observations after they leave the harness
// attention feed, so their eventual outcome remains observable.
func (s *Store) WatchedSessions(ctx context.Context, connection int64) ([]execution.SessionRef, error) {
	return read(ctx, s, func(tx store.Tx) ([]execution.SessionRef, error) { return tx.Observations().Watched(ctx, connection) })
}

// NeedsInputCount: tasks in needs_input plus top-level sessions no task owns
// observed needing input.
func (s *Store) NeedsInputCount(ctx context.Context) (int, error) {
	return read(ctx, s, func(tx store.Tx) (int, error) { return tx.Observations().NeedsInputCount(ctx) })
}
