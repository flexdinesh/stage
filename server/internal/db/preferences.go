package db

import (
	"context"
	"slices"
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
// needs all settled. A pseudo-state: never a task's state, not in TaskStates.
const EventReady TaskState = "ready"

// NotificationEvents: the events that can notify, in display order.
var NotificationEvents = []TaskState{StateNeedsInput, StateFailed, EventReady, StateDone, StateSettled}

func (s *Store) GetPreferences(ctx context.Context) (Preferences, error) {
	var p Preferences
	var events []string
	// A retired default (its machine removed, or replaced without a successor) reads as none.
	err := s.pool.QueryRow(ctx, `SELECT (SELECT c.id FROM harness_connections c WHERE c.id=p.default_connection_id AND c.active=1), notify_events FROM preferences p WHERE id = 1`).
		Scan(&p.DefaultConnectionID, &events)
	// Display order (migrations append new events at the end).
	p.NotifyEvents = make([]TaskState, 0, len(events))
	for _, e := range NotificationEvents {
		if slices.Contains(events, string(e)) {
			p.NotifyEvents = append(p.NotifyEvents, e)
		}
	}
	return p, err
}

// SetNotifyEvents stores the notification filter (deduplicated, display
// order). ErrInvalid for a state that cannot notify.
func (s *Store) SetNotifyEvents(ctx context.Context, events []TaskState) (Preferences, error) {
	for _, e := range events {
		if !slices.Contains(NotificationEvents, e) {
			return Preferences{}, Invalid("invalid event %q (want needs_input, failed, ready, done or settled)", e)
		}
	}
	stored := make([]string, 0, len(events))
	for _, e := range NotificationEvents {
		if slices.Contains(events, e) {
			stored = append(stored, string(e))
		}
	}
	if _, err := s.pool.Exec(ctx, `UPDATE preferences SET notify_events = $1, updated_at = `+nowSQL+` WHERE id = 1`, stored); err != nil {
		return Preferences{}, err
	}
	return s.GetPreferences(ctx)
}

// SetDefaultConnection makes an active connection the one new tasks start on
// (nil clears it). ErrInvalid for an unknown or replaced connection.
func (s *Store) SetDefaultConnection(ctx context.Context, id *int64) (Preferences, error) {
	if id != nil && !s.ConnectionActive(*id) {
		return Preferences{}, Invalid("Only an active connection can be the default.")
	}
	if _, err := s.pool.Exec(ctx, `UPDATE preferences SET default_connection_id = $1, updated_at = `+nowSQL+` WHERE id = 1`, id); err != nil {
		return Preferences{}, err
	}
	return s.GetPreferences(ctx)
}
