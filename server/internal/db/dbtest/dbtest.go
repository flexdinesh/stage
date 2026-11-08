// Package dbtest provisions throwaway Postgres databases for integration
// tests against the compose db (127.0.0.1:5439). Override the admin URL with
// TEST_DATABASE_URL.
package dbtest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/flexdinesh/t3-stage/server/internal/db"
)

const defaultAdminURL = "postgres://t3stage:t3stage@127.0.0.1:5439/t3stage?sslmode=disable"

// DB is a migrated test database (Pool nil + Skip set when unavailable).
type DB struct {
	Pool *pgxpool.Pool
	Skip string
}

// Open (re)creates database `name`, migrates it and returns a pool. Call
// from TestMain; when Postgres is unreachable Skip explains why.
func Open(name string) *DB { return OpenAt(name, 0) }

// OpenAt is Open migrated only up to version (0 = latest).
func OpenAt(name string, version int64) *DB {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminURL := os.Getenv("TEST_DATABASE_URL")
	if adminURL == "" {
		adminURL = defaultAdminURL
	}
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return &DB{Skip: fmt.Sprintf("postgres unreachable at %s (run `make db`): %v", redact(adminURL), err)}
	}
	defer admin.Close(ctx)
	ident := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(ctx, `DROP DATABASE IF EXISTS `+ident+` WITH (FORCE)`); err != nil {
		return &DB{Skip: "drop test database: " + err.Error()}
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+ident); err != nil {
		return &DB{Skip: "create test database: " + err.Error()}
	}
	u, err := url.Parse(adminURL)
	if err != nil {
		return &DB{Skip: err.Error()}
	}
	u.Path = "/" + name
	pool, err := db.Open(ctx, u.String())
	if err != nil {
		return &DB{Skip: err.Error()}
	}
	if err := db.MigrateTo(ctx, pool, version); err != nil {
		pool.Close()
		return &DB{Skip: "migrate: " + err.Error()}
	}
	return &DB{Pool: pool}
}

// Reset skips the test when no database is available, else truncates all
// tables and restores the t3_connection row.
func (d *DB) Reset(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if d.Pool == nil {
		t.Skip(d.Skip)
	}
	ctx := context.Background()
	_, err := d.Pool.Exec(ctx, `
TRUNCATE task_attachments, task_events, tasks, snippets, threads, projects, t3_connection, preferences,
  push_subscriptions, push_vapid RESTART IDENTITY CASCADE;
INSERT INTO t3_connection (id) VALUES (1);
INSERT INTO preferences (id) VALUES (1);`)
	if err != nil {
		t.Fatalf("reset db: %v", err)
	}
	return d.Pool
}

func (d *DB) Close() {
	if d.Pool != nil {
		d.Pool.Close()
	}
}

func redact(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<invalid url>"
	}
	return u.Redacted()
}
