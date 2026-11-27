//go:build postgres

package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3"
)

// The Postgres side of `stage db copy` (docs/storage.md "Data copy and
// cutover"): *DB is the store.CopyTarget.
var _ store.CopyTarget = (*DB)(nil)

// baselineSeeds identifies default rows that do not occupy a copy target.
// Populated harness settings or status, and any secret, occupy the target.
var baselineSeeds = map[string]string{
	"users":                    "id = 1 AND email IS NULL AND role = 'admin' AND status = 'active'",
	"installation_access":      "id = 1 AND mode = 'personal'",
	"machines":                 "id = 1",
	"preferences":              "id = 1",
	"notification_preferences": `user_id = 1 AND notify_events = '["needs_input","failed","done","ready"]'::jsonb`,
	"harness_settings":         `machine_id = 1 AND settings = '{}'::jsonb`,
	"harness_sync_status":      `last_error IS NULL AND last_full_sync_at IS NULL`,
}

// State reads the version, counts the schema's tables, and (with a schema)
// finds the first table, by name, holding rows beyond baselineSeeds.
func (d *DB) State(ctx context.Context) (store.CopyTargetState, error) {
	var st store.CopyTargetState
	v, err := CurrentVersion(ctx, d.sqlDB)
	if err != nil {
		return st, err
	}
	st.Version = v
	if err := d.pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'`,
	).Scan(&st.Tables); err != nil {
		return st, fmt.Errorf("target tables: %w", err)
	}
	if v == 0 {
		return st, nil
	}
	names, err := d.tableNames(ctx)
	if err != nil {
		return st, err
	}
	for _, name := range names {
		q := `SELECT EXISTS (SELECT 1 FROM ` + pgx.Identifier{name}.Sanitize()
		if seed, ok := baselineSeeds[name]; ok {
			q += ` WHERE NOT (` + seed + `)`
		}
		var occupied bool
		if err := d.pool.QueryRow(ctx, q+`)`).Scan(&occupied); err != nil {
			return st, fmt.Errorf("target rows in %s: %w", name, err)
		}
		if occupied {
			st.Occupied = name
			return st, nil
		}
	}
	return st, nil
}

// tableNames are the schema's Stage tables (not goose's), by name.
func (d *DB) tableNames(ctx context.Context) ([]string, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT table_name FROM information_schema.tables
 WHERE table_schema = current_schema() AND table_type = 'BASE TABLE' AND table_name <> $1 ORDER BY table_name`,
		goose.DefaultTablename)
	if err != nil {
		return nil, fmt.Errorf("target tables: %w", err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("target tables: %w", err)
	}
	return names, nil
}

// copyKinds maps information_schema data types to copy kinds; any other
// type fails the copy rather than guessing.
var copyKinds = map[string]store.CopyKind{
	"bigint":                   store.CopyInt,
	"integer":                  store.CopyInt,
	"smallint":                 store.CopyInt,
	"boolean":                  store.CopyBool,
	"double precision":         store.CopyReal,
	"real":                     store.CopyReal,
	"text":                     store.CopyText,
	"timestamp with time zone": store.CopyTime,
	"jsonb":                    store.CopyJSON,
	"bytea":                    store.CopyBytes,
}

// Schema lists every Stage table with its columns' kinds and identity
// column, parents before children; ties in name order.
func (d *DB) Schema(ctx context.Context) ([]store.CopyTable, error) {
	names, err := d.tableNames(ctx)
	if err != nil {
		return nil, err
	}
	tables := make(map[string]*store.CopyTable, len(names))
	for _, n := range names {
		tables[n] = &store.CopyTable{Name: n}
	}
	rows, err := d.pool.Query(ctx,
		`SELECT table_name, column_name, data_type, is_identity = 'YES'
 FROM information_schema.columns WHERE table_schema = current_schema() ORDER BY table_name, ordinal_position`)
	if err != nil {
		return nil, fmt.Errorf("target columns: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var table, column, typ string
		var identity bool
		if err := rows.Scan(&table, &column, &typ, &identity); err != nil {
			return nil, fmt.Errorf("target columns: %w", err)
		}
		t, ok := tables[table]
		if !ok {
			continue
		}
		kind, ok := copyKinds[typ]
		if !ok {
			return nil, fmt.Errorf("target column %s.%s: type %s has no copy encoding", table, column, typ)
		}
		t.Columns = append(t.Columns, store.CopyColumn{Name: column, Kind: kind})
		if identity {
			t.Identity = column
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("target columns: %w", err)
	}
	parents, err := d.foreignKeys(ctx)
	if err != nil {
		return nil, err
	}
	order, err := fkOrder(names, parents)
	if err != nil {
		return nil, err
	}
	out := make([]store.CopyTable, len(order))
	for i, n := range order {
		out[i] = *tables[n]
	}
	return out, nil
}

// foreignKeys maps each table to the tables its foreign keys reference.
func (d *DB) foreignKeys(ctx context.Context) (map[string][]string, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT child.relname, parent.relname FROM pg_constraint k
 JOIN pg_class child ON child.oid = k.conrelid
 JOIN pg_class parent ON parent.oid = k.confrelid
 JOIN pg_namespace n ON n.oid = child.relnamespace
 WHERE k.contype = 'f' AND n.nspname = current_schema()`)
	if err != nil {
		return nil, fmt.Errorf("target foreign keys: %w", err)
	}
	defer rows.Close()
	parents := map[string][]string{}
	for rows.Next() {
		var child, parent string
		if err := rows.Scan(&child, &parent); err != nil {
			return nil, fmt.Errorf("target foreign keys: %w", err)
		}
		parents[child] = append(parents[child], parent)
	}
	return parents, rows.Err()
}

