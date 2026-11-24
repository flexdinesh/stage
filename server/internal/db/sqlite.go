package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"
)

// timeLayout is the stored timestamp format: fixed-width UTC with
// microseconds, so lexical order is time order.
const timeLayout = "2006-01-02T15:04:05.000000Z"

// nowSQL is the current time in timeLayout from the Go clock (microsecond
// resolution; SQLite's own clock has milliseconds, so
// writes in quick succession would share a timestamp). Schema defaults use
// strftime('%Y-%m-%dT%H:%M:%f000Z', 'now'), the same format at ms resolution.
const nowSQL = `stage_now()`

// nowPlusSQL is the current time shifted by a SQLite date modifier
// ("-1 hour"), at millisecond resolution.
func nowPlusSQL(modifier string) string {
	return `strftime('%Y-%m-%dT%H:%M:%f000Z','now','` + modifier + `')`
}

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func parseTime(v any) (time.Time, error) {
	switch v := v.(type) {
	case time.Time:
		return v.UTC(), nil
	case string:
		t, err := time.Parse(time.RFC3339Nano, v)
		return t.UTC(), err
	case []byte:
		t, err := time.Parse(time.RFC3339Nano, string(v))
		return t.UTC(), err
	}
	return time.Time{}, fmt.Errorf("cannot scan %T into a timestamp", v)
}

func newUUID() string { return uuid.NewString() }

// jsonArray encodes a list parameter for json_each (nil = []).
func jsonArray[T any](v []T) string {
	if v == nil {
		v = []T{}
	}
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // lists of ids and strings always encode
	}
	return string(b)
}

// conn and Tx run queries over database/sql with context-first signatures.
// Arguments: time.Time is stored as timeLayout text; slices (except
// []byte), maps and structs as JSON text. Scan targets: *time.Time and
// **time.Time parse timeLayout; slices, maps and structs decode JSON
// (NULL leaves them unset).
type sqlRunner interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type conn struct{ db *sql.DB }

// Tx is a transaction started as BEGIN IMMEDIATE (Open's _txlock): it holds
// the database write lock from its first statement, so transactions run
// one at a time; this replaces Postgres row locks and advisory locks.
type Tx struct{ tx *sql.Tx }

func (c conn) Exec(ctx context.Context, q string, args ...any) (execResult, error) {
	return execOn(ctx, c.db, q, args)
}
func (c conn) Query(ctx context.Context, q string, args ...any) (*rowSet, error) {
	return queryOn(ctx, c.db, q, args)
}
func (c conn) QueryRow(ctx context.Context, q string, args ...any) *oneRow {
	return &oneRow{c.db.QueryRowContext(ctx, q, convertArgs(args)...)}
}
func (t *Tx) Exec(ctx context.Context, q string, args ...any) (execResult, error) {
	return execOn(ctx, t.tx, q, args)
}
func (t *Tx) Query(ctx context.Context, q string, args ...any) (*rowSet, error) {
	return queryOn(ctx, t.tx, q, args)
}
func (t *Tx) QueryRow(ctx context.Context, q string, args ...any) *oneRow {
	return &oneRow{t.tx.QueryRowContext(ctx, q, convertArgs(args)...)}
}

// inTx runs fn in a BEGIN IMMEDIATE transaction, committing when fn returns
// nil. It rolls back on error or panic, so the write lock is never left held.
func (s *Store) inTx(ctx context.Context, fn func(tx *Tx) error) (err error) {
	sqlTx, err := s.pool.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = sqlTx.Rollback()
		}
	}()
	if err := fn(&Tx{sqlTx}); err != nil {
		return err
	}
	committed = true
	return sqlTx.Commit()
}

// queryRower is a conn or a *Tx, for single-statement reads.
type queryRower interface {
	QueryRow(ctx context.Context, query string, args ...any) *oneRow
}

type execResult struct{ n int64 }

func (r execResult) RowsAffected() int64 { return r.n }

func execOn(ctx context.Context, r sqlRunner, q string, args []any) (execResult, error) {
	res, err := r.ExecContext(ctx, q, convertArgs(args)...)
	if err != nil {
		return execResult{}, err
	}
	n, err := res.RowsAffected()
	return execResult{n}, err
}

