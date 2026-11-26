// Package dbtest gives each integration test a fresh, migrated database
// through the SQLite engine, in a file in the test's temp dir.
package dbtest

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db/sqlite"
	"github.com/flexdinesh/stage/server/internal/db/sqlite/sqlitetest"
)

// T3Connection is the T3 connection Reset and Engine seed on the local
// machine, as every install before migration 0032 had. A fresh install has
// no connection (no default harness): Fresh opens one without it.
const T3Connection int64 = 1

// DB holds the current test's database (Pool, set by Reset).
type DB struct {
	Pool *sqlite.DB
}

// Reset opens a new migrated database for t (closed when t ends) with
// T3Connection seeded, stores it in Pool and returns it.
func (d *DB) Reset(t testing.TB) *sqlite.DB {
	t.Helper()
	pool := d.Fresh(t)
	sqlitetest.SeedT3Connection(t, pool)
	return pool
}

// Fresh opens a new migrated database for t (closed when t ends) as a new
// install has it, stores it in Pool and returns it.
func (d *DB) Fresh(t testing.TB) *sqlite.DB {
	t.Helper()
	ctx := context.Background()
	pool, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), sqlite.DBFile))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	if err := sqlite.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	d.Pool = pool
	return pool
}
