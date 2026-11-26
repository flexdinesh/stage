//go:build postgres

package dbtest

import (
	"os"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db/postgres/pgtest"
	"github.com/flexdinesh/stage/server/internal/db/store"
)

// postgresEngine: under check:postgres (STAGE_TEST_DATABASE_URL set) Engine
// opens a fresh database cloned from a migrated template on that server
// (docs/storage.md "Contract suite"), with T3Connection seeded; otherwise
// it stays on SQLite. seed adds T3Connection.
func postgresEngine(t testing.TB, seed bool) (store.Engine, bool) {
	t.Helper()
	if os.Getenv(pgtest.URLEnv) == "" {
		return nil, false
	}
	d := pgtest.Engine(t)
	if seed {
		pgtest.SeedT3Connection(t, d)
	}
	return d, true
}
