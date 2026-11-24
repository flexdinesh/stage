//go:build postgres

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/flexdinesh/stage/server/internal/db/backfill"
	"github.com/pressly/goose/v3"
)

// testMigrations stand in for the embedded dir until the baseline (#403).
// 00002 is slow and not idempotent: two unserialized runs collide.
var testMigrations = fstest.MapFS{
	"00001_one.sql": {Data: []byte("-- +goose Up\nCREATE TABLE one (id bigint PRIMARY KEY);\n")},
	"00002_two.sql": {Data: []byte("-- +goose Up\nCREATE TABLE two (id bigint PRIMARY KEY);\nSELECT pg_sleep(0.5);\nINSERT INTO one (id) VALUES (1);\n")},
}

// Every backfill step has a Postgres runner and every runner is a step.
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
		t.Fatalf("backfill steps %v, Postgres runners %v", steps, runners)
	}
}

func TestCurrentVersion(t *testing.T) {
	ctx := context.Background()
	pool := freshDB(t)
	if v, err := CurrentVersion(ctx, pool); err != nil || v != 0 {
		t.Fatalf("fresh: %d %v", v, err)
	}
	provider, err := providerFor(pool, testMigrations, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if v, err := CurrentVersion(ctx, pool); err != nil || v != 1 {
		t.Fatalf("at 1: %d %v", v, err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	// A version this binary does not know (a newer Stage migrated it) counts,
	// so the schema-too-new guard refuses it.
	if _, err := pool.ExecContext(ctx, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (7, true)`); err != nil {
		t.Fatal(err)
	}
	if v, err := CurrentVersion(ctx, pool); err != nil || v != 7 {
		t.Fatalf("newer: %d %v", v, err)
	}
}

// Two processes migrating one database at once: goose's session locker runs
// one after the other, so each migration applies exactly once. Without it
// both would run 00002 and the second would fail on CREATE TABLE two.
func TestSessionLockerSerializesMigrations(t *testing.T) {
	ctx := context.Background()
	pool := freshDB(t)
	// The version table exists first: on a fresh database goose's retry of
	// its own table creation would hide the race.
	first, err := providerFor(pool, testMigrations, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.UpTo(ctx, 1); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			provider, err := providerFor(pool, testMigrations, nil)
			if err != nil {
				errs[i] = err
				return
			}
			_, errs[i] = provider.Up(ctx)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("migration %d: %v", i, err)
		}
	}
	var applied, rows int
	if err := pool.QueryRowContext(ctx, `SELECT count(*) FROM goose_db_version WHERE version_id > 0`).Scan(&applied); err != nil || applied != 2 {
		t.Fatalf("applied %d %v, want 2", applied, err)
	}
	if err := pool.QueryRowContext(ctx, `SELECT count(*) FROM one`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("rows %d %v, want 1", rows, err)
	}
}

// A Go migration runs in goose's migration transaction: recorded when it
// succeeds, rolled back and unrecorded when it fails.
func TestGoMigrationRunsInTheMigrationTransaction(t *testing.T) {
	ctx := context.Background()
	for _, fail := range []bool{false, true} {
		pool := freshDB(t)
		provider, err := providerFor(pool, testMigrations, map[int64]goRunner{3: func(ctx context.Context, tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `INSERT INTO one (id) VALUES (3)`); err != nil {
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
		if err := pool.QueryRowContext(ctx, `SELECT count(*) FROM one WHERE id = 3`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		v, err := CurrentVersion(ctx, pool)
		if err != nil {
			t.Fatal(err)
		}
		if fail && (upErr == nil || n != 0 || v != 2) {
			t.Fatalf("failed backfill: err %v, rows %d, version %d", upErr, n, v)
		}
		if !fail && (upErr != nil || n != 1 || v != 3) {
			t.Fatalf("backfill: err %v, rows %d, version %d", upErr, n, v)
		}
	}
}

// The embedded migrations: before the baseline there are none and Migrate
// says so; after it, Migrate reaches MaxVersion.
func TestMigrateEmbedded(t *testing.T) {
	ctx := context.Background()
	pool := freshDB(t)
	err := NewMigrator(pool).Up(ctx)
	if MaxVersion() == 0 {
		if !errors.Is(err, goose.ErrNoMigrations) {
			t.Fatalf("no baseline: %v, want goose.ErrNoMigrations", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if v, err := NewMigrator(pool).Current(ctx); err != nil || v != MaxVersion() {
		t.Fatalf("migrated: %d %v, want %d", v, err, MaxVersion())
	}
}
