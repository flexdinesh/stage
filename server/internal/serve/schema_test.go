package serve_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/pressly/goose/v3"
)

// seedDB creates data/stage.db migrated up to version (from the source
// migrations) and returns its pool.
func seedDB(t *testing.T, data string, version int64) *sql.DB {
	t.Helper()
	pool, err := db.Open(context.Background(), filepath.Join(data, db.DBFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	p, err := goose.NewProvider(goose.DialectSQLite3, pool, os.DirFS(filepath.Join("..", "db", "migrations")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(context.Background(), version); err != nil {
		t.Fatal(err)
	}
	return pool
}

func version(t *testing.T, pool *sql.DB) int64 {
	t.Helper()
	v, err := db.CurrentVersion(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func backups(t *testing.T, data string) []string {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(data, db.BackupsDir, "stage-schema*.db"))
	if err != nil {
		t.Fatal(err)
	}
	return names
}

// A database migrated by a newer Stage is refused as invalid configuration
// (exit 78) and left untouched.
func TestNewerSchemaRefused(t *testing.T) {
	short := isolate(t)
	data := t.TempDir()
	pool := seedDB(t, data, db.MaxVersion())
	newer := db.MaxVersion() + 1
	if _, err := pool.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`, newer); err != nil {
		t.Fatal(err)
	}
	err := runErr(t, cfg(t, data, "unix:"+filepath.Join(short, "n.sock")))
	var ie *config.InvalidError
	if !errors.As(err, &ie) || !errors.Is(err, db.ErrSchemaTooNew) {
		t.Fatalf("err %v, want invalid configuration (schema too new)", err)
	}
	if !strings.Contains(err.Error(), "is newer than this Stage") || !strings.Contains(err.Error(), "docs/install.md#recovery") {
		t.Fatalf("message %q", err.Error())
	}
	if v := version(t, pool); v != newer {
		t.Fatalf("version %d, want %d", v, newer)
	}
	if len(backups(t, data)) != 0 {
		t.Fatal("backup taken for a newer schema")
	}
}

// Pending migrations on an existing database: a 0600 backup at the old
// schema first, then the migration; health reports the new schema.
func TestOlderSchemaBackedUpBeforeMigrate(t *testing.T) {
	short := isolate(t)
	data := t.TempDir()
	pool := seedDB(t, data, 2)
	if _, err := pool.Exec(`INSERT INTO projects (id, t3_project_id, title, workspace_root, source) VALUES (1, 'p', 'P', '/w', 'app')`); err != nil {
		t.Fatal(err)
	}
	i := start(t, cfg(t, data, "unix:"+filepath.Join(short, "o.sock")), newT3())
	if h := health(t, i.ready.Listeners[0]); h.Schema != db.MaxVersion() {
		t.Fatalf("health schema %d, want %d", h.Schema, db.MaxVersion())
	}
	if v := version(t, pool); v != db.MaxVersion() {
		t.Fatalf("version %d", v)
	}
	got := backups(t, data)
	if len(got) != 1 || !strings.HasPrefix(filepath.Base(got[0]), "stage-schema2-") {
		t.Fatalf("backups %v", got)
	}
	if fi, err := os.Stat(got[0]); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode %v %v", fi, err)
	}
	b, err := db.Open(context.Background(), got[0])
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var n int
	if v := version(t, b); v != 2 {
		t.Fatalf("backup version %d", v)
	}
	if err := b.QueryRow(`SELECT count(*) FROM projects`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("backup rows %d %v", n, err)
	}
	// Up to date now: a restart takes no backup.
	if err := i.stop(t); err != nil {
		t.Fatal(err)
	}
	start(t, cfg(t, data, "unix:"+filepath.Join(short, "o.sock")), newT3())
	if got := backups(t, data); len(got) != 1 {
		t.Fatalf("backups after restart %v", got)
	}
}

// Startup prunes to the newest three backups.
func TestBackupsPrunedToThree(t *testing.T) {
	short := isolate(t)
	data := t.TempDir()
	seedDB(t, data, 2)
	dir := filepath.Join(data, db.BackupsDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := []string{"stage-schema1-20260101T000000Z.db", "stage-schema1-20260102T000000Z.db", "stage-schema2-20260103T000000Z.db"}
	for _, n := range old {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	start(t, cfg(t, data, "unix:"+filepath.Join(short, "p.sock")), newT3())
	got := backups(t, data)
	if len(got) != 3 {
		t.Fatalf("backups %v", got)
	}
	for _, p := range got {
		if filepath.Base(p) == old[0] {
			t.Fatalf("oldest backup kept: %v", got)
		}
	}
}

// A backup that cannot be written refuses the migration (exit 1, not 78)
// and leaves the database at its old schema.
func TestBackupFailureRefusesMigrate(t *testing.T) {
	short := isolate(t)
	data := t.TempDir()
	pool := seedDB(t, data, 2)
	if err := os.WriteFile(filepath.Join(data, db.BackupsDir), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err := runErr(t, cfg(t, data, "unix:"+filepath.Join(short, "f.sock")))
	var ie *config.InvalidError
	if errors.As(err, &ie) || !strings.Contains(err.Error(), "refusing to migrate") {
		t.Fatalf("err %v", err)
	}
	if v := version(t, pool); v != 2 {
		t.Fatalf("version %d, want 2", v)
	}
}

// A fresh database is migrated without a backup.
func TestFreshDBNoBackup(t *testing.T) {
	short := isolate(t)
	data := t.TempDir()
	i := start(t, cfg(t, data, "unix:"+filepath.Join(short, "e.sock")), newT3())
	if h := health(t, i.ready.Listeners[0]); h.Schema != db.MaxVersion() {
		t.Fatalf("health schema %d", h.Schema)
	}
	if _, err := os.Stat(filepath.Join(data, db.BackupsDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backups dir for a fresh database: %v", err)
	}
}
