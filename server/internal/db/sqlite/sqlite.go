// Package sqlite is the SQLite storage engine: every SQLite statement, the
// modernc driver, pragmas, stage_now, VACUUM INTO, SQLite error codes and
// the SQLite migrations (docs/storage.md "Layout and ownership"). It holds
// no business rules; those stay in internal/db.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// DBFile is the database file name inside the data directory.
const DBFile = "stage.db"

// busyTimeoutMs: how long a writer waits for the write lock (BEGIN
// IMMEDIATE or an autocommit write) before SQLITE_BUSY.
const busyTimeoutMs = 10000

// DB is an open SQLite database and the SQLite store.Engine over it.
type DB struct {
	*sql.DB
	path string
}

var _ store.Engine = (*DB)(nil)

// Open opens (creating when absent) the SQLite database at path: WAL,
// busy_timeout, foreign keys on, and every transaction started as BEGIN
// IMMEDIATE so writers serialize (one writer at a time).
func Open(ctx context.Context, path string) (*DB, error) {
	q := url.Values{}
	q.Set("_txlock", "immediate")
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMs))
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	u := url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}
	pool, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := pool.PingContext(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}
	return &DB{DB: pool, path: path}, nil
}

// Read runs fn on the pool without a transaction: each statement is its own
// autocommit read, takes no write lock and never blocks a writer (WAL). A
// read transaction would start as BEGIN IMMEDIATE (_txlock) and take the
// write lock.
func (d *DB) Read(ctx context.Context, fn func(store.Tx) error) error {
	return fn(newTx(&runner{d.DB}))
}

// Write runs fn in a BEGIN IMMEDIATE transaction (store.UnitOfWork).
func (d *DB) Write(ctx context.Context, fn func(store.Tx) error) error {
	return d.inTx(ctx, func(r *runner) error { return fn(newTx(r)) })
}

// inTx runs fn in a BEGIN IMMEDIATE transaction, committing when fn returns
// nil. It rolls back on error or panic, so the write lock is never left held.
// BEGIN IMMEDIATE holds the database write lock from the first statement, so
// transactions run one at a time; this replaces Postgres row locks and
// advisory locks.
func (d *DB) inTx(ctx context.Context, fn func(r *runner) error) (err error) {
	sqlTx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = sqlTx.Rollback()
		}
	}()
	if err := fn(&runner{sqlTx}); err != nil {
		return err
	}
	committed = true
	return sqlTx.Commit()
}

func (d *DB) Errors() store.ErrorClassifier { return classifier{} }

// Ownership is the data-dir stage.lock the runtime takes before opening the
// database (docs/runtime.md "Ownership and locks"): nothing more to take.
func (d *DB) Ownership() store.Ownership { return ownership{} }

type ownership struct{}

func (ownership) Acquire(context.Context) (func() error, error) {
	return func() error { return nil }, nil
}

type classifier struct{}

func (classifier) IsUniqueViolation(err error) bool     { return isUniqueViolation(err) }
func (classifier) IsForeignKeyViolation(err error) bool { return isForeignKeyViolation(err) }

func sqliteCode(err error) int {
	var e *sqlite.Error
	if errors.As(err, &e) {
		return e.Code()
	}
	return 0
}

func isUniqueViolation(err error) bool {
	c := sqliteCode(err)
	return c == sqlite3.SQLITE_CONSTRAINT_UNIQUE || c == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
}

func isForeignKeyViolation(err error) bool {
	return sqliteCode(err) == sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY
}

// notFound maps a missing row to store.ErrNotFound.
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	return err
}

// where builds a dynamic WHERE clause with numbered placeholders.
type where struct {
	conds []string
	args  []any
}

func (w *where) arg(v any) string {
	w.args = append(w.args, v)
	return "$" + strconv.Itoa(len(w.args))
}

func (w *where) add(cond string) { w.conds = append(w.conds, cond) }

func (w *where) sql() string {
	if len(w.conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(w.conds, " AND ")
}

// likePattern escapes LIKE metacharacters (escape character \) for a
// substring match; use the placeholder with likeSQL.
func likePattern(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}

// likeSQL matches col against a likePattern placeholder, case-insensitively
// (SQLite lower() folds ASCII only).
func likeSQL(col, pattern string) string {
	return "lower(" + col + ") LIKE lower(" + pattern + `) ESCAPE '\'`
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
