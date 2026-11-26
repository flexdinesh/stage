package gitmeta

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// Git evidence is read only by machine.Ops, on the machine where the files
// live, never by an adapter (ADR 0007 decision 11). Normalizing a remote a
// harness reports (RemoteIdentity) is allowed anywhere.
func TestOnlyMachineOpsReadCheckouts(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	allowed := []string{filepath.Join(root, "machine") + string(filepath.Separator), filepath.Join(root, "gitmeta") + string(filepath.Separator)}
	var offenders []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		for _, a := range allowed {
			if strings.HasPrefix(path, a) {
				return nil
			}
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "gitmeta" && sel.Sel.Name == "Inspect" {
					rel, _ := filepath.Rel(root, path)
					offenders = append(offenders, rel+": gitmeta."+sel.Sel.Name)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("Git evidence read outside machine.Ops (fill it in machine.Ops.Inventory): %v", offenders)
	}
}
