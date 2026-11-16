package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// openUnmigrated opens a fresh temp database with no migrations applied.
func openUnmigrated(t *testing.T) *sql.DB {
	t.Helper()
	pool, err := Open(context.Background(), filepath.Join(t.TempDir(), DBFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

// migrateTo applies the embedded migrations up to and including version.
func migrateTo(t *testing.T, pool *sql.DB, version int64) {
	t.Helper()
	provider, err := newProvider(pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(context.Background(), version); err != nil {
		t.Fatalf("migrate to %d: %v", version, err)
	}
}

func tableExists(t *testing.T, pool *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := pool.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func TestMigrationDropsContinuations(t *testing.T) {
	pool := openUnmigrated(t)
	migrateTo(t, pool, 1)
	tables := []string{"continuations", "continuation_events"}
	for _, name := range tables {
		if !tableExists(t, pool, name) {
			t.Fatalf("%s missing after 0001", name)
		}
	}
	if _, err := pool.Exec(`INSERT INTO threads (t3_thread_id, t3_project_id, status, created_at, updated_at)
		VALUES ('a', 'p', 'idle', '', ''), ('b', 'p', 'idle', '', '')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(`INSERT INTO continuations (order_id, after_thread_id, target_thread_id, prompt) VALUES ('o', 'a', 'b', 'next')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(`INSERT INTO continuation_events (continuation_id, state) VALUES (1, 'waiting')`); err != nil {
		t.Fatal(err)
	}
	migrateTo(t, pool, 2)
	for _, name := range tables {
		if tableExists(t, pool, name) {
			t.Fatalf("%s still exists after 0002", name)
		}
	}
	if !tableExists(t, pool, "threads") {
		t.Fatal("threads dropped by 0002")
	}
}
