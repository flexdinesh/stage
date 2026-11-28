package db

import (
	"context"
	"slices"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

// Preferences: DefaultConnectionID is the connection new tasks start on (nil =
// none, or its connection is no longer active). Each connection pins its own
// new-task model (HarnessConnection.NewTaskConfig). NotifyEvents controls
// which events notify the acting user.
type Preferences struct {
	DefaultConnectionID *int64              `json:"defaultConnectionId"`
	NotifyEvents        []NotificationEvent `json:"notifyEvents"`
}

// eventList names the valid events for errors.
var eventList = func() string {
	names := make([]string, len(NotificationEvents))
	for i, e := range NotificationEvents {
		names[i] = string(e)
	}
	return strings.Join(names, ", ")
}()

func (s *Store) GetPreferences(ctx context.Context) (Preferences, error) {
	return read(ctx, s, func(tx store.Tx) (Preferences, error) { return getPreferences(ctx, tx) })
}

func getPreferences(ctx context.Context, tx store.Tx) (Preferences, error) {
	row, err := tx.Preferences().Get(ctx, ActingUser(ctx))
	p := Preferences{DefaultConnectionID: row.DefaultConnectionID}
	// Display order (migrations append new events at the end). No saved
	// filter reads as the default one.
	p.NotifyEvents = make([]NotificationEvent, 0, len(NotificationEvents))
	for _, e := range NotificationEvents {
		if row.NotifyEvents == nil && slices.Contains(DefaultNotifyEvents, e) || slices.Contains(row.NotifyEvents, string(e)) {
			p.NotifyEvents = append(p.NotifyEvents, e)
		}
	}
	return p, err
}

// SetNotifyEvents stores the notification filter (deduplicated, display
// order). ErrInvalid for a state that cannot notify.
func (s *Store) SetNotifyEvents(ctx context.Context, events []NotificationEvent) (Preferences, error) {
	for _, e := range events {
		if e == "settled" {
			return Preferences{}, Invalid("event settled was removed in API v9; use completed (want %s)", eventList)
		}
		if !slices.Contains(NotificationEvents, e) {
			return Preferences{}, Invalid("invalid event %q (want %s)", e, eventList)
		}
	}
	stored := make([]string, 0, len(events))
	for _, e := range NotificationEvents {
		if slices.Contains(events, e) {
			stored = append(stored, string(e))
		}
	}
	return write(ctx, s, func(tx store.Tx) (Preferences, error) {
		if err := tx.Preferences().SetNotifyEvents(ctx, ActingUser(ctx), stored); err != nil {
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
