package db

import (
	"context"
	"time"

	"github.com/flexdinesh/stage/server/internal/t3"
)

var _ t3.TokenStore = (*Store)(nil)

// LoadToken implements t3.TokenStore (zero value when none stored).
func (s *Store) LoadToken(ctx context.Context) (t3.StoredToken, error) {
	var clientID, token *string
	var expires *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT client_id, token, expires_at FROM t3_connection WHERE id = 1`,
	).Scan(&clientID, &token, &expires)
	if err != nil {
		return t3.StoredToken{}, err
	}
	var out t3.StoredToken
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

// SaveToken implements t3.TokenStore.
func (s *Store) SaveToken(ctx context.Context, tok t3.StoredToken) error {
	var expires *time.Time
	if !tok.ExpiresAt.IsZero() {
		expires = &tok.ExpiresAt
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE t3_connection SET client_id = $1, token = $2, expires_at = $3, updated_at = now() WHERE id = 1`,
		nullIfEmpty(tok.ClientID), nullIfEmpty(tok.Token), expires)
	return err
}

// SetLastError implements t3.TokenStore ("" clears).
func (s *Store) SetLastError(ctx context.Context, msg string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE t3_connection SET last_error = $1, updated_at = now() WHERE id = 1`, nullIfEmpty(msg))
	return err
}

// Connection is the non-secret part of the t3_connection row.
type Connection struct {
	LastError      *string
	LastFullSyncAt *time.Time
}

func (s *Store) GetConnection(ctx context.Context) (Connection, error) {
	var c Connection
	err := s.pool.QueryRow(ctx,
		`SELECT last_error, last_full_sync_at FROM t3_connection WHERE id = 1`,
	).Scan(&c.LastError, &c.LastFullSyncAt)
	return c, err
}

func (s *Store) SetLastFullSync(ctx context.Context, at time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE t3_connection SET last_full_sync_at = $1, updated_at = now() WHERE id = 1`, at)
	return err
}
