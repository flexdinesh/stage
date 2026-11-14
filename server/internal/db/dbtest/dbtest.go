// Package dbtest gives each integration test a fresh, migrated SQLite
// database file in the test's temp dir.
package dbtest

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
)

// DB holds the current test's database (Pool, set by Reset).
type DB struct {
	Pool *sql.DB
}

// Reset opens a new migrated database for t (closed when t ends), stores it
// in Pool and returns it.
func (d *DB) Reset(t testing.TB) *sql.DB {
	t.Helper()
	ctx := context.Background()
	pool, err := db.Open(ctx, filepath.Join(t.TempDir(), db.DBFile))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	d.Pool = pool
	return pool
}
