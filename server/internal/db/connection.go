package db

import (
	"context"
	"errors"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// T3 setup is per machine (migration 0018): the t3_connection row of a
// machine serves its active T3 connection. A machine with no row reads
// defaults; writes create it.

// LoadToken reads machine's T3 token (zero value when none stored).
func (s *Store) LoadToken(ctx context.Context, machine int64) (execution.Credential, error) {
	c, err := read(ctx, s, func(tx store.Tx) (execution.Credential, error) { return tx.Connections().T3Credential(ctx, machine) })
	if errors.Is(err, ErrNotFound) {
		return execution.Credential{}, nil
	}
	return c, err
}

// SaveToken saves machine's T3 token.
func (s *Store) SaveToken(ctx context.Context, machine int64, tok execution.Credential) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Connections().SaveT3Credential(ctx, machine, tok) })
}

// SetLastError records machine's T3 connection error ("" clears).
func (s *Store) SetLastError(ctx context.Context, machine int64, msg string) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Connections().SetT3LastError(ctx, machine, msg) })
}

// Connection is the non-secret part of a machine's t3_connection row.
type Connection = store.T3Connection

// GetConnection reads machine's T3 setup.
func (s *Store) GetConnection(ctx context.Context, machine int64) (Connection, error) {
	return read(ctx, s, func(tx store.Tx) (Connection, error) { return tx.Connections().T3Settings(ctx, machine) })
}

// SetT3URL saves machine's T3 URL setting ("" clears it: back to the default).
func (s *Store) SetT3URL(ctx context.Context, machine int64, u string) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Connections().SetT3URL(ctx, machine, u) })
}

// SetT3PublicURL saves machine's T3 browser URL ("" clears it: the T3 URL).
func (s *Store) SetT3PublicURL(ctx context.Context, machine int64, u string) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Connections().SetT3PublicURL(ctx, machine, u) })
}

// LoadT3AdminToken reads machine's T3 admin token ("" when none).
func (s *Store) LoadT3AdminToken(ctx context.Context, machine int64) (string, error) {
	return read(ctx, s, func(tx store.Tx) (string, error) { return tx.Connections().T3AdminToken(ctx, machine) })
}

// SaveT3AdminToken saves machine's T3 admin token ("" clears it).
func (s *Store) SaveT3AdminToken(ctx context.Context, machine int64, tok string) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Connections().SaveT3AdminToken(ctx, machine, tok) })
}

// SetLastFullSync records machine's last successful T3 full sync.
func (s *Store) SetLastFullSync(ctx context.Context, machine int64, at time.Time) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Connections().SetT3LastFullSync(ctx, machine, at) })
}
