//go:build postgres

package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db/sqlite"
)

// Logical schema parity (docs/storage.md "Tests"): at the same version both
// engines hold the same tables and columns with the same nullability,
// primary, foreign and unique keys, and type class. Each engine keeps its
// native types, so types compare by class only (Postgres timestamptz, jsonb
// and text are all "text" against SQLite TEXT; boolean is "int" against
// SQLite's 0/1 INTEGER). Partial and expression indexes are engine-native
// and not compared.

// logicalColumn is a column's type class and nullability.
type logicalColumn struct {
	Type    string
	NotNull bool
}

// logicalTable is one table: columns by name, its primary key columns in
// order, and its foreign and unique keys rendered as sorted strings
// ("a,b->table(c,d)" and "a,b").
type logicalTable struct {
	Columns map[string]logicalColumn
	PK      []string
	FKs     []string
	Uniques []string
}

type logicalSchema map[string]*logicalTable

// parityAllowlist names objects ("table" or "table.column") left out of the
// comparison, with why. Every entry must exist on at least one engine.
var parityAllowlist = map[string]string{
	"goose_db_version": "goose bookkeeping in goose's own per-dialect shape, not Stage schema",
}

func (s logicalSchema) table(name string) *logicalTable {
	tb, ok := s[name]
	if !ok {
		tb = &logicalTable{Columns: map[string]logicalColumn{}}
		s[name] = tb
	}
	return tb
}

// compareSchemas lists every difference between the two engines' schemas
// outside allow, and every allowlist entry present on neither engine.
func compareSchemas(lite, pg logicalSchema, allow map[string]string) []string {
	var diffs []string
	has := func(s logicalSchema, obj string) bool {
		table, column, isColumn := strings.Cut(obj, ".")
		tb, ok := s[table]
		if !ok || !isColumn {
			return ok
		}
		_, ok = tb.Columns[column]
		return ok
	}
	for obj := range allow {
		if !has(lite, obj) && !has(pg, obj) {
			diffs = append(diffs, fmt.Sprintf("allowlist entry %s exists on neither engine: remove it", obj))
		}
	}
	tables := map[string]bool{}
	for name := range lite {
		tables[name] = true
	}
	for name := range pg {
		tables[name] = true
	}
	for _, name := range slices.Sorted(maps.Keys(tables)) {
		if _, ok := allow[name]; ok {
			continue
		}
		l, lok := lite[name]
		p, pok := pg[name]
		if !lok || !pok {
			diffs = append(diffs, fmt.Sprintf("table %s only on %s", name, engineOf(lok)))
			continue
		}
		columns := map[string]bool{}
		for c := range l.Columns {
			columns[c] = true
		}
		for c := range p.Columns {
			columns[c] = true
		}
		for _, c := range slices.Sorted(maps.Keys(columns)) {
			if _, ok := allow[name+"."+c]; ok {
				continue
			}
			lc, lok := l.Columns[c]
			pc, pok := p.Columns[c]
			switch {
			case !lok || !pok:
				diffs = append(diffs, fmt.Sprintf("column %s.%s only on %s", name, c, engineOf(lok)))
			case lc.Type != pc.Type:
				diffs = append(diffs, fmt.Sprintf("column %s.%s type %s on sqlite, %s on postgres", name, c, lc.Type, pc.Type))
			case lc.NotNull != pc.NotNull:
				diffs = append(diffs, fmt.Sprintf("column %s.%s not null %t on sqlite, %t on postgres", name, c, lc.NotNull, pc.NotNull))
			}
		}
		if !slices.Equal(l.PK, p.PK) {
			diffs = append(diffs, fmt.Sprintf("table %s primary key %v on sqlite, %v on postgres", name, l.PK, p.PK))
		}
		if !slices.Equal(l.FKs, p.FKs) {
			diffs = append(diffs, fmt.Sprintf("table %s foreign keys %v on sqlite, %v on postgres", name, l.FKs, p.FKs))
		}
		if !slices.Equal(l.Uniques, p.Uniques) {
			diffs = append(diffs, fmt.Sprintf("table %s unique keys %v on sqlite, %v on postgres", name, l.Uniques, p.Uniques))
		}
	}
	return diffs
}

func engineOf(onSQLite bool) string {
	if onSQLite {
		return "sqlite"
	}
	return "postgres"
}

