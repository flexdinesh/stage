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
// (docs/storage.md "Contract suite"); otherwise it stays on SQLite.
func postgresEngine(t testing.TB) (store.Engine, bool) {
	t.Helper()
	if os.Getenv(pgtest.URLEnv) == "" {
		return nil, false
	}
	return pgtest.Engine(t), true
}
