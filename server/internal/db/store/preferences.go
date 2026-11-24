package store

import (
	"context"
	"time"
)

// PreferencesRow is the stored preferences row: the default connection
// (nil when unset or no longer active) and the notify events as stored.
type PreferencesRow struct {
	DefaultConnectionID *int64
	NotifyEvents        []string
}

// Preferences is the preferences repository (one row).
type Preferences interface {
	Get(ctx context.Context) (PreferencesRow, error)
	SetNotifyEvents(ctx context.Context, events []string) error
	SetDefaultConnection(ctx context.Context, id *int64) error
}

// User is a person who creates work. Sign-in identities (identities table)
// map to a user later; machine tokens are machine principals, never users.
type User struct {
	ID          int64     `json:"id"`
	DisplayName string    `json:"displayName"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Users is the users repository.
type Users interface {
	Get(ctx context.Context, id int64) (User, error)
}