// sqliteTypeClass follows SQLite's type affinity rules.
func sqliteTypeClass(declared string) string {
	d := strings.ToUpper(declared)
	switch {
	case strings.Contains(d, "INT"):
		return "int"
	case strings.Contains(d, "CHAR"), strings.Contains(d, "CLOB"), strings.Contains(d, "TEXT"):
		return "text"
	case d == "", strings.Contains(d, "BLOB"):
		return "bytes"
	case strings.Contains(d, "REAL"), strings.Contains(d, "FLOA"), strings.Contains(d, "DOUB"):
		return "real"
	}
	return "numeric"
}

// postgresTypeClass maps an information_schema data_type to its class.
func postgresTypeClass(dataType string) string {
	switch dataType {
	case "smallint", "integer", "bigint", "boolean":
		return "int"
	case "text", "character varying", "character", "jsonb", "json", "uuid",
		"timestamp with time zone", "timestamp without time zone", "date":
		return "text"
	case "bytea":
		return "bytes"
	case "real", "double precision":
		return "real"
	}
	return dataType
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// scanAll runs q and calls row for each row's scan.
func scanAll(t *testing.T, db querier, q string, row func(scan func(...any) error) error, args ...any) {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer rows.Close()
	for rows.Next() {
		if err := row(rows.Scan); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// sqliteSchema introspects a SQLite database's logical schema.
func sqliteSchema(t *testing.T, db querier) logicalSchema {
	t.Helper()
	s := logicalSchema{}
	var names []string
	scanAll(t, db, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`, func(scan func(...any) error) error {
		var n string
		if err := scan(&n); err != nil {
			return err
		}
		names = append(names, n)
		return nil
	})
	for _, name := range names {
		tb := s.table(name)
		pk := map[int]string{}
		scanAll(t, db, `SELECT name, type, "notnull", pk FROM pragma_table_info(?)`, func(scan func(...any) error) error {
			var col, typ string
			var notNull bool
			var pkPos int
			if err := scan(&col, &typ, &notNull, &pkPos); err != nil {
				return err
			}
			if pkPos > 0 {
				pk[pkPos] = col
				notNull = true // SQLite reports INTEGER PRIMARY KEY columns as nullable
			}
			tb.Columns[col] = logicalColumn{Type: sqliteTypeClass(typ), NotNull: notNull}
			return nil
		}, name)
		for _, pos := range slices.Sorted(maps.Keys(pk)) {
			tb.PK = append(tb.PK, pk[pos])
		}
		type fkPart struct {
			table, from string
			to          sql.NullString
		}
		fks := map[int][]fkPart{}
		scanAll(t, db, `SELECT id, "table", "from", "to" FROM pragma_foreign_key_list(?) ORDER BY id, seq`, func(scan func(...any) error) error {
			var id int
			var p fkPart
			if err := scan(&id, &p.table, &p.from, &p.to); err != nil {
				return err
			}
			fks[id] = append(fks[id], p)
			return nil
		}, name)
		for _, parts := range fks {
			var from, to []string
			for _, p := range parts {
				from = append(from, p.from)
				if p.to.Valid {
					to = append(to, p.to.String)
				}
			}
			if len(to) == 0 { // REFERENCES t: its primary key
				to = sqlitePK(t, db, parts[0].table)
			}
			tb.FKs = append(tb.FKs, fmt.Sprintf("%s->%s(%s)", strings.Join(from, ","), parts[0].table, strings.Join(to, ",")))
		}
		slices.Sort(tb.FKs)
		var unique []string
		scanAll(t, db, `SELECT name FROM pragma_index_list(?) WHERE "unique" = 1 AND origin <> 'pk' AND partial = 0`, func(scan func(...any) error) error {
			var n string
			if err := scan(&n); err != nil {
				return err
			}
			unique = append(unique, n)
			return nil
		}, name)
		for _, idx := range unique {
			var cols []string
			expression := false
			scanAll(t, db, `SELECT name FROM pragma_index_info(?) ORDER BY seqno`, func(scan func(...any) error) error {
				var c sql.NullString
				if err := scan(&c); err != nil {
					return err
				}
				expression = expression || !c.Valid
				cols = append(cols, c.String)
				return nil
			}, idx)
			if !expression {
				tb.Uniques = append(tb.Uniques, strings.Join(cols, ","))
			}
		}
		slices.Sort(tb.Uniques)
	}
	return s
}

func sqlitePK(t *testing.T, db querier, table string) []string {
	t.Helper()
	var cols []string
	scanAll(t, db, `SELECT name FROM pragma_table_info(?) WHERE pk > 0 ORDER BY pk`, func(scan func(...any) error) error {
		var c string
		if err := scan(&c); err != nil {
			return err
		}
		cols = append(cols, c)
		return nil
	}, table)
	return cols
}

// postgresSchema introspects the current schema of a Postgres database.
func postgresSchema(t *testing.T, db querier) logicalSchema {
	t.Helper()
	s := logicalSchema{}
	scanAll(t, db, `SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'`, func(scan func(...any) error) error {
		var n string
		if err := scan(&n); err != nil {
			return err
		}
		s.table(n)
		return nil
	})
	scanAll(t, db, `SELECT c.table_name, c.column_name, c.data_type, c.is_nullable = 'NO'
		FROM information_schema.columns c JOIN information_schema.tables t
		  ON t.table_schema = c.table_schema AND t.table_name = c.table_name AND t.table_type = 'BASE TABLE'
		WHERE c.table_schema = current_schema()`, func(scan func(...any) error) error {
		var table, col, typ string
		var notNull bool
		if err := scan(&table, &col, &typ, &notNull); err != nil {
			return err
		}
		s.table(table).Columns[col] = logicalColumn{Type: postgresTypeClass(typ), NotNull: notNull}
		return nil
	})
	const cols = `(SELECT string_agg(a.attname, ',' ORDER BY k.ord) FROM unnest(%s) WITH ORDINALITY AS k(attnum, ord)
		JOIN pg_attribute a ON a.attrelid = %s AND a.attnum = k.attnum)`
	scanAll(t, db, `SELECT t.relname, i.indisprimary, `+fmt.Sprintf(cols, "i.indkey::int2[]", "i.indrelid")+`
		FROM pg_index i JOIN pg_class t ON t.oid = i.indrelid JOIN pg_namespace n ON n.oid = t.relnamespace
		WHERE n.nspname = current_schema() AND i.indisunique AND i.indpred IS NULL AND i.indexprs IS NULL`, func(scan func(...any) error) error {
		var table, columns string
		var primary bool
		if err := scan(&table, &primary, &columns); err != nil {
			return err
		}
		tb := s.table(table)
		if primary {
			tb.PK = strings.Split(columns, ",")
		} else {
			tb.Uniques = append(tb.Uniques, columns)
		}
		return nil
	})
	scanAll(t, db, `SELECT t.relname, f.relname, `+fmt.Sprintf(cols, "c.conkey", "c.conrelid")+`, `+fmt.Sprintf(cols, "c.confkey", "c.confrelid")+`
		FROM pg_constraint c JOIN pg_class t ON t.oid = c.conrelid JOIN pg_class f ON f.oid = c.confrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		WHERE c.contype = 'f' AND n.nspname = current_schema()`, func(scan func(...any) error) error {
		var table, ref, from, to string
		if err := scan(&table, &ref, &from, &to); err != nil {
			return err
		}
		tb := s.table(table)
		tb.FKs = append(tb.FKs, fmt.Sprintf("%s->%s(%s)", from, ref, to))
		return nil
	})
	for _, tb := range s {
		slices.Sort(tb.FKs)
		slices.Sort(tb.Uniques)
	}
	return s
}

// TestSchemaParity compares both engines' schemas at their heads. Until the
// Postgres baseline (#403) only the SQLite side runs.
func TestSchemaParity(t *testing.T) {
	ctx := context.Background()
	lite, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), sqlite.DBFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lite.Close() })
	if err := sqlite.Migrate(ctx, lite); err != nil {
		t.Fatal(err)
	}
	liteSchema := sqliteSchema(t, lite)
	tasks, ok := liteSchema["tasks"]
	if !ok || !slices.Equal(tasks.PK, []string{"id"}) || tasks.Columns["project_id"] != (logicalColumn{Type: "int", NotNull: true}) || !slices.Contains(tasks.FKs, "project_id->projects(id)") {
		t.Fatalf("SQLite introspection of tasks: %+v", tasks)
	}
	if MaxVersion() == 0 {
		t.Skip("no Postgres baseline yet (#403): the cross-engine comparison runs once it exists")
	}
	if MaxVersion() != sqlite.MaxVersion() {
		t.Fatalf("heads differ: Postgres %d, SQLite %d", MaxVersion(), sqlite.MaxVersion())
	}
	pool := freshDB(t)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if diffs := compareSchemas(liteSchema, postgresSchema(t, pool), parityAllowlist); len(diffs) > 0 {
		t.Fatalf("schema parity (fix the migration, or allowlist an engine-only object with why):\n%s", strings.Join(diffs, "\n"))
	}
}

