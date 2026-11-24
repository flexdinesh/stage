//go:build postgres

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db/postgres"
	"github.com/flexdinesh/stage/server/internal/db/postgres/pgtest"
	"github.com/flexdinesh/stage/server/internal/db/sqlite"
	"github.com/flexdinesh/stage/server/internal/instance"
)

// copySource writes a migrated SQLite database (at version) into its own
// data dir and returns its path.
func copySource(t *testing.T, version int64) string {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), sqlite.DBFile)
	d, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlite.MigrateTo(ctx, d, version); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, `INSERT INTO snippets (name, body) VALUES ('s', 'b')`); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func emptyTargetURL(t *testing.T) string {
	t.Helper()
	return pgtest.DatabaseURL(t, pgtest.CreateDatabase(t, pgtest.Locale))
}

// The command end to end: copied and verified (exit 0, one line per table,
// never the URL); a second run is refused (exit 1).
func TestDBCopy(t *testing.T) {
	src := copySource(t, sqlite.MaxVersion())
	url := emptyTargetURL(t)
	code, out, stderr := runDBArgs("copy", "--from", src, "--to", url)
	if code != 0 {
		t.Fatalf("copy: %d %s", code, stderr)
	}
	if !strings.Contains(out, "copied "+src+" to Postgres: schema") || !strings.Contains(out, "target migrated") ||
		!strings.Contains(out, "  snippets ") || !strings.Contains(out, "verified") {
		t.Fatalf("output:\n%s", out)
	}
	if strings.Contains(out+stderr, url) {
		t.Fatal("output names the URL")
	}
	code, _, stderr = runDBArgs("copy", "--from", src, "--to", url)
	if code != 1 || !strings.Contains(stderr, "holds data") {
		t.Fatalf("second copy: %d %s", code, stderr)
	}
}

// Offline: a held source data dir exits 75; so does a target a server owns.
// Neither writes the target.
func TestDBCopyHeld(t *testing.T) {
	ctx := context.Background()
	_, environ := env(t)
	src := copySource(t, sqlite.MaxVersion())
	url := emptyTargetURL(t)

	o, err := instance.Lock(filepath.Dir(src))
	if err != nil {
		t.Fatal(err)
	}
	code, stderr := runMain(t, environ, "db", "copy", "--from", src, "--to", url)
	o.Release()
	if code != 75 || !strings.Contains(stderr, "in use by another stage server") {
		t.Fatalf("held data dir: %d %s", code, stderr)
	}

	owner, err := postgres.Open(ctx, url, postgres.Options{ApplicationName: "stage owner"})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	lease, err := owner.Ownership().Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	code, _, stderr = runDBArgs("copy", "--from", src, "--to", url)
	if code != 75 || !strings.Contains(stderr, "owned by another stage server (stage owner)") {
		t.Fatalf("owned target: %d %s", code, stderr)
	}
	st, err := owner.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != 0 || st.Tables != 0 {
		t.Fatalf("target written: %+v", st)
	}
}

// A source below this binary's head: exit 1 with the recovery step.
func TestDBCopyOldSource(t *testing.T) {
	src := copySource(t, sqlite.MaxVersion()-1)
	code, _, stderr := runDBArgs("copy", "--from", src, "--to", emptyTargetURL(t))
	if code != 1 || !strings.Contains(stderr, "start this stage version once on the SQLite database") {
		t.Fatalf("%d %s", code, stderr)
	}
	if _, err := os.Stat(src + "-wal"); !os.IsNotExist(err) {
		t.Fatalf("source WAL created: %v", err)
	}
}
