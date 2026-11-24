package db

import (
	"context"
	"github.com/flexdinesh/stage/server/internal/db/store"
)

// Launch base sources: predecessor or fallback.
const (
	LaunchBasePredecessor = store.LaunchBasePredecessor
	LaunchBaseFallback    = store.LaunchBaseFallback
)

// LaunchBase is the worktree base one launch attempt used.
type LaunchBase = store.LaunchBase

// RecordLaunchBase stores attempt's launch base (replacing an earlier record
// of the same attempt) and a launching task event describing it, in one
// transaction.
func (s *Store) RecordLaunchBase(ctx context.Context, taskID int64, b LaunchBase) error {
	return s.e.Write(ctx, func(tx store.Tx) error {
		var sourceID *int64
		if b.Session != nil {
			id, err := ensureSession(ctx, tx, *b.Session)
			if err != nil {
				return err
			}
			sourceID = &id
		}
		if err := tx.Attempts().RecordBase(ctx, taskID, b, sourceID); err != nil {
			return err
		}
		from := StateLaunching
		return tx.Tasks().AddEvent(ctx, taskID, &from, StateLaunching, b.Detail())
	})
}

// GetLaunchBase is attempt's launch base record; nil when none was recorded.
func (s *Store) GetLaunchBase(ctx context.Context, taskID int64, attempt int) (*LaunchBase, error) {
	return read(ctx, s, func(tx store.Tx) (*LaunchBase, error) { return tx.Attempts().Base(ctx, taskID, attempt) })
}