// The comparator and both introspections on one hand-written schema.
func TestSchemaParityCompare(t *testing.T) {
	ctx := context.Background()
	const ddlLite = `
CREATE TABLE parent (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE);
CREATE TABLE child (
 parent_id INTEGER NOT NULL REFERENCES parent(id),
 n INTEGER NOT NULL,
 at TEXT NOT NULL,
 doc TEXT,
 done INTEGER NOT NULL DEFAULT 0 CHECK (done IN (0,1)),
 blob BLOB,
 PRIMARY KEY (parent_id, n)
);
CREATE UNIQUE INDEX child_at ON child(at, n);
CREATE UNIQUE INDEX child_partial ON child(doc) WHERE doc IS NOT NULL;`
	const ddlPG = `
CREATE TABLE parent (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, name text NOT NULL UNIQUE);
CREATE TABLE child (
 parent_id bigint NOT NULL REFERENCES parent(id),
 n bigint NOT NULL,
 at timestamptz NOT NULL,
 doc jsonb,
 done boolean NOT NULL DEFAULT false,
 blob bytea,
 PRIMARY KEY (parent_id, n)
);
CREATE UNIQUE INDEX child_at ON child(at, n);
CREATE UNIQUE INDEX child_partial ON child((doc->>'k')) WHERE doc IS NOT NULL;`

	lite, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), sqlite.DBFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lite.Close() })
	if _, err := lite.ExecContext(ctx, ddlLite); err != nil {
		t.Fatal(err)
	}
	pool := freshDB(t)
	if _, err := pool.ExecContext(ctx, ddlPG); err != nil {
		t.Fatal(err)
	}
	liteSchema, pgSchema := sqliteSchema(t, lite), postgresSchema(t, pool)
	if diffs := compareSchemas(liteSchema, pgSchema, nil); len(diffs) > 0 {
		t.Fatalf("equivalent schemas differ:\n%s", strings.Join(diffs, "\n"))
	}
	want := logicalTable{
		Columns: map[string]logicalColumn{
			"parent_id": {"int", true}, "n": {"int", true}, "at": {"text", true},
			"doc": {"text", false}, "done": {"int", true}, "blob": {"bytes", false},
		},
		PK: []string{"parent_id", "n"}, FKs: []string{"parent_id->parent(id)"}, Uniques: []string{"at,n"},
	}
	if got := *pgSchema["child"]; !maps.Equal(got.Columns, want.Columns) || !slices.Equal(got.PK, want.PK) || !slices.Equal(got.FKs, want.FKs) || !slices.Equal(got.Uniques, want.Uniques) {
		t.Fatalf("child: %+v, want %+v", got, want)
	}

	for _, tc := range []struct {
		name  string
		ddl   string
		allow map[string]string
		want  []string
	}{
		{"nullability", `ALTER TABLE child ALTER COLUMN at DROP NOT NULL`, nil,
			[]string{"column child.at not null true on sqlite, false on postgres"}},
		{"type class", `ALTER TABLE child ALTER COLUMN n TYPE text`, nil,
			[]string{"column child.n type int on sqlite, text on postgres"}},
		{"extra column", `ALTER TABLE child ADD COLUMN extra text`, nil,
			[]string{"column child.extra only on postgres"}},
		{"allowlisted column", `ALTER TABLE child ADD COLUMN extra text`, map[string]string{"child.extra": "postgres-only"}, nil},
		{"extra table", `CREATE TABLE pg_only (id bigint PRIMARY KEY)`, nil,
			[]string{"table pg_only only on postgres"}},
		{"allowlisted table", `CREATE TABLE pg_only (id bigint PRIMARY KEY)`, map[string]string{"pg_only": "postgres-only"}, nil},
		{"stale allowlist", `SELECT 1`, map[string]string{"gone": "removed"},
			[]string{"allowlist entry gone exists on neither engine: remove it"}},
		{"unique key", `DROP INDEX child_at`, nil,
			[]string{"table child unique keys [at,n] on sqlite, [] on postgres"}},
		{"foreign key", `ALTER TABLE child DROP CONSTRAINT child_parent_id_fkey`, nil,
			[]string{"table child foreign keys [parent_id->parent(id)] on sqlite, [] on postgres"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := freshDB(t)
			if _, err := pool.ExecContext(ctx, ddlPG); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.ExecContext(ctx, tc.ddl); err != nil {
				t.Fatal(err)
			}
			if got := compareSchemas(liteSchema, postgresSchema(t, pool), tc.allow); !slices.Equal(got, tc.want) {
				t.Fatalf("diffs %q, want %q", got, tc.want)
			}
		})
	}
}
