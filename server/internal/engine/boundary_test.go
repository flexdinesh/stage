package engine_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestPackagesDoNotImportEachOther(t *testing.T) {
	packages := []string{"coordinator", "observe", "discovery"}
	forbidden := map[string]bool{
		"coordinator": true, "observe": true, "discovery": true,
		"engine": true, "api": true, "serve": true,
		"remote": true, "workerchan": true, "worker": true,
	}
	const internal = "github.com/flexdinesh/stage/server/internal/"
	for _, pkg := range packages {
		t.Run(pkg, func(t *testing.T) {
			dir := filepath.Join("..", pkg)
			files, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range files {
				name := entry.Name()
				if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
					continue
				}
				file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, parser.ImportsOnly)
				if err != nil {
					t.Fatal(err)
				}
				for _, spec := range file.Imports {
					path, err := strconv.Unquote(spec.Path.Value)
					if err != nil {
						t.Fatal(err)
					}
					if rel, ok := strings.CutPrefix(path, internal); ok {
						dep, _, _ := strings.Cut(rel, "/")
						if forbidden[dep] {
							t.Errorf("%s/%s imports %s", pkg, name, path)
						}
					}
				}
			}
		})
	}
}
