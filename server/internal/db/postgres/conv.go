//go:build postgres

package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// nowSQL is the current time for adapter-side stamps (updated_at, read_at,
// ...): clock_timestamp() advances within a transaction like SQLite's
// stage_now(), so writes in quick succession keep their order. Schema
// defaults use now() (the transaction's start). Business rules never read
// it; they pass Go time (docs/storage.md "Encodings").
const nowSQL = `clock_timestamp()`

// nowPlusSQL is the current time shifted by an interval ("-1 hour").
func nowPlusSQL(interval string) string {
	return `(clock_timestamp() + interval '` + interval + `')`
}

// pgTime is t as stored: UTC, truncated to microseconds (timestamptz
// precision), so a value round-trips equal on both engines.
func pgTime(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

func newUUID() string { return uuid.NewString() }

// jsonArray encodes a list as JSON text (nil = []) for a jsonb parameter.
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

// pgRunner is a pool or a transaction.
type pgRunner interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// runner runs queries with argument and scan conversions. Arguments:
// time.Time is normalized (pgTime); nil slices become empty slices (jsonb []
// or an empty array); named basic kinds become their base kind; slices,
// maps and structs pass to pgx, which encodes them by the parameter's type
// (arrays, jsonb). Scan targets: *time.Time and **time.Time read UTC; other
// slices, maps and structs decode JSON (NULL leaves them unset).
type runner struct{ r pgRunner }

func (t *runner) Exec(ctx context.Context, q string, args ...any) (execResult, error) {
	tag, err := t.r.Exec(ctx, q, convertArgs(args)...)
	if err != nil {
		return execResult{}, err
	}
	return execResult{tag.RowsAffected()}, nil
}

func (t *runner) Query(ctx context.Context, q string, args ...any) (*rowSet, error) {
	rs, err := t.r.Query(ctx, q, convertArgs(args)...)
	if err != nil {
		return nil, err
	}
	return &rowSet{rs}, nil
}

func (t *runner) QueryRow(ctx context.Context, q string, args ...any) *oneRow {
	return &oneRow{t.r.QueryRow(ctx, q, convertArgs(args)...)}
}

type execResult struct{ n int64 }

func (r execResult) RowsAffected() int64 { return r.n }

// rowScanner is a row or the current row of rows.
type rowScanner interface {
	Scan(dest ...any) error
}

type oneRow struct{ r pgx.Row }

func (r *oneRow) Scan(dest ...any) error { return r.r.Scan(convertDests(dest)...) }

type rowSet struct{ r pgx.Rows }

func (r *rowSet) Next() bool             { return r.r.Next() }
func (r *rowSet) Err() error             { return r.r.Err() }
func (r *rowSet) Close()                 { r.r.Close() }
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
	switch a := a.(type) {
	case time.Time:
		return pgTime(a)
	case json.RawMessage:
		if a == nil {
			return nil
		}
		return string(a)
	case []byte, driver.Valuer:
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
		if v.IsNil() {
			return reflect.MakeSlice(v.Type(), 0, 0).Interface()
		}
	}
	return a
}

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
	case sql.Scanner, *any, *[]byte, *json.RawMessage:
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

// plainKind: scanned by pgx itself.
func plainKind(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64, reflect.String:
		return true
	}
	return t == timeType
}

func scanTime(v any) (time.Time, error) {
	switch v := v.(type) {
	case time.Time:
		return pgTime(v), nil
	case string:
		t, err := time.Parse(time.RFC3339Nano, v)
		return pgTime(t), err
	}
	return time.Time{}, fmt.Errorf("cannot scan %T into a timestamp", v)
}

type timeDest struct{ p *time.Time }

func (d timeDest) Scan(v any) error {
	if v == nil {
		return fmt.Errorf("cannot scan NULL into time.Time")
	}
	t, err := scanTime(v)
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
	t, err := scanTime(v)
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
