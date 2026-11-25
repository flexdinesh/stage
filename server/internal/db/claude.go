package db

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db/store"
)

// ClaudeSettings is machine's Claude Code harness setup (migration 0030:
// one row per machine; none reads the zero value).
func (s *Store) ClaudeSettings(ctx context.Context, machine int64) (config.ClaudeSettings, error) {
	var c config.ClaudeSettings
	err := s.e.Read(ctx, func(tx store.Tx) error {
		stored, found, err := tx.Connections().ClaudeSettings(ctx, machine)
		if found {
			c = stored
		}
		return err
	})
	return c, err
}

// SaveClaudeSettings saves machine's Claude Code harness setup.
func (s *Store) SaveClaudeSettings(ctx context.Context, machine int64, c config.ClaudeSettings) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Connections().SaveClaudeSettings(ctx, machine, c) })
}
