package dbtest

import (
	"testing"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

// Engine opens a fresh migrated engine for t (closed when t ends): the
// engine the storage contract suite (storetest) and other engine-agnostic
// tests run on. Today a temp SQLite file; see engine_postgres.go.
func Engine(t testing.TB) store.Engine {
	t.Helper()
	notePostgres(t)
	var d DB
	return d.Reset(t)
}
