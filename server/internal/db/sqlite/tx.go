package sqlite

import "github.com/flexdinesh/stage/server/internal/db/store"

// tx is the store.Tx over a runner: the pool (Read) or a BEGIN IMMEDIATE
// transaction (Write). Each repository embeds the runner.
type tx struct{ r *runner }

func newTx(r *runner) *tx { return &tx{r} }

var _ store.Tx = (*tx)(nil)
