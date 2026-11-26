package db

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

// HarnessSyncStatus is the persisted sync bookkeeping of a machine and harness.
type HarnessSyncStatus = store.HarnessSyncStatus

// HarnessSettings returns the public settings object, or {} when absent.
func (s *Store) HarnessSettings(ctx context.Context, machine int64, kind string) (json.RawMessage, error) {
	return read(ctx, s, func(tx store.Tx) (json.RawMessage, error) {
		return tx.HarnessSetup().Settings(ctx, machine, kind)
	})
}

// SaveHarnessSettings merges the supplied keys into the public settings.
// A supplied JSON null is stored as a value.
func (s *Store) SaveHarnessSettings(ctx context.Context, machine int64, kind string, patch json.RawMessage) error {
	return s.saveHarnessSettings(ctx, machine, kind, patch, nil)
}

// SaveHarnessSettingsAndSecret merges settings and replaces a secret in one
// transaction. A nil value deletes the secret.
func (s *Store) SaveHarnessSettingsAndSecret(ctx context.Context, machine int64, kind string, patch json.RawMessage, key string, value json.RawMessage) error {
	if value != nil && !json.Valid(value) {
		return Invalid("harness secret must be valid JSON")
	}
	return s.saveHarnessSettings(ctx, machine, kind, patch, func(setup store.HarnessSetup) error {
		if value == nil {
			return setup.DeleteSecret(ctx, machine, kind, key)
		}
		return setup.PutSecret(ctx, machine, kind, key, value)
	})
}

func (s *Store) saveHarnessSettings(ctx context.Context, machine int64, kind string, patch json.RawMessage, saveSecret func(store.HarnessSetup) error) error {
	var updates map[string]json.RawMessage
	if err := json.Unmarshal(patch, &updates); err != nil || updates == nil {
		return Invalid("harness settings patch must be a JSON object")
	}
	return s.e.Write(ctx, func(tx store.Tx) error {
		if err := tx.Machines().Lock(ctx, machine); err != nil {
			return err
		}
		setup := tx.HarnessSetup()
		current, err := setup.Settings(ctx, machine, kind)
		if err != nil {
			return err
		}
		var settings map[string]json.RawMessage
		if err := json.Unmarshal(current, &settings); err != nil {
			return err
		}
		maps.Copy(settings, updates)
		merged, err := json.Marshal(settings)
		if err != nil {
			return err
		}
		if err := setup.PutSettings(ctx, machine, kind, merged); err != nil {
			return err
		}
		if saveSecret != nil {
			return saveSecret(setup)
		}
		return nil
	})
}

// HarnessSecret returns the opaque JSON value for key, or nil when absent.
func (s *Store) HarnessSecret(ctx context.Context, machine int64, kind, key string) (json.RawMessage, error) {
	return read(ctx, s, func(tx store.Tx) (json.RawMessage, error) {
		return tx.HarnessSetup().Secret(ctx, machine, kind, key)
	})
}

// SetHarnessSecret replaces the JSON value of one secret.
func (s *Store) SetHarnessSecret(ctx context.Context, machine int64, kind, key string, value json.RawMessage) error {
	if !json.Valid(value) {
		return Invalid("harness secret must be valid JSON")
	}
	return s.e.Write(ctx, func(tx store.Tx) error {
		return tx.HarnessSetup().PutSecret(ctx, machine, kind, key, value)
	})
}

// DeleteHarnessSecret removes a secret if present.
func (s *Store) DeleteHarnessSecret(ctx context.Context, machine int64, kind, key string) error {
	return s.e.Write(ctx, func(tx store.Tx) error {
		return tx.HarnessSetup().DeleteSecret(ctx, machine, kind, key)
	})
}

// HarnessSyncStatus returns the last error and successful full sync, if recorded.
func (s *Store) HarnessSyncStatus(ctx context.Context, machine int64, kind string) (HarnessSyncStatus, error) {
	return read(ctx, s, func(tx store.Tx) (HarnessSyncStatus, error) {
		return tx.HarnessSetup().SyncStatus(ctx, machine, kind)
	})
}

// SetHarnessLastError records a sync error. An empty message clears it.
func (s *Store) SetHarnessLastError(ctx context.Context, machine int64, kind, msg string) error {
	return s.e.Write(ctx, func(tx store.Tx) error {
		return tx.HarnessSetup().SetLastError(ctx, machine, kind, nullIfEmpty(msg))
	})
}

// SetHarnessLastFullSync records the time of a successful full sync.
func (s *Store) SetHarnessLastFullSync(ctx context.Context, machine int64, kind string, at time.Time) error {
	return s.e.Write(ctx, func(tx store.Tx) error {
		return tx.HarnessSetup().SetLastFullSync(ctx, machine, kind, at)
	})
}

// ActiveConnectionIdentity returns the active connection ID and identity in a
// single read. Both values are zero when there is no active connection.
func (s *Store) ActiveConnectionIdentity(ctx context.Context, machine int64, kind string) (int64, string, error) {
	conn, err := read(ctx, s, func(tx store.Tx) (store.ActiveConnectionRow, error) {
		return tx.Connections().Active(ctx, machine, kind)
	})
	if errors.Is(err, ErrNotFound) {
		return 0, "", nil
	}
	return conn.ID, conn.Identity, err
}
