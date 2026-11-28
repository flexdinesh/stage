package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

// Notification hold (ADR 0013, docs/api.md "Notification rules"): a tree
// member's needs_input notification waits the tree's steering_hold_seconds
// while its steerer can answer. The notification row is inserted only at
// release, because the SSE cursor is id-ordered. notify.Holds releases.

type (
	TaskHold  = store.TaskHold
	HeldHold  = store.HeldHold
	HoldState = store.HoldState
)

const (
	HoldHeld      = store.HoldHeld
	HoldReleased  = store.HoldReleased
	HoldCancelled = store.HoldCancelled
	HoldEscalated = store.HoldEscalated
)

// MaxSteeringHold bounds a tree's steeringHoldSeconds.
const MaxSteeringHold = 3600

// MaxEscalationReason bounds escalate_to_user's reason (characters).
const MaxEscalationReason = 500

// Hold release reasons (notification_holds.reason) other than an
// escalation's text.
const (
	HoldReasonAnswered        = "answered"
	HoldReasonStateChanged    = "state_changed"
	HoldReasonSteererEnded    = "steerer_ended"
	HoldReasonSteererInactive = "steerer_inactive"
	HoldReasonLeftTree        = "left_tree"
	HoldReasonUnanswerable    = "unanswerable"
)

// Hold notes: the inbox and push text of a held notification released by
// escalation.
const HoldNoteTimeout = "Steerer did not answer"

// HoldNoteEscalated is an escalation's note.
func HoldNoteEscalated(reason string) string { return "Steerer: " + reason }

// SteererCanAnswer: steerer states that keep a hold (it is live).
func SteererCanAnswer(s TaskState) bool {
	return s == StateRunning || s == StateDone || s == StateNeedsInput
}

// NotSteererError: the caller is not the active steerer of the task's tree
// (403 not_steerer).
type NotSteererError struct{ Msg string }

func (e *NotSteererError) Error() string { return e.Msg }

func notSteerer(task int64) error {
	return &NotSteererError{Msg: fmt.Sprintf("Only the active steerer of task %d's tree can escalate it.", task)}
}

// NotWaiting: escalate_to_user on a task that holds nothing and does not
// need input (409 not_waiting).
func NotWaiting(task int64, state TaskState) error {
	return &ConflictError{Code: "not_waiting", Msg: fmt.Sprintf("Task %d does not need input (state %s); nothing to escalate.", task, state)}
}

// NotificationRecord builds the notification a released hold inserts, in
// the release's transaction (tx joins it); nil inserts none (the personal
// filter mutes needs_input).
type NotificationRecord func(ctx context.Context, tx *Store, t Task) (*NewNotification, error)

// HoldRelease is what releasing or escalating did.
type HoldRelease struct {
	// State: the hold's final state (cancelled when the wait ended first).
	State HoldState
	// Notification is the inserted row (nil: none); NotificationID its id.
	Notification   *NewNotification
	NotificationID int64
}

// HoldNeedsInput holds task id's needs_input notification when its tree's
// steerer can answer it (store conditions; the member connection's respond
// or send is checked by the release loop) and reports whether it is held.
// Joins the enclosing observation transaction on a scoped Store.
func (s *Store) HoldNeedsInput(ctx context.Context, id int64, now time.Time) (bool, error) {
	return write(ctx, s, func(tx store.Tx) (bool, error) {
		if err := tx.Tasks().Lock(ctx, id); err != nil {
			return false, err
		}
		t, err := tx.Tasks().Get(ctx, id)
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if t.State != StateNeedsInput || t.TreeID == nil || answeredAt(t, t.StateRevision) {
			return false, nil
		}
		hold, err := tx.Trees().SteeringHold(ctx, *t.TreeID)
		if err != nil || hold <= 0 {
			return false, err
		}
		steerer, err := tx.Steering().Active(ctx, *t.TreeID)
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		st, err := tx.Tasks().Get(ctx, steerer.TaskID)
		if err != nil {
			return false, err
		}
		if !SteererCanAnswer(st.State) {
			return false, nil
		}
		h, err := tx.Holds().HeldForTask(ctx, id)
		switch {
		case err == nil && h.StateRevision == t.StateRevision:
			return true, nil
		case err == nil:
			if err := tx.Holds().CancelTask(ctx, id, HoldReasonStateChanged, nil); err != nil {
				return false, err
			}
		case !errors.Is(err, ErrNotFound):
			return false, err
		}
		_, err = tx.Holds().Insert(ctx, store.NewHold{TaskID: id, StateRevision: t.StateRevision, SteererID: steerer.ID,
			HeldAt: now, Until: now.Add(time.Duration(hold) * time.Second), Attribution: WorkAttribution(ctx)})
		return err == nil, err
	})
}

// HeldHolds lists every held notification, oldest first.
func (s *Store) HeldHolds(ctx context.Context) ([]HeldHold, error) {
	return read(ctx, s, func(tx store.Tx) ([]HeldHold, error) { return tx.Holds().Held(ctx) })
}