// fkOrder orders names so every table follows the tables it references
// (self-references ignored), ties in name order. A cycle fails: rows could
// not be inserted with immediate foreign-key checks.
func fkOrder(names []string, parents map[string][]string) ([]string, error) {
	pending := map[string]map[string]bool{}
	for _, n := range names {
		pending[n] = map[string]bool{}
		for _, p := range parents[n] {
			if p != n {
				pending[n][p] = true
			}
		}
	}
	var order []string
	for len(pending) > 0 {
		var ready []string
		for n, ps := range pending {
			if len(ps) == 0 {
				ready = append(ready, n)
			}
		}
		if len(ready) == 0 {
			left := make([]string, 0, len(pending))
			for n := range pending {
				left = append(left, n)
			}
			slices.Sort(left)
			return nil, fmt.Errorf("target foreign keys form a cycle among %s", strings.Join(left, ", "))
		}
		slices.Sort(ready)
		for _, n := range ready {
			delete(pending, n)
			for _, ps := range pending {
				delete(ps, n)
			}
		}
		order = append(order, ready...)
	}
	return order, nil
}

// Load runs fn in one READ COMMITTED transaction (no retry: the target is
// held by the copy's ownership lease, so nothing contends).
func (d *DB) Load(ctx context.Context, fn func(store.CopyLoad) error) error {
	tx, err := d.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return contextErr(ctx, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	if err := fn(copyLoad{tx}); err != nil {
		return contextErr(ctx, err)
	}
	committed = true
	return contextErr(ctx, tx.Commit(ctx))
}

type copyLoad struct{ tx pgx.Tx }

func (l copyLoad) Clear(ctx context.Context, tables []store.CopyTable) error {
	if len(tables) == 0 {
		return nil
	}
	names := make([]string, len(tables))
	for i, t := range tables {
		names[i] = pgx.Identifier{t.Name}.Sanitize()
	}
	if _, err := l.tx.Exec(ctx, `TRUNCATE `+strings.Join(names, ", ")); err != nil {
		return fmt.Errorf("clear target: %w", err)
	}
	return nil
}

func (l copyLoad) Insert(ctx context.Context, t store.CopyTable, rows []store.CopyRow) error {
	cols := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		cols[i] = c.Name
	}
	if _, err := l.tx.CopyFrom(ctx, pgx.Identifier{t.Name}, cols, pgx.CopyFromRows(rows)); err != nil {
		return fmt.Errorf("write %s: %w", t.Name, err)
	}
	return nil
}

func (l copyLoad) RestartIdentity(ctx context.Context, t store.CopyTable, last int64) error {
	next, called := last, true
	if last <= 0 {
		next, called = 1, false
	}
	if _, err := l.tx.Exec(ctx, `SELECT setval(pg_get_serial_sequence($1, $2), $3, $4)`,
		pgx.Identifier{t.Name}.Sanitize(), t.Identity, next, called); err != nil {
		return fmt.Errorf("restart identity of %s: %w", t.Name, err)
	}
	return nil
}

func (l copyLoad) Rows(ctx context.Context, t store.CopyTable, fn func(store.CopyRow) error) error {
	cols := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		cols[i] = pgx.Identifier{c.Name}.Sanitize()
	}
	rows, err := l.tx.Query(ctx, `SELECT `+strings.Join(cols, ", ")+` FROM `+pgx.Identifier{t.Name}.Sanitize())
	if err != nil {
		return fmt.Errorf("read back %s: %w", t.Name, err)
	}
	defer rows.Close()
	for rows.Next() {
		row, err := scanCopyRow(rows, t)
		if err != nil {
			return fmt.Errorf("read back %s: %w", t.Name, err)
		}
		if err := fn(row); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read back %s: %w", t.Name, err)
	}
	return nil
}

// scanCopyRow scans one row into each column kind's Go type (nil for NULL).
func scanCopyRow(rows pgx.Rows, t store.CopyTable) (store.CopyRow, error) {
	dest := make([]any, len(t.Columns))
	for i, c := range t.Columns {
		switch c.Kind {
		case store.CopyInt:
			dest[i] = new(*int64)
		case store.CopyBool:
			dest[i] = new(*bool)
		case store.CopyReal:
			dest[i] = new(*float64)
		case store.CopyText:
			dest[i] = new(*string)
		case store.CopyTime:
			dest[i] = new(*time.Time)
		case store.CopyJSON, store.CopyBytes:
			dest[i] = new(*[]byte)
		default:
			return nil, fmt.Errorf("column %s: unknown kind %d", c.Name, c.Kind)
		}
	}
	if err := rows.Scan(dest...); err != nil {
		return nil, err
	}
	row := make(store.CopyRow, len(t.Columns))
	for i, c := range t.Columns {
		row[i] = derefCopy(dest[i], c.Kind)
	}
	return row, nil
}

func derefCopy(d any, kind store.CopyKind) any {
	switch p := d.(type) {
	case **int64:
		if *p != nil {
			return **p
		}
	case **bool:
		if *p != nil {
			return **p
		}
	case **float64:
		if *p != nil {
			return **p
		}
	case **string:
		if *p != nil {
			return **p
		}
	case **time.Time:
		if *p != nil {
			return (**p).UTC()
		}
	case **[]byte:
		if *p != nil {
			if kind == store.CopyJSON {
				return json.RawMessage(**p)
			}
			return **p
		}
	}
	return nil
}
