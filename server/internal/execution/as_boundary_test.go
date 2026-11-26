package execution_test

import (
	"github.com/flexdinesh/stage/server/internal/execution"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// optional are the optional adapter interfaces. A remote machine's proxy
// implements them all in Go and reports which ones its worker's adapter
// has (execution.Partial), so a type assertion would offer what the
// harness lacks: product code uses execution.As instead.
var optional = execution.OptionalInterfaces()

func TestOptionalInterfacesUseAs(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	var offenders []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.Contains(path, string(filepath.Separator)+"execution"+string(filepath.Separator)) {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			var typ ast.Expr
			switch x := n.(type) {
			case *ast.TypeAssertExpr:
				typ = x.Type
			case *ast.CaseClause:
				for _, e := range x.List {
					if isOptional(e) {
						offenders = append(offenders, path)
					}
				}
				return true
			default:
				return true
			}
			if isOptional(typ) {
				rel, _ := filepath.Rel(root, path)
				offenders = append(offenders, rel)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("type assertions of optional adapter interfaces (use execution.As): %v", offenders)
	}
}

func isOptional(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "execution" && optional[sel.Sel.Name]
}
