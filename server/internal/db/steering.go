package db

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

// Tree steering (ADR 0013): a tree's steerer is an ordinary standalone task
// bound to it; while bound, the tree's changes enqueue steering events
// (journal_events.go).

type (
	SteeringEvent     = store.SteeringEvent
	SteeringEndReason = store.SteeringEndReason
	TreeSteerer       = store.TreeSteerer
	TaskSteering      = store.TaskSteering
)

const (
	SteeringReplaced     = store.SteeringReplaced
	SteeringStopped      = store.SteeringStopped
	SteeringTreeFinished = store.SteeringTreeFinished
	SteeringTaskRemoved  = store.SteeringTaskRemoved
)

// SteererInput sets a tree's steerer: a new standalone task from Task.
type SteererInput struct {
	Task    NewTask
	Replace bool
	// RequestID makes the request replayable; Digest is its content's hash
	// (the same RequestID with another Digest is 409).
	RequestID, Digest string
}

// SteererResult is a set steerer.
type SteererResult struct {
	TaskID int64
	// Replaced: the steerer task this one replaced (nil: none).
	Replaced *int64
	// Replayed: RequestID was already applied; nothing changed.
	Replayed bool
}

// SteererExists: the tree has an active steerer and the request did not
// ask to replace it (409 steerer_exists).
func SteererExists(tree, task int64) error {
	return &ConflictError{Code: "steerer_exists", Msg: fmt.Sprintf("Tree %d is already steered by #%d. Replace it or stop steering first.", tree, task)}
}

// SetTreeSteerer creates the steerer task and binds it to tree in one
// write: tree open, one active steerer (Replace ends the current one), the
// task standalone. Launching it and stopping a replaced session happen
// after (coordinator).
func (s *Store) SetTreeSteerer(ctx context.Context, tree int64, in SteererInput) (SteererResult, error) {
	var r SteererResult
	if strings.TrimSpace(in.RequestID) == "" {
		return r, Invalid("requestId is required")
	}
	in.Task.TreeID, in.Task.Needs, in.Task.NeedsSession = nil, nil, nil
	ctx = WithWorkCause(ctx, Cause{Kind: "steering_request", RequestID: in.RequestID, Session: CallerRef(ctx)})
	err := s.e.Write(ctx, func(tx store.Tx) error {
		// Write may rerun fn (Postgres retry): start each run from scratch.
		r = SteererResult{}
		if err := tx.Projects().LockIdentity(ctx); err != nil {
			return err
		}
		if err := tx.Trees().Lock(ctx, tree); err != nil {
			return err
		}
		if err := treeExists(ctx, tx, tree); err != nil {
			return err
		}
		st := tx.Steering()
		previous, err := st.Request(ctx, tree, in.RequestID)
		if err == nil {
			if previous.Digest != in.Digest {
				return Conflict("requestId already used with different content")
			}
			if previous.TaskID == nil {
				return Conflict("The steerer task of requestId %q was deleted.", in.RequestID)
			}
			r.TaskID, r.Replayed = *previous.TaskID, true
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		if err := treeOpen(ctx, tx, tree); err != nil {
			return err
		}
		task := in.Task
		if err := connectionUsable(ctx, tx, task.ConnectionID); err != nil {
			return err
		}
		if task, err = taskCreationDestination(ctx, tx, task); err != nil {
			return err
		}
		current, err := st.Active(ctx, tree)
		switch {
		case err == nil && !in.Replace:
			return SteererExists(tree, current.TaskID)
		case err == nil:
			if err := tx.Tasks().Lock(ctx, current.TaskID); err != nil {
				return err
			}
			if err := endSteerer(ctx, tx, current, store.SteeringReplaced); err != nil {
				return err
			}
			replaced := current.TaskID
			r.Replaced = &replaced
		case !errors.Is(err, ErrNotFound):
			return err
		}
		r.TaskID, err = s.insertTask(ctx, tx, task, func(tx store.Tx, id int64) error {
			_, err := bindSteerer(ctx, tx, tree, id, in.RequestID, in.Digest, r.Replaced)
			return err
		})
		return err
	})
	if err != nil && s.e.Errors().IsUniqueViolation(err) {
		return SteererResult{}, &ConflictError{Code: "steerer_exists", Msg: fmt.Sprintf("Tree %d got a steerer meanwhile. Refresh it.", tree)}
	}
	return r, err
}

// NoSteerer: the tree has no active steerer (409 no_steerer).
func NoSteerer(tree int64) error {
	return &ConflictError{Code: "no_steerer", Msg: fmt.Sprintf("Tree %d has no active steerer.", tree)}
}

// EndTreeSteering ends tree's active binding with reason and returns the
// steerer task id (stopping its session happens after, coordinator).
func (s *Store) EndTreeSteering(ctx context.Context, tree int64, reason SteeringEndReason) (int64, error) {
	return write(ctx, s, func(tx store.Tx) (int64, error) {
		if err := tx.Trees().Lock(ctx, tree); err != nil {
			return 0, err
		}
		if err := treeExists(ctx, tx, tree); err != nil {
			return 0, err
		}
		current, err := tx.Steering().Active(ctx, tree)
		if errors.Is(err, ErrNotFound) {
			return 0, NoSteerer(tree)
		}
		if err != nil {
			return 0, err
		}
		if err := tx.Tasks().Lock(ctx, current.TaskID); err != nil {
			return 0, err
		}
		return current.TaskID, endSteerer(ctx, tx, current, reason)
	})
}

// SteeringEvents are tree's steering events after id after, ascending, at
// most limit; more reports whether later ones exist.
func (s *Store) SteeringEvents(ctx context.Context, tree, after int64, limit int) (events []SteeringEvent, more bool, err error) {
	err = s.e.Read(ctx, func(tx store.Tx) error {
		if err := treeExists(ctx, tx, tree); err != nil {
			return err
		}
		var err error
		if events, err = tx.Steering().Events(ctx, tree, after, limit+1); err != nil {
			return err
		}
		if more = len(events) > limit; more {
			events = events[:limit]
		}
		return nil
	})
	return events, more, err
}
