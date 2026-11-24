//go:build postgres

package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// openEngine opens an engine on a fresh empty database.
func openEngine(t *testing.T, url string, opts Options) *DB {
	t.Helper()
	d, err := Open(context.Background(), url, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// rawExec runs SQL on a unit of work's transaction.
func rawExec(ctx context.Context, x store.Tx, q string, args ...any) error {
	_, err := x.(*tx).r.Exec(ctx, q, args...)
	return err
}

// A second owner of one database gets OwnedError naming the holder's
// application_name; after Release it can take ownership.
func TestOwnershipSingleOwner(t *testing.T) {
	ctx := context.Background()
	url := freshURL(t)
	a := openEngine(t, url, Options{ApplicationName: "stage owner-a"})
	b := openEngine(t, url, Options{ApplicationName: "stage owner-b"})
	held, err := a.Ownership().Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.Ownership().Acquire(ctx)
	var owned *store.OwnedError
	if !errors.As(err, &owned) || owned.Holder != "stage owner-a" {
		t.Fatalf("second owner: %v, want OwnedError held by stage owner-a", err)
	}
	if err := held.Release(); err != nil {
		t.Fatal(err)
	}
	if err := held.Release(); err != nil {
		t.Fatalf("second release: %v", err)
	}
	next, err := b.Ownership().Acquire(ctx)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	if err := next.Release(); err != nil {
		t.Fatal(err)
	}
}

// Losing the lock's connection closes Lost, and another owner can then take
// the database.
func TestOwnershipLost(t *testing.T) {
	ctx := context.Background()
	url := freshURL(t)
	a := openEngine(t, url, Options{ApplicationName: "stage lost-a", OwnershipPing: 20 * time.Millisecond})
	held, err := a.Ownership().Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	select {
	case <-held.Lost():
		t.Fatal("lost before the connection failed")
	case <-time.After(100 * time.Millisecond):
	}
	admin := openEngine(t, url, Options{ApplicationName: "stage admin"})
	var killed int
	if err := admin.Pool().QueryRow(ctx, `SELECT count(pg_terminate_backend(l.pid)) FROM pg_locks l
 WHERE l.locktype = 'advisory' AND l.objid = ($1::bigint & 4294967295)::oid`, ownerLockKey).Scan(&killed); err != nil || killed != 1 {
		t.Fatalf("terminate holder: %d %v", killed, err)
	}
	select {
	case <-held.Lost():
	case <-time.After(5 * time.Second):
		t.Fatal("Lost not closed after the lock's connection was terminated")
	}
	b := openEngine(t, url, Options{ApplicationName: "stage lost-b"})
	next, err := b.Ownership().Acquire(ctx)
	if err != nil {
		t.Fatalf("after loss: %v", err)
	}
	next.Release()
}

// A deadlock between two writes is retried: both commit.
func TestWriteRetriesDeadlock(t *testing.T) {
	ctx := context.Background()
	url := freshURL(t)
	d := openEngine(t, url, Options{})
	if _, err := d.Pool().Exec(ctx, `CREATE TABLE pair (id int PRIMARY KEY, n int NOT NULL);
INSERT INTO pair VALUES (1, 0), (2, 0)`); err != nil {
		t.Fatal(err)
	}
	var runs atomic.Int32
	var both sync.WaitGroup
	both.Add(2)
	write := func(first, second int) error {
		return d.Write(ctx, func(x store.Tx) error {
			run := runs.Add(1)
			if err := rawExec(ctx, x, `SET LOCAL deadlock_timeout = '50ms'`); err != nil {
				return err
			}
			if err := rawExec(ctx, x, `UPDATE pair SET n = n + 1 WHERE id = $1`, first); err != nil {
				return err
			}
			if run <= 2 { // first runs: each waits until the other holds its row
				both.Done()
				both.Wait()
			}
			return rawExec(ctx, x, `UPDATE pair SET n = n + 1 WHERE id = $1`, second)
		})
	}
	errs := make(chan error, 2)
	go func() { errs <- write(1, 2) }()
	go func() { errs <- write(2, 1) }()
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if got := runs.Load(); got != 3 {
		t.Fatalf("%d runs, want 3 (one deadlock victim reran)", got)
	}
	var n1, n2 int
	if err := d.Pool().QueryRow(ctx, `SELECT (SELECT n FROM pair WHERE id = 1), (SELECT n FROM pair WHERE id = 2)`).Scan(&n1, &n2); err != nil || n1 != 2 || n2 != 2 {
		t.Fatalf("rows %d %d %v, want 2 2", n1, n2, err)
	}
}

// A write that keeps failing with a serialization failure runs
// writeAttempts times and returns the error; other errors never rerun.
func TestWriteRetryLimit(t *testing.T) {
	d := openEngine(t, freshURL(t), Options{})
	for _, tc := range []struct {
		code string
		runs int
	}{{"40001", writeAttempts}, {"40P01", writeAttempts}, {"23505", 1}} {
		runs := 0
		err := d.Write(context.Background(), func(store.Tx) error {
			runs++
			return fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: tc.code})
		})
		if pgCode(err) != tc.code || runs != tc.runs {
			t.Fatalf("%s: %d runs, err %v; want %d runs", tc.code, runs, err, tc.runs)
		}
	}
}

// Read runs in a read-only transaction: a write inside it fails.
func TestReadIsReadOnly(t *testing.T) {
	d := openEngine(t, freshURL(t), Options{})
	err := d.Read(context.Background(), func(x store.Tx) error {
		return rawExec(context.Background(), x, `CREATE TABLE nope (id int)`)
	})
	if pgCode(err) != "25006" {
		t.Fatalf("write in Read: %v, want read_only_sql_transaction (25006)", err)
	}
}

// A statement cancelled because the context ended returns the context's
// error.
func TestContextEndedIsTheContextError(t *testing.T) {
	d := openEngine(t, freshURL(t), Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := d.Write(ctx, func(x store.Tx) error { return rawExec(ctx, x, `SELECT pg_sleep(5)`) })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled write: %v, want context.DeadlineExceeded", err)
	}
}

// SQLSTATE mapping (docs/storage.md "Errors").
func TestErrorClassifier(t *testing.T) {
	c := classifier{}
	wrap := func(code string) error { return fmt.Errorf("x: %w", &pgconn.PgError{Code: code}) }
	if !c.IsUniqueViolation(wrap("23505")) || c.IsUniqueViolation(wrap("23503")) || c.IsUniqueViolation(errors.New("23505")) {
		t.Fatal("unique violation is exactly 23505")
	}
	if !c.IsForeignKeyViolation(wrap("23503")) || c.IsForeignKeyViolation(wrap("23505")) {
		t.Fatal("foreign-key violation is exactly 23503")
	}
	if !errors.Is(notFound(fmt.Errorf("x: %w", pgx.ErrNoRows)), store.ErrNotFound) {
		t.Fatal("pgx.ErrNoRows is ErrNotFound")
	}
}

// Backup takes none and warns, naming the versions.
func TestBackupWarns(t *testing.T) {
	var buf bytes.Buffer
	d := openEngine(t, freshURL(t), Options{Logger: slog.New(slog.NewTextHandler(&buf, nil))})
	path, err := d.Backup().Backup(context.Background(), 27, time.Now())
	if err != nil || path != "" {
		t.Fatalf("backup: %q %v, want none", path, err)
	}
	out := buf.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "schema=27") || !strings.Contains(out, fmt.Sprintf("migrating_to=%d", MaxVersion())) {
		t.Fatalf("warning: %s", out)
	}
}
