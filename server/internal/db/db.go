package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// DBFile is the database file name inside the data directory.
const DBFile = "stage.db"

// busyTimeoutMs: how long a writer waits for the write lock (BEGIN
// IMMEDIATE or an autocommit write) before SQLITE_BUSY.
const busyTimeoutMs = 10000

// Open opens (creating when absent) the SQLite database at path: WAL,
// busy_timeout, foreign keys on, and every transaction started as BEGIN
// IMMEDIATE so writers serialize (one writer at a time).
func Open(ctx context.Context, path string) (*sql.DB, error) {
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
	return pool, nil
}

// Store is hand-written data access over a SQLite pool.
type Store struct {
	pool conn
}

func NewStore(pool *sql.DB) *Store { return &Store{pool: conn{pool}} }

// App errors shared by db, sync, links and api (api maps them to statuses).
var ErrNotFound = errors.New("not found")

// ConflictError: action not allowed in current state / duplicate (409).
// Code, when set, is a machine-readable reason (error body "code").
type ConflictError struct {
	Msg  string
	Code string
}

func (e *ConflictError) Error() string { return e.Msg }

func Conflict(format string, args ...any) error {
	return &ConflictError{Msg: fmt.Sprintf(format, args...)}
}

// InvalidError: invalid input (400).
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

func Invalid(format string, args ...any) error {
	return &InvalidError{Msg: fmt.Sprintf(format, args...)}
}

// T3Error wraps a failed T3 call (502, or 503 when it wraps t3.ErrNotConnected).
type T3Error struct{ Err error }

func (e *T3Error) Error() string { return e.Err.Error() }
func (e *T3Error) Unwrap() error { return e.Err }

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

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
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

// Optional is a PATCH field: Set when present in JSON, Null when JSON null.
type Optional[T any] struct {
	Set  bool
	Null bool
	V    T
}

func (o *Optional[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Null = true
		var zero T
		o.V = zero
		return nil
	}
	return json.Unmarshal(b, &o.V)
}

// Ptr returns a pointer to V, or nil when unset or null.
func (o Optional[T]) Ptr() *T {
	if !o.Set || o.Null {
		return nil
	}
	v := o.V
	return &v
}
