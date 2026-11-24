//go:build postgres

package serve_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/postgres"
	"github.com/flexdinesh/stage/server/internal/db/postgres/pgtest"
	"github.com/flexdinesh/stage/server/internal/db/sqlite"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/instance"
	"github.com/flexdinesh/stage/server/internal/serve"
)

// pgConfig is cfg on the Postgres database rawURL names.
func pgConfig(t *testing.T, dataDir, rawURL string, listens ...string) config.Config {
	t.Helper()
	c := cfg(t, dataDir, listens...)
	d, err := config.ParseDatabase(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	c.Database = d
	return c
}

// noSecret fails when out holds rawURL or its password.
func noSecret(t *testing.T, what, out, rawURL string) {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	pw, _ := u.User.Password()
	if strings.Contains(out, rawURL) || strings.Contains(out, "postgres://") || (pw != "" && strings.Contains(out, ":"+pw+"@")) {
		t.Fatalf("%s prints the database URL or password:\n%s", what, out)
	}
}

// TestPostgresEngine: STAGE_DATABASE_URL selects Postgres. The server
// migrates a fresh database, reports the engine (never the URL), keeps only
// its lock and sockets in the data dir, and is the database's single owner
// across data dirs.
func TestPostgresEngine(t *testing.T) {
	short := isolate(t)
	rawURL := pgtest.DatabaseURL(t, pgtest.CreateDatabase(t, ""))
	data := filepath.Join(short, "a")
	logger, logs := bufLogger()
	i := startLogged(t, pgConfig(t, data, rawURL, "unix:"+filepath.Join(short, "a.sock")), newT3(), logger)
	if h := health(t, i.ready.Listeners[0]); h.Engine != "postgres" || h.Schema != postgres.MaxVersion() || h.DB != "ok" {
		t.Fatalf("health engine %q schema %d db %q", h.Engine, h.Schema, h.DB)
	}
	if _, err := os.Stat(filepath.Join(i.ready.DataDir, instance.LockFile)); err != nil {
		t.Fatalf("data dir lock: %v", err)
	}
	gone(t, filepath.Join(i.ready.DataDir, sqlite.DBFile))
	if !strings.Contains(logs.String(), "msg=database engine=postgres\n") {
		t.Fatalf("startup log lacks the engine:\n%s", logs)
	}

	// A second server on another data dir, same database: 75.
	other := filepath.Join(short, "b")
	err := runErr(t, pgConfig(t, other, rawURL, "unix:"+filepath.Join(short, "b.sock")))
	var oe *store.OwnedError
	if !errors.As(err, &oe) || !strings.HasPrefix(oe.Holder, "stage "+i.ready.Meta.InstanceID) {
		t.Fatalf("second owner: %v", err)
	}
	gone(t, filepath.Join(other, sqlite.DBFile))
	noSecret(t, "error", err.Error(), rawURL)
	noSecret(t, "logs", logs.String(), rawURL)
}

// TestPostgresNewerSchemaRefused: a database migrated by a newer Stage
// exits 78, untouched.
func TestPostgresNewerSchemaRefused(t *testing.T) {
	short := isolate(t)
	rawURL := pgtest.DatabaseURL(t, pgtest.CreateDatabase(t, ""))
	ctx := context.Background()
	d, err := postgres.Open(ctx, rawURL, postgres.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Migrator().Up(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Pool().Exec(ctx, `INSERT INTO goose_db_version (version_id, is_applied) VALUES ($1, true)`, postgres.MaxVersion()+1); err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
	err = runErr(t, pgConfig(t, filepath.Join(short, "d"), rawURL, "unix:"+filepath.Join(short, "s.sock")))
	var ie *config.InvalidError
	var tooNew *db.SchemaTooNewError
	if !errors.As(err, &ie) || !errors.As(err, &tooNew) || tooNew.Current != postgres.MaxVersion()+1 {
		t.Fatalf("newer schema: %v", err)
	}
}

// TestPostgresUnreachable: a database that cannot be reached fails startup
// (exit 1) within the bound, without printing the password.
func TestPostgresUnreachable(t *testing.T) {
	short := isolate(t)
	rawURL := "postgres://stage:s3cret-pw@" + freePort(t) + "/stage?sslmode=disable"
	logger, logs := bufLogger()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	err := serve.Run(ctx, serve.Options{Config: pgConfig(t, filepath.Join(short, "d"), rawURL, "unix:"+filepath.Join(short, "s.sock")), T3: newT3(), Logger: logger})
	var ie *config.InvalidError
	var oe *store.OwnedError
	if err == nil || ctx.Err() != nil || errors.As(err, &ie) || errors.As(err, &oe) {
		t.Fatalf("unreachable: %v (ctx %v)", err, ctx.Err())
	}
	noSecret(t, "error", err.Error(), rawURL)
	noSecret(t, "logs", logs.String(), rawURL)
}