// ReleaseHold ends held hold id as outcome (released or escalated) with
// reason, inserting record's notification with note, when its task still
// needs input at the held revision unanswered; otherwise it cancels it.
// Not held anymore: nothing changes (State is its state).
func (s *Store) ReleaseHold(ctx context.Context, id int64, outcome HoldState, reason, note *string, record NotificationRecord) (HoldRelease, error) {
	return write(ctx, s, func(tx store.Tx) (HoldRelease, error) {
		h, err := tx.Holds().Get(ctx, id)
		if err != nil {
			return HoldRelease{}, err
		}
		if err := tx.Tasks().Lock(ctx, h.TaskID); err != nil {
			return HoldRelease{}, err
		}
		if h, err = tx.Holds().Get(ctx, id); err != nil {
			return HoldRelease{}, err
		}
		if h.State != HoldHeld {
			return HoldRelease{State: h.State}, nil
		}
		t, err := tx.Tasks().Get(ctx, h.TaskID)
		if err != nil {
			return HoldRelease{}, err
		}
		if cancel := holdEnded(t, h.StateRevision); cancel != "" {
			return HoldRelease{State: HoldCancelled}, finishHold(ctx, tx, h, HoldCancelled, &cancel, nil)
		}
		r := HoldRelease{State: outcome}
		if err := s.insertHeldNotification(ctx, tx, t, note, record, &r); err != nil {
			return HoldRelease{}, err
		}
		return r, finishHold(ctx, tx, h, outcome, reason, r.notificationID())
	})
}

// EscalateTask is escalate_to_user from steerer task callerTask on task id:
// it releases the task's hold at once, or notifies at once when the task
// needs input and nothing is held, noting reason, and enqueues
// hold_escalated. Only the active steerer of the task's tree may
// (*NotSteererError); a task that neither holds nor needs input is
// NotWaiting.
func (s *Store) EscalateTask(ctx context.Context, id, callerTask int64, reason string, record NotificationRecord) (HoldRelease, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return HoldRelease{}, Invalid("reason is required")
	}
	if n := len([]rune(reason)); n > MaxEscalationReason {
		return HoldRelease{}, Invalid("reason is %d characters; the limit is %d", n, MaxEscalationReason)
	}
	note := HoldNoteEscalated(reason)
	return write(ctx, s, func(tx store.Tx) (HoldRelease, error) {
		if err := tx.Tasks().Lock(ctx, id); err != nil {
			return HoldRelease{}, err
		}
		t, err := tx.Tasks().Get(ctx, id)
		if err != nil {
			return HoldRelease{}, err
		}
		if t.TreeID == nil || callerTask == 0 {
			return HoldRelease{}, notSteerer(id)
		}
		steerer, err := tx.Steering().Active(ctx, *t.TreeID)
		if errors.Is(err, ErrNotFound) || err == nil && steerer.TaskID != callerTask {
			return HoldRelease{}, notSteerer(id)
		}
		if err != nil {
			return HoldRelease{}, err
		}
		h, err := tx.Holds().HeldForTask(ctx, id)
		held := err == nil
		if err != nil && !errors.Is(err, ErrNotFound) {
			return HoldRelease{}, err
		}
		if held {
			if cancel := holdEnded(t, h.StateRevision); cancel != "" {
				if err := finishHold(ctx, tx, h, HoldCancelled, &cancel, nil); err != nil {
					return HoldRelease{}, err
				}
				held = false
			}
		}
		if !held && (t.State != StateNeedsInput || answeredAt(t, t.StateRevision)) {
			return HoldRelease{}, NotWaiting(id, t.State)
		}
		r := HoldRelease{State: HoldEscalated}
		if err := s.insertHeldNotification(ctx, tx, t, &note, record, &r); err != nil {
			return HoldRelease{}, err
		}
		if held {
			return r, finishHold(ctx, tx, h, HoldEscalated, &reason, r.notificationID())
		}
		return r, steerHoldEscalated(ctx, tx, t, &reason, false)
	})
}

// insertHeldNotification inserts record's notification for t with note in
// tx (none when record returns nil) into r.
func (s *Store) insertHeldNotification(ctx context.Context, tx store.Tx, t Task, note *string, record NotificationRecord, r *HoldRelease) error {
	scoped := s.observationStore(tx)
	rec, err := record(ctx, scoped, t)
	if err != nil || rec == nil {
		return err
	}
	rec.Note = note
	id, err := scoped.InsertNotification(ctx, *rec)
	if err != nil {
		return err
	}
	r.Notification, r.NotificationID = rec, id
	return nil
}

func (r HoldRelease) notificationID() *int64 {
	if r.Notification == nil {
		return nil
	}
	id := r.NotificationID
	return &id
}

// holdEnded is why a hold at revision no longer waits ("" : it still does).
func holdEnded(t Task, revision int64) string {
	switch {
	case t.State != StateNeedsInput || t.StateRevision != revision:
		return HoldReasonStateChanged
	case answeredAt(t, revision):
		return HoldReasonAnswered
	}
	return ""
}

func answeredAt(t Task, revision int64) bool {
	return t.AnsweredRevision != nil && *t.AnsweredRevision == revision
}
