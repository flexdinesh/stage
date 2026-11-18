package db

import (
	"context"
	"slices"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// ModelDefaults is a new-task model preset (pinned or last used).
// ProviderInstanceID / Model nil = T3 default; set together.
type ModelDefaults struct {
	ProviderInstanceID *string                 `json:"providerInstanceId"`
	Model              *string                 `json:"model"`
	ModelOptions       []execution.ModelOption `json:"modelOptions"`
	RuntimeMode        string                  `json:"runtimeMode"`
	InteractionMode    string                  `json:"interactionMode"`
}

// Preferences: new-task prefill is Pinned, else LastUsed. NotifyEvents:
// events that notify (inbox + push); others are dropped.
type Preferences struct {
	Pinned       *ModelDefaults `json:"pinned"`
	LastUsed     *ModelDefaults `json:"lastUsed"`
	NotifyEvents []TaskState    `json:"notifyEvents"`
}

// EventReady is the ready notification event: a manual tree member whose
// needs all settled. A pseudo-state: never a task's state, not in TaskStates.
const EventReady TaskState = "ready"

// NotificationEvents: the events that can notify, in display order.
var NotificationEvents = []TaskState{StateNeedsInput, StateFailed, EventReady, StateDone, StateSettled}

func (s *Store) GetPreferences(ctx context.Context) (Preferences, error) {
	var p Preferences
	var events []string
	err := s.pool.QueryRow(ctx, `SELECT pinned, last_used, notify_events FROM preferences WHERE id = 1`).
		Scan(&p.Pinned, &p.LastUsed, &events)
	// Display order (migrations append new events at the end).
	p.NotifyEvents = make([]TaskState, 0, len(events))
	for _, e := range NotificationEvents {
		if slices.Contains(events, string(e)) {
			p.NotifyEvents = append(p.NotifyEvents, e)
		}
	}
	return normalize(p), err
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

// SetPinned stores (or clears, nil) the pinned defaults.
func (s *Store) SetPinned(ctx context.Context, d *ModelDefaults) (Preferences, error) {
	if _, err := s.pool.Exec(ctx, `UPDATE preferences SET pinned = $1, updated_at = `+nowSQL+` WHERE id = 1`, d); err != nil {
		return Preferences{}, err
	}
	return s.GetPreferences(ctx)
}

// SetLastUsed records the model config of the latest created task.
func (s *Store) SetLastUsed(ctx context.Context, d ModelDefaults) error {
	d.ModelOptions = nonNilOptions(d.ModelOptions)
	_, err := s.pool.Exec(ctx, `UPDATE preferences SET last_used = $1, updated_at = `+nowSQL+` WHERE id = 1`, d)
	return err
}

func normalize(p Preferences) Preferences {
	for _, d := range []*ModelDefaults{p.Pinned, p.LastUsed} {
		if d != nil {
			d.ModelOptions = nonNilOptions(d.ModelOptions)
		}
	}
	return p
}
