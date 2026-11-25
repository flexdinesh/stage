package db

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db/store"
)

// CodexSettings is machine's Codex setup (migration 0030: one row per
// machine; none reads the zero value, never connected).
func (s *Store) CodexSettings(ctx context.Context, machine int64) (config.CodexSettings, error) {
	return read(ctx, s, func(tx store.Tx) (config.CodexSettings, error) { return tx.Connections().CodexSettings(ctx, machine) })
}

// SaveCodexSettings saves machine's Codex setup.
func (s *Store) SaveCodexSettings(ctx context.Context, machine int64, c config.CodexSettings) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Connections().SaveCodexSettings(ctx, machine, c) })
}
