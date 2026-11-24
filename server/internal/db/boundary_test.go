package db

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// adapters maps each storage adapter directory (relative to the server
// module) to the driver import paths only it may use (docs/storage.md
// "Layout").
var adapters = map[string][]string{
	"internal/db/sqlite":   {"modernc.org/sqlite"},
	"internal/db/postgres": {"github.com/jackc/pgx", "github.com/lib/pq"},
}

// rawSQL matches a SQL statement in a string literal.
var rawSQL = regexp.MustCompile(`\bSELECT\b[\s\S]*\bFROM\b|\bINSERT\s+(OR\s+[A-Z]+\s+)?INTO\b|\bUPDATE\s+\w+\s+SET\b|\bDELETE\s+FROM\b|\b(CREATE|ALTER|DROP)\s+(TABLE|INDEX|TRIGGER|VIEW)\b|\bPRAGMA\s+\w+`)

// TestDriverImportBoundary: a database driver is imported only by its
// adapter (tests included), and outside the adapters no non-test file
// imports database/sql or holds a SQL statement. SQL belongs in
// internal/db/<engine>; rules in internal/db reach it through the ports in
// internal/db/store.
func TestDriverImportBoundary(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	files := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "testdata" || name == "node_modules" || strings.HasPrefix(name, ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		files++
		checkFile(t, rel, f)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files == 0 {
		t.Fatal("no Go files scanned")
	}
}

// adapterOf is the adapter directory holding rel ("" outside adapters).
func adapterOf(rel string) string {
	for dir := range adapters {
		if strings.HasPrefix(rel, dir+"/") {
			return dir
		}
	}
	return ""
}

func checkFile(t *testing.T, rel string, f *ast.File) {
	t.Helper()
	adapter := adapterOf(rel)
	test := strings.HasSuffix(rel, "_test.go")
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			t.Errorf("%s: import %s: %v", rel, imp.Path.Value, err)
			continue
		}
		for dir, drivers := range adapters {
			for _, driver := range drivers {
				if (path == driver || strings.HasPrefix(path, driver+"/")) && adapter != dir {
					t.Errorf("%s imports %s: only %s may import its driver; reach storage through internal/db", rel, path, dir)
				}
			}
		}
		if path == "database/sql" && adapter == "" && !test {
			t.Errorf("%s imports database/sql: SQL access belongs in a storage adapter (internal/db/<engine>)", rel)
		}
	}
	if adapter != "" || test {
		return
	}
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if s, err := strconv.Unquote(lit.Value); err == nil && rawSQL.MatchString(s) {
			t.Errorf("%s: SQL statement in a string literal (%q): move it into a storage adapter repository", rel, firstLine(s))
		}
		return true
	})
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}
