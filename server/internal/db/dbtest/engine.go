package dbtest

import (
	"testing"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

// Engine opens a fresh migrated engine for t (closed when t ends), with
// T3Connection seeded: the
// engine the storage contract suite (storetest) and other engine-agnostic
// tests run on. A temp SQLite file; under -tags postgres with
// STAGE_TEST_DATABASE_URL set (check:postgres), a fresh Postgres database
// (engine_postgres.go).
func Engine(t testing.TB) store.Engine {
	t.Helper()
	if e, ok := postgresEngine(t, true); ok {
		return e
	}
	var d DB
	return d.Reset(t)
}

// FreshEngine is Engine as a new install has it: no connection.
func FreshEngine(t testing.TB) store.Engine {
	t.Helper()
	if e, ok := postgresEngine(t, false); ok {
		return e
	}
	var d DB
	return d.Fresh(t)
}
