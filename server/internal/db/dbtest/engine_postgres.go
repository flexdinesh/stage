//go:build postgres

package dbtest

import (
	"os"
	"testing"
)

// notePostgres: check:postgres sets STAGE_TEST_DATABASE_URL, but there is no
// Postgres adapter yet, so Engine still opens SQLite and says so. The
// Postgres adapter (#403) replaces this with an Engine that opens a fresh
// database or schema on that URL (docs/storage.md "Contract suite").
func notePostgres(t testing.TB) {
	if os.Getenv("STAGE_TEST_DATABASE_URL") != "" {
		t.Log("postgres adapter not present yet (#403): running on SQLite")
	}
}
