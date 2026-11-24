package sqlite

import (
	"database/sql"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

// tx is the store.Tx over a runner: the pool (Read) or a BEGIN IMMEDIATE
// transaction (Write). Each repository embeds the runner.
type tx struct{ r *runner }

func newTx(r *runner) *tx { return &tx{r} }

var _ store.Tx = (*tx)(nil)

// RawTx is the write transaction (nil in Read), for db code not yet behind
// repositories (transitional; removed once every aggregate is).
func (t *tx) RawTx() *sql.Tx {
	sqlTx, _ := t.r.r.(*sql.Tx)
	return sqlTx
}
