package sqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

// migratedFile writes a migrated database with stmts applied, closes it
// and returns its path.
func migratedFile(t *testing.T, stmts ...string) string {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), DBFile)
	d, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, d); err != nil {
		t.Fatal(err)
	}
	for _, q := range stmts {
		if _, err := d.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func openCopy(t *testing.T, path string) *CopySource {
	t.Helper()
	s, err := OpenCopySource(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// Tables leaves out SQLite's own tables, goose's and the FTS5 tables with
// their shadow tables; Sequences reads AUTOINCREMENT high-water marks.
func TestCopySourceTablesAndSequences(t *testing.T) {
	ctx := context.Background()
	path := migratedFile(t,
		`INSERT INTO snippets (id, name, body) VALUES (7, 'a', 'b')`,
		`DELETE FROM snippets WHERE id = 7`)
	s := openCopy(t, path)
	v, err := s.Version(ctx)
	if err != nil || v != MaxVersion() {
		t.Fatalf("version %d %v", v, err)
	}
	tables, err := s.Tables(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for name := range tables {
		if strings.HasPrefix(name, "sqlite_") || strings.Contains(name, "_fts") || name == "goose_db_version" {
			t.Errorf("engine-internal table %s listed", name)
		}
	}
	if cols := tables["tasks"]; len(cols) == 0 || cols[0] != "id" {
		t.Fatalf("tasks columns %v", cols)
	}
	seqs, err := s.Sequences(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if seqs["snippets"] != 7 {
		t.Fatalf("snippets high-water %d, want 7", seqs["snippets"])
	}
}

func TestCopyValue(t *testing.T) {
	cases := []struct {
		in   any
		kind store.CopyKind
		want any
		err  string
	}{
		{nil, store.CopyTime, nil, ""},
		{int64(5), store.CopyInt, int64(5), ""},
		{int64(1), store.CopyBool, true, ""},
		{int64(0), store.CopyBool, false, ""},
		{int64(2), store.CopyBool, nil, "not 0 or 1"},
		{int64(3), store.CopyReal, float64(3), ""},
		{"héllo", store.CopyText, "héllo", ""},
		{"a\x00b", store.CopyText, nil, "NUL"},
		{string([]byte{0xff}), store.CopyText, nil, "UTF-8"},
		{"2026-11-01T10:00:00.123Z", store.CopyTime, time.Date(2026, 11, 1, 10, 0, 0, 123000000, time.UTC), ""},
		{"2026-11-01T10:00:00.1234567Z", store.CopyTime, time.Date(2026, 11, 1, 10, 0, 0, 123456000, time.UTC), ""},
		{"2026-11-01T12:00:00+02:00", store.CopyTime, time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), ""},
		{"yesterday", store.CopyTime, nil, "timestamp"},
		{int64(1), store.CopyTime, nil, "does not convert"},
		{`{"a": 1}`, store.CopyJSON, json.RawMessage(`{"a": 1}`), ""},
		{`{"a":`, store.CopyJSON, nil, "invalid JSON"},
		{[]byte{0, 1}, store.CopyBytes, []byte{0, 1}, ""},
		{"x", store.CopyInt, nil, "does not convert"},
	}
	for _, c := range cases {
		got, err := copyValue(c.in, c.kind)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("copyValue(%q, %s) error %v, want %q", c.in, c.kind, err, c.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("copyValue(%q, %s): %v", c.in, c.kind, err)
			continue
		}
		switch w := c.want.(type) {
		case time.Time:
			if g, ok := got.(time.Time); !ok || !g.Equal(w) || g.Location() != time.UTC {
				t.Errorf("copyValue(%q, %s) = %v, want %v", c.in, c.kind, got, w)
			}
		case []byte:
			if g, ok := got.([]byte); !ok || !bytes.Equal(g, w) {
				t.Errorf("copyValue(%q, %s) = %v, want %v", c.in, c.kind, got, w)
			}
		case json.RawMessage:
			if g, ok := got.(json.RawMessage); !ok || string(g) != string(w) {
				t.Errorf("copyValue(%q, %s) = %v, want %v", c.in, c.kind, got, w)
			}
		default:
			if got != c.want {
				t.Errorf("copyValue(%q, %s) = %v, want %v", c.in, c.kind, got, c.want)
			}
		}
	}
}

// A value that does not convert names its table, column and row.
func TestCopySourceRowsNameBadValue(t *testing.T) {
	path := migratedFile(t, `UPDATE users SET created_at = 'soon' WHERE id = 1`)
	s := openCopy(t, path)
	tbl := store.CopyTable{Name: "users", Columns: []store.CopyColumn{
		{Name: "id", Kind: store.CopyInt}, {Name: "created_at", Kind: store.CopyTime},
	}}
	err := s.Rows(context.Background(), tbl, func(store.CopyRow) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "users.created_at, row 1") {
		t.Fatalf("got %v", err)
	}
}

// The source connection cannot write, and reading leaves the directory as
// it was (no WAL or shm file, same bytes).
func TestCopySourceReadOnly(t *testing.T) {
	ctx := context.Background()
	path := migratedFile(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := openCopy(t, path)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO snippets (name, body) VALUES ('x', 'y')`); err == nil {
		t.Fatal("the copy source wrote")
	}
	tbl := store.CopyTable{Name: "users", Columns: []store.CopyColumn{{Name: "id", Kind: store.CopyInt}}}
	n := 0
	if err := s.Rows(ctx, tbl, func(store.CopyRow) error { n++; return nil }); err != nil || n != 1 {
		t.Fatalf("rows %d %v", n, err)
	}
	s.Close()
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("source file changed")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("source dir holds %v, want only %s", names, DBFile)
	}
}

// With a WAL left behind (a server that crashed), the copy reads its
// commits too.
func TestCopySourceReadsWAL(t *testing.T) {
	ctx := context.Background()
	path := migratedFile(t)
	w, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.ExecContext(ctx, `INSERT INTO snippets (name, body) VALUES ('in-wal', 'b')`); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(path + "-wal"); err != nil || st.Size() == 0 {
		t.Fatalf("no WAL content: %v", err)
	}
	s := openCopy(t, path)
	tbl := store.CopyTable{Name: "snippets", Columns: []store.CopyColumn{{Name: "name", Kind: store.CopyText}}}
	var names []any
	if err := s.Rows(ctx, tbl, func(r store.CopyRow) error { names = append(names, r[0]); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "in-wal" {
		t.Fatalf("rows %v, want the WAL's row", names)
	}
}

func TestOpenCopySourceRefusesMissingAndDir(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if _, err := OpenCopySource(ctx, filepath.Join(dir, DBFile)); err == nil {
		t.Fatal("opened a missing file")
	}
	if _, err := os.Stat(filepath.Join(dir, DBFile)); !os.IsNotExist(err) {
		t.Fatal("opening created the file")
	}
	if _, err := OpenCopySource(ctx, dir); err == nil || !strings.Contains(err.Error(), "not a file") {
		t.Fatalf("directory: %v", err)
	}
}
