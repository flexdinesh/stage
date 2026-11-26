package db

import (
	"context"
	"slices"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

// Preferences: DefaultConnectionID is the connection new tasks start on (nil =
// none, or its connection is no longer active). Each connection pins its own
// new-task model (HarnessConnection.NewTaskConfig). NotifyEvents: events that
// notify (inbox + push); others are dropped.
type Preferences struct {
	DefaultConnectionID *int64      `json:"defaultConnectionId"`
	NotifyEvents        []TaskState `json:"notifyEvents"`
}

// EventReady is the ready notification event: a manual tree member whose
// needs all accepted. A pseudo-state: never a task's state, not in TaskStates.
const EventReady TaskState = "ready"

// NotificationEvents: the events that can notify, in display order.
var NotificationEvents = []TaskState{StateNeedsInput, StateFailed, EventReady, StateDone}

func (s *Store) GetPreferences(ctx context.Context) (Preferences, error) {
	return read(ctx, s, func(tx store.Tx) (Preferences, error) { return getPreferences(ctx, tx) })
}

func getPreferences(ctx context.Context, tx store.Tx) (Preferences, error) {
	row, err := tx.Preferences().Get(ctx)
	p := Preferences{DefaultConnectionID: row.DefaultConnectionID}
	// Display order (migrations append new events at the end).
	p.NotifyEvents = make([]TaskState, 0, len(row.NotifyEvents))
	for _, e := range NotificationEvents {
		if slices.Contains(row.NotifyEvents, string(e)) {
			p.NotifyEvents = append(p.NotifyEvents, e)
		}
	}
	return p, err
}

// SetNotifyEvents stores the notification filter (deduplicated, display
// order). ErrInvalid for a state that cannot notify.
func (s *Store) SetNotifyEvents(ctx context.Context, events []TaskState) (Preferences, error) {
	for _, e := range events {
		if e == "settled" {
			return Preferences{}, Invalid("event settled was removed in API v9; completion sends no notification (want needs_input, failed, ready or done)")
		}
		if !slices.Contains(NotificationEvents, e) {
			return Preferences{}, Invalid("invalid event %q (want needs_input, failed, ready or done)", e)
		}
	}
	stored := make([]string, 0, len(events))
	for _, e := range NotificationEvents {
		if slices.Contains(events, e) {
			stored = append(stored, string(e))
		}
	}
	return write(ctx, s, func(tx store.Tx) (Preferences, error) {
		if err := tx.Preferences().SetNotifyEvents(ctx, stored); err != nil {
			return Preferences{}, err
		}
		return getPreferences(ctx, tx)
	})
}

// SetDefaultConnection makes an active connection the one new tasks start on
// (nil clears it). ErrInvalid for an unknown or replaced connection.
func (s *Store) SetDefaultConnection(ctx context.Context, id *int64) (Preferences, error) {
	if id != nil && !s.ConnectionActive(*id) {
		return Preferences{}, Invalid("Only an active connection can be the default.")
	}
	return write(ctx, s, func(tx store.Tx) (Preferences, error) {
		if err := tx.Preferences().SetDefaultConnection(ctx, id); err != nil {
			return Preferences{}, err
		}
		return getPreferences(ctx, tx)
	})
}
