// Package sqlitetest holds SQLite test fixtures that need raw SQL (the
// storage boundary keeps SQL in the adapters).
package sqlitetest

import (
	"context"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db/sqlite"
)

// SeedT3Connection inserts T3 connection 1 on the local machine (unpaired,
// identity unstamped), as every install before migration 0032 had.
func SeedT3Connection(t testing.TB, d *sqlite.DB) {
	t.Helper()
	if _, err := d.ExecContext(context.Background(), `INSERT INTO harness_connections (id, machine_id, harness, stable_identity) VALUES (1, 1, 't3', true)`); err != nil {
		t.Fatalf("seed t3 connection: %v", err)
	}
}
