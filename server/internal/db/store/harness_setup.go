package store

import (
	"context"
	"encoding/json"
	"time"
)

// HarnessSyncStatus is the persisted sync bookkeeping of a machine and harness.
type HarnessSyncStatus struct {
	LastError      *string
	LastFullSyncAt *time.Time
}

// HarnessSetup holds public settings, private secrets, and sync bookkeeping
// independently from the lifetime of a connection.
type HarnessSetup interface {
	Settings(ctx context.Context, machine int64, kind string) (json.RawMessage, error)
	PutSettings(ctx context.Context, machine int64, kind string, settings json.RawMessage) error
	Secret(ctx context.Context, machine int64, kind, key string) (json.RawMessage, error)
	PutSecret(ctx context.Context, machine int64, kind, key string, value json.RawMessage) error
	DeleteSecret(ctx context.Context, machine int64, kind, key string) error
	SyncStatus(ctx context.Context, machine int64, kind string) (HarnessSyncStatus, error)
	SetLastError(ctx context.Context, machine int64, kind string, message *string) error
	SetLastFullSync(ctx context.Context, machine int64, kind string, at time.Time) error
}
