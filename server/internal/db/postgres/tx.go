//go:build postgres

package postgres

import "github.com/flexdinesh/stage/server/internal/db/store"

// tx is the store.Tx over a runner: a read-only READ COMMITTED transaction
// (Read) or a READ COMMITTED transaction (Write). Each repository embeds the
// runner.
type tx struct{ r *runner }

func newTx(r *runner) *tx { return &tx{r} }

var _ store.Tx = (*tx)(nil)
