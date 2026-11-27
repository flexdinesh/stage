package store

import (
	"context"
	"time"
)

// PreferencesRow holds the active default connection and the user's stored
// notification filter (nil when the user has never set a filter).
type PreferencesRow struct {
	DefaultConnectionID *int64
	NotifyEvents        []string
}

// Preferences stores the installation default connection and per-user notification filters.
type Preferences interface {
	Get(ctx context.Context, userID int64) (PreferencesRow, error)
	SetNotifyEvents(ctx context.Context, userID int64, events []string) error
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
