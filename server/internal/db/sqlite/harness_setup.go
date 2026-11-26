package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

type harnessSetup struct{ *runner }

func (t *tx) HarnessSetup() store.HarnessSetup { return harnessSetup{t.r} }

func (r harnessSetup) Settings(ctx context.Context, machine int64, kind string) (json.RawMessage, error) {
	var settings string
	err := r.QueryRow(ctx, `SELECT settings FROM harness_settings WHERE machine_id=$1 AND kind=$2`, machine, kind).Scan(&settings)
	if errors.Is(err, sql.ErrNoRows) {
		return json.RawMessage("{}"), nil
	}
	return json.RawMessage(settings), err
}

func (r harnessSetup) PutSettings(ctx context.Context, machine int64, kind string, settings json.RawMessage) error {
	_, err := r.Exec(ctx, `INSERT INTO harness_settings(machine_id,kind,settings,updated_at) VALUES($1,$2,$3,`+nowSQL+`)
 ON CONFLICT(machine_id,kind) DO UPDATE SET settings=excluded.settings,updated_at=excluded.updated_at`, machine, kind, string(settings))
	return err
}

func (r harnessSetup) Secret(ctx context.Context, machine int64, kind, key string) (json.RawMessage, error) {
	var value string
	err := r.QueryRow(ctx, `SELECT value FROM harness_secrets WHERE machine_id=$1 AND kind=$2 AND key=$3`, machine, kind, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return json.RawMessage(value), err
}

func (r harnessSetup) PutSecret(ctx context.Context, machine int64, kind, key string, value json.RawMessage) error {
	_, err := r.Exec(ctx, `INSERT INTO harness_secrets(machine_id,kind,key,value) VALUES($1,$2,$3,$4)
 ON CONFLICT(machine_id,kind,key) DO UPDATE SET value=excluded.value`, machine, kind, key, string(value))
	return err
}

func (r harnessSetup) DeleteSecret(ctx context.Context, machine int64, kind, key string) error {
	_, err := r.Exec(ctx, `DELETE FROM harness_secrets WHERE machine_id=$1 AND kind=$2 AND key=$3`, machine, kind, key)
	return err
}

func (r harnessSetup) SyncStatus(ctx context.Context, machine int64, kind string) (store.HarnessSyncStatus, error) {
	var status store.HarnessSyncStatus
	err := r.QueryRow(ctx, `SELECT last_error,last_full_sync_at FROM harness_sync_status WHERE machine_id=$1 AND kind=$2`, machine, kind).Scan(&status.LastError, &status.LastFullSyncAt)
	if errors.Is(err, sql.ErrNoRows) {
		return status, nil
	}
	return status, err
}

func (r harnessSetup) SetLastError(ctx context.Context, machine int64, kind string, message *string) error {
	_, err := r.Exec(ctx, `INSERT INTO harness_sync_status(machine_id,kind,last_error) VALUES($1,$2,$3)
 ON CONFLICT(machine_id,kind) DO UPDATE SET last_error=excluded.last_error`, machine, kind, message)
	return err
}

func (r harnessSetup) SetLastFullSync(ctx context.Context, machine int64, kind string, at time.Time) error {
	_, err := r.Exec(ctx, `INSERT INTO harness_sync_status(machine_id,kind,last_full_sync_at) VALUES($1,$2,$3)
 ON CONFLICT(machine_id,kind) DO UPDATE SET last_full_sync_at=excluded.last_full_sync_at`, machine, kind, at)
	return err
}
