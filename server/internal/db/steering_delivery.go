package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/principal"
)

// Steering delivery (ADR 0013 "Delivery" and "Tree finish"): the
// dispatcher (internal/steering) reads active bindings and their pending
// events, records what it sent, ends a finished tree's binding and
// auto-completes its steerer once.

type (
	SteererState      = store.SteererState
	SteeringPending   = store.SteeringPending
	SteererCompletion = store.SteererCompletion
)

// SteeringSentDetail is the steerer task event a delivered digest records.
func SteeringSentDetail(through int64) string {
	return fmt.Sprintf("Steering update sent (through #%d)", through)
}

// SteererAutoCompletedDetail is the acceptance event of a steerer Stage
// completed because its tree finished.
func SteererAutoCompletedDetail(tree int64) string {
	return fmt.Sprintf("Completed automatically: tree #%d finished", tree)
}

// ActiveSteerers are every active binding's delivery state.
func (s *Store) ActiveSteerers(ctx context.Context) ([]SteererState, error) {
	return read(ctx, s, func(tx store.Tx) ([]SteererState, error) { return tx.Steering().ActiveStates(ctx) })
}

// SteeringPending summarizes binding id's events after after not caused
// by its steerer task.
func (s *Store) SteeringPending(ctx context.Context, id, after int64) (SteeringPending, error) {
	return read(ctx, s, func(tx store.Tx) (SteeringPending, error) { return tx.Steering().Pending(ctx, id, after) })
}

// SetSteeringError sets (nil: clears) active binding id's last error.
func (s *Store) SetSteeringError(ctx context.Context, id int64, msg *string) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Steering().SetError(ctx, id, msg) })
}

// RecordSteeringDelivery records a digest sent through event through on
// active binding id: delivered_through, last_delivered_at, a cleared last
// error and one steerer task event. When through reaches the binding's
// tree_finished event it also ends the binding (tree_finished), keeping
// finalRevision (the steerer's state revision before the send) for the
// auto-complete. Reports false when the binding is no longer active
// (nothing recorded).
func (s *Store) RecordSteeringDelivery(ctx context.Context, id, through, finalRevision int64) (bool, error) {
	return write(ctx, s, func(tx store.Tx) (bool, error) {
		st, ok, err := lockActiveSteerer(ctx, tx, id)
		if err != nil || !ok {
			return false, err
		}
		if _, err := tx.Steering().SetDelivered(ctx, id, through, true); err != nil {
			return false, err
		}
		ls, err := tx.Tasks().LaunchState(ctx, st.TaskID)
		if err != nil {
			return false, err
		}
		state := ls.State
		if err := addTaskEvent(ctx, tx, st.TaskID, &state, state, SteeringSentDetail(through)); err != nil {
			return false, err
		}
		if st.FinishEventID == nil || through < *st.FinishEventID {
			return true, nil
		}
		if err := tx.Steering().SetFinalRevision(ctx, id, finalRevision); err != nil {
			return false, err
		}
		return true, endSteerer(ctx, tx, store.Steerer{ID: st.ID, TreeID: st.TreeID, TaskID: st.TaskID}, store.SteeringTreeFinished)
	})
}

// FinishSteering ends active binding id with its tree (tree_finished)
// without a final digest: the steerer is not running. Reports false when
// it was no longer active.
func (s *Store) FinishSteering(ctx context.Context, id int64) (bool, error) {
	return write(ctx, s, func(tx store.Tx) (bool, error) {
		st, ok, err := lockActiveSteerer(ctx, tx, id)
		if err != nil || !ok {
			return false, err
		}
		return true, endSteerer(ctx, tx, store.Steerer{ID: st.ID, TreeID: st.TreeID, TaskID: st.TaskID}, store.SteeringTreeFinished)
	})
}

// lockActiveSteerer locks binding id's tree and task (EndTreeSteering's
// order) and rereads its state (false: no longer active).
func lockActiveSteerer(ctx context.Context, tx store.Tx, id int64) (SteererState, bool, error) {
	st, err := tx.Steering().ActiveState(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return st, false, nil
	}
	if err != nil {
		return st, false, err
	}
	if err := tx.Trees().Lock(ctx, st.TreeID); err != nil {
		return st, false, err
	}
	if err := tx.Tasks().Lock(ctx, st.TaskID); err != nil {
		return st, false, err
	}
	st, err = tx.Steering().ActiveState(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return st, false, nil
	}
	return st, err == nil, err
}

// SteerersAwaitingCompletion are bindings that ended with their tree after
// the final digest, whose steerer has no auto-complete decision yet.
func (s *Store) SteerersAwaitingCompletion(ctx context.Context) ([]SteererCompletion, error) {
	return read(ctx, s, func(tx store.Tx) ([]SteererCompletion, error) { return tx.Steering().AwaitingCompletion(ctx) })
}

// AutoCompleteSteerer decides c's steerer task once, after its state moved
// past the final digest: done completes it through the acceptance path
// (system actor, SteererAutoCompletedDetail); failed, interrupted or
// cancelled, or an acceptance someone already made, skips it for good.
// Any other state waits. Reports whether it completed the task.
func (s *Store) AutoCompleteSteerer(ctx context.Context, c SteererCompletion) (bool, error) {
	ctx = principal.With(ctx, principal.Actor{Kind: principal.System})
	ctx = WithAttribution(ctx, Attribution{Actor: store.Actor{Kind: "system"}, Cause: &Cause{Kind: "steering_finish", TaskID: c.TaskID}})
	return write(ctx, s, func(tx store.Tx) (bool, error) {
		if err := tx.Tasks().Lock(ctx, c.TaskID); err != nil {
			return false, err
		}
		t, err := tx.Tasks().Get(ctx, c.TaskID)
		if err != nil {
			return false, err
		}
		skip := func() (bool, error) {
			_, err := tx.Steering().SetCompletion(ctx, c.ID, store.SteererSkipped)
			return false, err
		}
		if t.AcceptanceID != nil {
			return skip()
		}
		if t.StateRevision <= c.FinalStateRevision {
			return false, nil
		}
		switch t.State {
		case StateFailed, StateInterrupted, StateCancelled:
			return skip()
		case StateDone:
		default:
			return false, nil
		}
		p, err := completionPreview(ctx, tx, c.TaskID)
		if err != nil || p.BlockedReason != "" {
			return false, err
		}
		decided, err := tx.Steering().SetCompletion(ctx, c.ID, store.SteererCompleted)
		if err != nil || !decided {
			return false, err
		}
		acceptanceID, err := insertAcceptance(ctx, tx, c.TaskID, "user", p.Evidence, "")
		if err != nil {
			return false, err
		}
		return true, recordAcceptance(ctx, tx, c.TaskID, p.Revision+1, &acceptanceID, "task_completed", SteererAutoCompletedDetail(c.TreeID))
	})
}
