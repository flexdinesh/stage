package db

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

type Machine = store.Machine

// GetMachine reads a machine.
func (s *Store) GetMachine(ctx context.Context, id int64) (Machine, error) {
	return read(ctx, s, func(tx store.Tx) (Machine, error) { return tx.Machines().Get(ctx, id) })
}
