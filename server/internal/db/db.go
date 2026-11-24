package db

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/flexdinesh/stage/server/internal/db/store"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Store is the data access facade: business rules over the storage ports
// (internal/db/store) of one engine.
type Store struct {
	e    store.Engine
	pool conn
	// conns caches harness_connections (a handful of rows). Every Store
	// write to that table drops it; nil reloads on the next read.
	conns atomic.Pointer[connectionSnapshot]
}

// NewStore is the Store over engine e.
func NewStore(e store.Engine) *Store {
	s := &Store{e: e}
	if r, ok := e.(interface{ RawSQL() *sql.DB }); ok {
		s.pool = conn{r.RawSQL()} // transitional: SQL not yet behind repositories
	}
	return s
}

// App errors shared by db, sync, links and api (api maps them to statuses).
var ErrNotFound = store.ErrNotFound

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

// HarnessError wraps a failed adapter operation.
type HarnessError struct{ Err error }

// T3Error retains source compatibility for existing callers.
type T3Error = HarnessError

func (e *HarnessError) Error() string { return e.Err.Error() }
func (e *T3Error) Unwrap() error      { return e.Err }

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

// DecodeStrict decodes one JSON object (empty: {}) into v, rejecting unknown
// fields; errors are Invalid.
func DecodeStrict(raw []byte, v any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return Invalid("invalid JSON body: %v", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return Invalid("body must contain one JSON object")
	}
	return nil
}
