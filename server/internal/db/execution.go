package db

import (
	"context"
	"errors"
	"fmt"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
	"time"
)

// PutObservation keeps normalized state scoped by connection. The T3 cache is
// maintained only for retained API compatibility, never as execution authority.
func (s *Store) PutObservation(ctx context.Context, o execution.Observation) error {
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
		if o.Reply == nil && (s.IsT3(o.Session.ConnectionID) || o.ExecutionID == previous.ExecutionID) {
			o.Reply = previous.Reply
		}
		if err := tx.Observations().Put(ctx, id, o); err != nil {
			return err
		}
		if !s.IsT3(o.Session.ConnectionID) {
			return nil
		}
		pending := 0
		if o.State == "needs_input" {
			pending = 1
		}
		active := ""
		if o.Active {
			active = o.ExecutionID
		}
		// settled_at keeps the first observed settle; T3's own settledAt is not carried.
		settled := o.Closed != nil && o.Closed.Signal == execution.ClosedSettled
		var settledAt *time.Time
		if settled {
			settledAt = &o.UpdatedAt
		}
		err = mirrorThread(ctx, tx, o, store.ThreadMirror{
			SessionID: id, ParentThreadID: externalParent(o), Relationship: childMarker(o.Child),
			Branch: nullIfEmpty(o.Branch), WorktreePath: nullIfEmpty(o.Directory),
			PendingRequests: pending, ActiveRunID: nullIfEmpty(active), LatestRunID: nullIfEmpty(o.ExecutionID),
			Settled: settled, SettledAt: settledAt,
			Model: nullIfEmpty(o.ModelLabel), RuntimeMode: nullIfEmpty(o.Config.Permission), InteractionMode: nullIfEmpty(o.Config.Mode),
		})
		if err != nil {
			return err
		}
		if o.Reply != nil {
			r := o.Reply
			preview := PreviewText(r.Text)
			at := r.UpdatedAt
			err = tx.Threads().SetSessionLastWord(ctx, id, o.UpdatedAt, store.LastWordRow{Text: r.Text, Preview: &preview, Truncated: r.Truncated,
				RunID: nullIfEmpty(r.RunID), At: &at})
		}
		return err
	})
}
func childMarker(child bool) *string {
	if child {
		v := "subagent"
		return &v
	}
	return nil
}
func (s *Store) Observation(ctx context.Context, ref execution.SessionRef) (execution.Observation, error) {
	return read(ctx, s, func(tx store.Tx) (execution.Observation, error) { return tx.Observations().ByRef(ctx, ref) })
}
func (s *Store) ObservationUnavailable(ctx context.Context, ref execution.SessionRef, reason string) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Observations().Unavailable(ctx, ref, reason) })
}

type (
	Attempt         = store.Attempt
	ObservedSession = store.ObservedSession
)

// Observed is the attempt record of the session o names. It keeps o's
// resume command (Links.Command): with no stored observation, it is the
// only way back to a session on a machine whose adapter offers no
// destination (a remote one's Destinations is empty).
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
	// The session's destinations are resolved when read; only its resume
	// command is kept (Observed).
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
		if !s.IsT3(o.Session.ConnectionID) {
			return Conflict("Wait for the harness to report execution before linking this launch.")
		}
		o.State = "done"
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
		if manual {
			detail = "existing session linked by user; no launch sent"
		}
		from := StateLaunching
		return addTaskEvent(ctx, tx, a.TaskID, &from, TaskState(o.State), detail)
	})
}
func (s *Store) RejectAttempt(ctx context.Context, a Attempt, reason string) error {
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

func externalParent(o execution.Observation) *string {
	if o.Parent != nil {
		return &o.Parent.ExternalSessionID
	}
	return nil
}

// WatchedSessions retains normalized active observations after they leave the
// harness attention feed, so their eventual outcome remains observable.
func (s *Store) WatchedSessions(ctx context.Context, connection int64) ([]execution.SessionRef, error) {
	return read(ctx, s, func(tx store.Tx) ([]execution.SessionRef, error) { return tx.Observations().Watched(ctx, connection) })
}