func queryOn(ctx context.Context, r sqlRunner, q string, args []any) (*rowSet, error) {
	rs, err := r.QueryContext(ctx, q, convertArgs(args)...)
	if err != nil {
		return nil, err
	}
	return &rowSet{rs}, nil
}

// rowScanner is a row or the current row of rows.
type rowScanner interface {
	Scan(dest ...any) error
}

type oneRow struct{ r *sql.Row }

func (r *oneRow) Scan(dest ...any) error { return r.r.Scan(convertDests(dest)...) }

type rowSet struct{ r *sql.Rows }

func (r *rowSet) Next() bool             { return r.r.Next() }
func (r *rowSet) Err() error             { return r.r.Err() }
func (r *rowSet) Close()                 { _ = r.r.Close() }
func (r *rowSet) Scan(dest ...any) error { return r.r.Scan(convertDests(dest)...) }

// collectRows scans every row with fn and closes rows.
func collectRows[T any](r *rowSet, fn func(rowScanner) (T, error)) ([]T, error) {
	defer r.Close()
	out := []T{}
	for r.Next() {
		v, err := fn(r)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, r.Err()
}

// rowTo scans a single-column row into T.
func rowTo[T any](r rowScanner) (T, error) {
	var v T
	err := r.Scan(&v)
	return v, err
}

var (
	timeType    = reflect.TypeFor[time.Time]()
	valuerType  = reflect.TypeFor[driver.Valuer]()
	scannerType = reflect.TypeFor[sql.Scanner]()
)

func convertArgs(args []any) []any {
	out := make([]any, len(args))
	for i, a := range args {
		out[i] = convertArg(a)
	}
	return out
}

func convertArg(a any) any {
	if a == nil {
		return nil
	}
	if t, ok := a.(time.Time); ok {
		return formatTime(t)
	}
	if _, ok := a.(driver.Valuer); ok {
		return a
	}
	v := reflect.ValueOf(a)
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return nil
		}
		return convertArg(v.Elem().Interface())
	case reflect.Bool:
		return v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int64(v.Uint())
	case reflect.Float32, reflect.Float64:
		return v.Float()
	case reflect.String:
		return v.String()
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return a
		}
		if v.IsNil() {
			return "[]"
		}
	}
	b, err := json.Marshal(a)
	if err != nil {
		return jsonArgError{err}
	}
	return string(b)
}

// jsonArgError fails the statement when an argument cannot be encoded.
type jsonArgError struct{ err error }

func (e jsonArgError) Value() (driver.Value, error) { return nil, e.err }

func convertDests(dest []any) []any {
	out := make([]any, len(dest))
	for i, d := range dest {
		out[i] = convertDest(d)
	}
	return out
}

func convertDest(d any) any {
	switch d := d.(type) {
	case *time.Time:
		return timeDest{d}
	case **time.Time:
		return nullTimeDest{d}
	case sql.Scanner, *any, *[]byte:
		return d
	}
	t := reflect.TypeOf(d)
	if t == nil || t.Kind() != reflect.Pointer {
		return d
	}
	if t.Implements(scannerType) || t.Elem().Implements(valuerType) {
		return d
	}
	if plainKind(t.Elem()) || (t.Elem().Kind() == reflect.Pointer && plainKind(t.Elem().Elem())) {
		return d
	}
	return jsonDest{d}
}

// plainKind: scanned by database/sql itself.
func plainKind(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64, reflect.String:
		return true
	}
	return t == timeType
}

type timeDest struct{ p *time.Time }

func (d timeDest) Scan(v any) error {
	if v == nil {
		return fmt.Errorf("cannot scan NULL into time.Time")
	}
	t, err := parseTime(v)
	if err != nil {
		return err
	}
	*d.p = t
	return nil
}

type nullTimeDest struct{ p **time.Time }

func (d nullTimeDest) Scan(v any) error {
	if v == nil {
		*d.p = nil
		return nil
	}
	t, err := parseTime(v)
	if err != nil {
		return err
	}
	*d.p = &t
	return nil
}

type jsonDest struct{ p any }

func (d jsonDest) Scan(v any) error {
	var b []byte
	switch v := v.(type) {
	case nil:
		return nil
	case string:
		b = []byte(v)
	case []byte:
		b = v
	default:
		return fmt.Errorf("cannot scan %T into %T as JSON", v, d.p)
	}
	return json.Unmarshal(b, d.p)
}
