package db

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// derivedWrites maps each raw repository write that implies derived rows
// (journal events, recorded work, membership revisions, source sessions,
// thread projects) to the journal_events.go helper that writes them too.
var derivedWrites = map[string]map[string]string{
	"Tasks":       {"AddEvent": "addTaskEvent"},
	"Journal":     {"InsertNote": "insertNote"},
	"Acceptances": {"Insert": "insertAcceptance"},
	"Attempts":    {"RecordHandoff": "recordHandoff"},
	"Threads":     {"Mirror": "mirrorThread"},
	"Steering":    {"InsertEvent": "enqueueSteering", "Bind": "bindSteerer", "End": "endSteerer"},
	"Holds":       {"Finish": "finishHold"},
	"Trees": {
		"Insert": "insertTree", "Update": "updateTree", "SetStatus": "setTreeStatus", "SetSteeringHold": "setSteeringHold",
		"ArchiveDissolved": "archiveDissolved", "ArchiveIfEmpty": "setMembership",
		"SetMembership": "setMembership", "AddNeed": "addNeed", "KeepNeeds": "keepNeeds",
		"InheritNeeds": "inheritNeeds", "DropNeeds": "dropNeeds", "DropMemberNeeds": "detachMembers",
	},
}

const derivedFile = "journal_events.go"

// TestDerivedWritesBoundary: outside journal_events.go, no internal/db
// code calls a raw repository write that implies derived rows; it calls the
// helper, so the derived rows commit in the same Write (docs/storage.md
// "Journal events").
func TestDerivedWritesBoundary(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == derivedFile {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			checkDerivedCalls(t, fset, fn.Body)
		}
	}
	if checked == 0 {
		t.Fatal("no files checked")
	}
}

// repoOf is the repository accessor name X.Repo() that expr is, directly or
// through a variable bound to it in the function.
func repoOf(expr ast.Expr, bound map[string]string) string {
	switch x := expr.(type) {
	case *ast.CallExpr:
		if sel, ok := x.Fun.(*ast.SelectorExpr); ok && len(x.Args) == 0 {
			if _, guarded := derivedWrites[sel.Sel.Name]; guarded {
				return sel.Sel.Name
			}
		}
	case *ast.Ident:
		return bound[x.Name]
	}
	return ""
}

func checkDerivedCalls(t *testing.T, fset *token.FileSet, body *ast.BlockStmt) {
	t.Helper()
	bound := map[string]string{}
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for i, rhs := range n.Rhs {
				if i >= len(n.Lhs) {
					break
				}
				if id, ok := n.Lhs[i].(*ast.Ident); ok {
					if repo := repoOf(rhs, nil); repo != "" {
						bound[id.Name] = repo
					}
				}
			}
		case *ast.CallExpr:
			sel, ok := n.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			repo := repoOf(sel.X, bound)
			if helper, guarded := derivedWrites[repo][sel.Sel.Name]; guarded {
				t.Errorf("%s: %s().%s writes without its derived rows; call %s (%s)", fset.Position(n.Pos()), repo, sel.Sel.Name, helper, derivedFile)
			}
		}
		return true
	})
}
