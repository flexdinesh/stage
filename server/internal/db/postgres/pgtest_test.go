//go:build postgres

package postgres

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// freshDB returns a pool on a new, empty database created for this test on
// the check:postgres server (STAGE_TEST_DATABASE_URL), dropped afterwards.
// Packages share that server, so every test works in its own database.
// Without the variable the test fails: Postgres tests never skip.
func freshDB(t *testing.T) *sql.DB {
	t.Helper()
	admin := os.Getenv("STAGE_TEST_DATABASE_URL")
	if admin == "" {
		t.Fatal("STAGE_TEST_DATABASE_URL is unset: run Postgres tests with mise run check:postgres")
	}
	adminPool, err := sql.Open("pgx", admin)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { adminPool.Close() })
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := "stage_test_" + hex.EncodeToString(suffix)
	ctx := context.Background()
	if _, err := adminPool.ExecContext(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(admin)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pool, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		if _, err := adminPool.ExecContext(ctx, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`); err != nil {
			t.Errorf("drop %s: %v", name, err)
		}
	})
	return pool
}
