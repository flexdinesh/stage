package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// T3 setup is per machine (migration 0018): the t3_connection row of a
// machine serves its active T3 connection. A machine with no row reads
// defaults; writes create it.

// LoadToken reads machine's T3 token (zero value when none stored).
func (s *Store) LoadToken(ctx context.Context, machine int64) (execution.Credential, error) {
	var clientID, token *string
	var expires *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT client_id, token, expires_at FROM t3_connection WHERE machine_id = $1`, machine,
	).Scan(&clientID, &token, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return execution.Credential{}, nil
	}
	if err != nil {
		return execution.Credential{}, err
	}
	var out execution.Credential
	if clientID != nil {
		out.ClientID = *clientID
	}
	if token != nil {
		out.Token = *token
	}
	if expires != nil {
		out.ExpiresAt = *expires
	}
	return out, nil
}

// SaveToken saves machine's T3 token.
func (s *Store) SaveToken(ctx context.Context, machine int64, tok execution.Credential) error {
	var expires *time.Time
	if !tok.ExpiresAt.IsZero() {
		expires = &tok.ExpiresAt
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO t3_connection(machine_id, client_id, token, expires_at) VALUES($1, $2, $3, $4)
 ON CONFLICT(machine_id) DO UPDATE SET client_id = excluded.client_id, token = excluded.token, expires_at = excluded.expires_at, updated_at = `+nowSQL,
		machine, nullIfEmpty(tok.ClientID), nullIfEmpty(tok.Token), expires)
	return err
}

// SetLastError records machine's T3 connection error ("" clears).
func (s *Store) SetLastError(ctx context.Context, machine int64, msg string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO t3_connection(machine_id, last_error) VALUES($1, $2)
 ON CONFLICT(machine_id) DO UPDATE SET last_error = excluded.last_error, updated_at = `+nowSQL, machine, nullIfEmpty(msg))
	return err
}

// Connection is the non-secret part of a machine's t3_connection row.
type Connection struct {
	LastError      *string
	LastFullSyncAt *time.Time
	// T3URL is the T3 URL saved in Settings (nil = default).
	T3URL *string
	// EnvironmentID is the identity of the machine's active T3 connection
	// (nil until the first successful pairing).
	EnvironmentID *string
	// ConnectionID is the machine's active T3 connection.
	ConnectionID int64
}

// GetConnection reads machine's T3 setup.
func (s *Store) GetConnection(ctx context.Context, machine int64) (Connection, error) {
	var c Connection
	err := s.pool.QueryRow(ctx,
		`SELECT (SELECT last_error FROM t3_connection WHERE machine_id=$1), (SELECT last_full_sync_at FROM t3_connection WHERE machine_id=$1), (SELECT t3_url FROM t3_connection WHERE machine_id=$1),
 (SELECT NULLIF(identity,'') FROM harness_connections WHERE machine_id=$1 AND harness='t3' AND active=1),
 COALESCE((SELECT id FROM harness_connections WHERE machine_id=$1 AND harness='t3' AND active=1),0)`, machine,
	).Scan(&c.LastError, &c.LastFullSyncAt, &c.T3URL, &c.EnvironmentID, &c.ConnectionID)
	return c, err
}

// SetT3URL saves machine's T3 URL setting ("" clears it: back to the default).
func (s *Store) SetT3URL(ctx context.Context, machine int64, u string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO t3_connection(machine_id, t3_url) VALUES($1, $2)
 ON CONFLICT(machine_id) DO UPDATE SET t3_url = excluded.t3_url, updated_at = `+nowSQL, machine, nullIfEmpty(u))
	return err
}

// SetLastFullSync records machine's last successful T3 full sync.
func (s *Store) SetLastFullSync(ctx context.Context, machine int64, at time.Time) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO t3_connection(machine_id, last_full_sync_at) VALUES($1, $2)
 ON CONFLICT(machine_id) DO UPDATE SET last_full_sync_at = excluded.last_full_sync_at, updated_at = `+nowSQL, machine, at)
	return err
}
