//go:build postgres

// Package pgtest opens Postgres engines for tests on the check:postgres
// server (STAGE_TEST_DATABASE_URL, set only by mise run check:postgres).
// Each engine is its own database, cloned from a template database
// migrated once per test process, and dropped when its test ends.
package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// URLEnv names the admin URL of the check:postgres server.
const URLEnv = "STAGE_TEST_DATABASE_URL"

// Locale is the CREATE DATABASE clause for test databases: Postgres's
// builtin C.UTF-8 provider (Unicode case mapping), so search folds
// non-ASCII case the same on every server whatever its initdb locale
// (docs/storage.md "Search"). Databases cloned from the template inherit it.
const Locale = "TEMPLATE template0 ENCODING 'UTF8' LOCALE_PROVIDER builtin BUILTIN_LOCALE 'C.UTF-8'"

// maxConns caps each test engine's pool: enough for the contract suite's
// racers, small enough for many packages on one server.
const maxConns = 12

var template struct {
	once sync.Once
	name string
	err  error
}

// Engine opens a fresh migrated Postgres engine for t, closed and dropped
// when t ends. Without STAGE_TEST_DATABASE_URL the test fails: Postgres
// tests never skip.
func Engine(t testing.TB) *postgres.DB {
	t.Helper()
	admin := adminURL(t)
	ctx := context.Background()
	template.once.Do(func() { template.name, template.err = migratedTemplate(ctx, admin) })
	if template.err != nil {
		t.Fatalf("pgtest template: %v", template.err)
	}
	name := CreateDatabase(t, "TEMPLATE "+template.name)
	d, err := postgres.Open(ctx, DatabaseURL(t, name), postgres.Options{ApplicationName: "stage test", MaxConns: maxConns})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	warm(t, d)
	return d
}

// warm opens every pool connection up front: the pool dials lazily, and a
// dial per racer would stagger the contract suite's concurrency cases
// until they no longer overlap.
func warm(t testing.TB, d *postgres.DB) {
	t.Helper()
	ctx := context.Background()
	conns := make([]*pgxpool.Conn, 0, maxConns)
	defer func() {
		for _, c := range conns {
			c.Release()
		}
	}()
	for range maxConns {
		c, err := d.Pool().Acquire(ctx)
		if err != nil {
			t.Fatalf("warm pool: %v", err)
		}
		conns = append(conns, c)
	}
}

// CreateDatabase creates a database with a random name (with clause, e.g.
// "TEMPLATE x") and drops it when t ends. It returns the name.
func CreateDatabase(t testing.TB, clause string) string {
	t.Helper()
	ctx := context.Background()
	admin := adminURL(t)
	name := "stage_test_" + suffix(t)
	if err := adminExec(ctx, admin, `CREATE DATABASE `+name+` `+clause); err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		if err := adminExec(context.Background(), admin, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`); err != nil {
			t.Errorf("drop %s: %v", name, err)
		}
	})
	return name
}

// DatabaseURL is the admin URL pointed at database name.
func DatabaseURL(t testing.TB, name string) string {
	t.Helper()
	u, err := url.Parse(adminURL(t))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}

func adminURL(t testing.TB) string {
	t.Helper()
	admin := os.Getenv(URLEnv)
	if admin == "" {
		t.Fatal(URLEnv + " is unset: run Postgres tests with mise run check:postgres")
	}
	return admin
}

// migratedTemplate creates a database migrated to the head and returns its
// name. Nothing stays connected to it, so databases can be cloned from it.
// The throwaway check:postgres server is removed with it.
func migratedTemplate(ctx context.Context, admin string) (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	name := "stage_template_" + hex.EncodeToString(b)
	if err := adminExec(ctx, admin, `CREATE DATABASE `+name+` `+Locale); err != nil {
		return "", err
	}
	u, err := url.Parse(admin)
	if err != nil {
		return "", err
	}
	u.Path = "/" + name
	d, err := postgres.Open(ctx, u.String(), postgres.Options{ApplicationName: "stage test template"})
	if err != nil {
		return "", err
	}
	defer d.Close()
	if err := d.Migrator().Up(ctx); err != nil {
		return "", fmt.Errorf("migrate template: %w", err)
	}
	return name, nil
}

func adminExec(ctx context.Context, admin, sql string) error {
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		return err
	}
	defer conn.Close(context.WithoutCancel(ctx))
	_, err = conn.Exec(ctx, sql)
	return err
}

func suffix(t testing.TB) string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}
