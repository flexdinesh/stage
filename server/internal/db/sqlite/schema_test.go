package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
)

// openUnmigrated opens a fresh temp database with no migrations applied.
func openUnmigrated(t *testing.T) *DB {
	t.Helper()
	pool, err := Open(context.Background(), filepath.Join(t.TempDir(), DBFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

// migrateTo applies the embedded migrations up to and including version.
func migrateTo(t *testing.T, pool *DB, version int64) {
	t.Helper()
	if err := MigrateTo(context.Background(), pool, version); err != nil {
		t.Fatal(err)
	}
}

func mustExec(t *testing.T, pool *DB, q string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func TestMaxVersionIsLastMigration(t *testing.T) {
	provider, err := newProvider(openUnmigrated(t).DB)
	if err != nil {
		t.Fatal(err)
	}
	sources := provider.ListSources()
	if want := sources[len(sources)-1].Version; MaxVersion() != want {
		t.Fatalf("MaxVersion %d, want %d", MaxVersion(), want)
	}
}

func TestCurrentVersion(t *testing.T) {
	ctx := context.Background()
	pool := openUnmigrated(t)
	if v, err := CurrentVersion(ctx, pool); err != nil || v != 0 {
		t.Fatalf("fresh: %d %v", v, err)
	}
	migrateTo(t, pool, 2)
	if v, err := CurrentVersion(ctx, pool); err != nil || v != 2 {
		t.Fatalf("at 2: %d %v", v, err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if v, _ := db.NewStore(pool).SchemaVersion(ctx); v != MaxVersion() {
		t.Fatalf("migrated: %d, want %d", v, MaxVersion())
	}
	// A version this binary does not know (a newer Stage migrated it) counts.
	mustExec(t, pool, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`, MaxVersion()+5)
	if v, _ := CurrentVersion(ctx, pool); v != MaxVersion()+5 {
		t.Fatalf("newer: %d", v)
	}
}

func TestBackup(t *testing.T) {
	ctx := context.Background()
	pool := openUnmigrated(t)
	migrateTo(t, pool, 2)
	mustExec(t, pool, `INSERT INTO projects (id, t3_project_id, title, workspace_root, source) VALUES (1, 'p', 'P', '/w', 'app')`)
	dir := filepath.Join(t.TempDir(), BackupsDir)
	now := time.Date(2026, 11, 16, 5, 30, 45, 0, time.FixedZone("x", 3600))
	path, err := Backup(ctx, pool, dir, 2, now)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "stage-schema2-20261116T043045Z.db"); path != want {
		t.Fatalf("path %s, want %s", path, want)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode %v %v", fi, err)
	}
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v %v", fi, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("leftover files %v", entries)
	}
	copyPool, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer copyPool.Close()
	var n int
	if v, err := CurrentVersion(ctx, copyPool); err != nil || v != 2 {
		t.Fatalf("backup version %d %v", v, err)
	}
	if err := copyPool.QueryRow(`SELECT count(*) FROM projects`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("backup rows %d %v", n, err)
	}
}

func TestBackupFailureLeavesNothing(t *testing.T) {
	ctx := context.Background()
	pool := openUnmigrated(t)
	migrateTo(t, pool, 2)
	parent := t.TempDir()
	notDir := filepath.Join(parent, BackupsDir)
	if err := os.WriteFile(notDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Backup(ctx, pool, notDir, 2, time.Now()); err == nil {
		t.Fatal("backup into a file succeeded")
	}
	// VACUUM INTO itself failing (closed pool) removes its temp file.
	dir := filepath.Join(parent, "ok")
	closed := openUnmigrated(t)
	closed.Close()
	if _, err := Backup(ctx, closed, dir, 2, time.Now()); err == nil {
		t.Fatal("backup from a closed pool succeeded")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("partial backup left: %v", entries)
	}
}

func TestPruneBackupsKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	names := []string{
		"stage-schema3-20261101T000000Z.db",
		"stage-schema12-20261102T000000Z.db",
		"stage-schema4-20261103T000000Z.db",
		"stage-schema4-20261105T000000Z.db",
		"stage-schema2-20261104T000000Z.db",
		"stage-schema1-notatime.db",
		"notes.txt",
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := PruneBackups(dir, 3); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	want := []string{"notes.txt", "stage-schema1-notatime.db", "stage-schema2-20261104T000000Z.db", "stage-schema4-20261103T000000Z.db", "stage-schema4-20261105T000000Z.db"}
	if !slices.Equal(got, want) {
		t.Fatalf("kept %s", strings.Join(got, " "))
	}
}
