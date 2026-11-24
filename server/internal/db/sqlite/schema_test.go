package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/backfill"
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

// Every backfill step has a SQLite runner and every runner is a step.
func TestGoMigrationsMatchBackfillSteps(t *testing.T) {
	var steps, runners []int64
	for _, s := range backfill.Steps {
		steps = append(steps, s.Version)
	}
	for v := range goMigrations {
		runners = append(runners, v)
	}
	slices.Sort(steps)
	slices.Sort(runners)
	if !slices.Equal(steps, runners) {
		t.Fatalf("backfill steps %v, SQLite runners %v", steps, runners)
	}
}

// A Go migration runs in goose's migration transaction: it is recorded when
// it succeeds and rolled back, unrecorded, when it fails.
func TestGoMigrationRunsInTheMigrationTransaction(t *testing.T) {
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	next := MaxVersion() + 1
	ctx := context.Background()
	for _, fail := range []bool{false, true} {
		pool := openUnmigrated(t)
		provider, err := providerFor(pool.DB, fsys, map[int64]goRunner{next: func(ctx context.Context, tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `INSERT INTO snippets(name, body) VALUES ('backfilled', 'x')`); err != nil {
				return err
			}
			if fail {
				return errors.New("backfill failed")
			}
			return nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		_, upErr := provider.Up(ctx)
		var n int
		if err := pool.QueryRow(`SELECT count(*) FROM snippets WHERE name = 'backfilled'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		v, err := CurrentVersion(ctx, pool)
		if err != nil {
			t.Fatal(err)
		}
		if fail && (upErr == nil || n != 0 || v != next-1) {
			t.Fatalf("failed backfill: err %v, rows %d, version %d", upErr, n, v)
		}
		if !fail && (upErr != nil || n != 1 || v != next) {
			t.Fatalf("backfill: err %v, rows %d, version %d", upErr, n, v)
		}
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
