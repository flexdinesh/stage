package harness_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/harnesses"
)

// Mark camel-case and numeric boundaries for forms such as OpenCode and
// t3sync. Preserve punctuation and spaces in declared spellings.
var (
	acronymBoundary = regexp.MustCompile(`(\p{Lu})(\p{Lu}\p{Ll})`)
	camelBoundary   = regexp.MustCompile(`(\p{Ll})(\p{Lu})`)
	digitBoundary   = regexp.MustCompile(`(\p{N})(\p{L})`)
)

func markNameBoundaries(s string) string {
	for _, boundary := range []*regexp.Regexp{acronymBoundary, camelBoundary, digitBoundary} {
		s = boundary.ReplaceAllString(s, "${1}\x00${2}")
	}
	return s
}

type moduleNames struct {
	kind   string
	owners []string
	names  *regexp.Regexp
}

func namesOfModules(modules []harness.Module) []moduleNames {
	names := make([]moduleNames, 0, len(modules))
	for _, m := range modules {
		d := m.Descriptor
		n := moduleNames{kind: d.Kind}
		terms := []string{d.Kind, d.Name}
		if d.Wraps != nil {
			terms = append(terms, d.Wraps.Origins...)
		}
		for _, owner := range m.Owners {
			dir := path.Clean(strings.ReplaceAll(owner, "\\", "/"))
			n.owners = append(n.owners, dir)
			terms = append(terms, path.Base(dir))
		}
		var alternatives []string
		for _, term := range terms {
			if term != "" {
				alternatives = append(alternatives, regexp.QuoteMeta(markNameBoundaries(term)))
			}
		}
		n.names = regexp.MustCompile(`(?i)(^|[^\pL\pN])(?:` + strings.Join(alternatives, "|") + `)([^\pL\pN]|$)`)
		names = append(names, n)
	}
	return names
}

func ownerOf(rel string, modules []moduleNames) string {
	for _, m := range modules {
		if rel == "internal/cli/slots_"+m.kind+".go" {
			return m.kind
		}
		for _, dir := range m.owners {
			if rel == dir || strings.HasPrefix(rel, dir+"/") {
				return m.kind
			}
		}
	}
	return ""
}

// These packages build test fixtures using ordinary Go files.
var harnessTestHelpers = []string{
	"internal/claudecode/claudetest",
	"internal/db/dbtest",
	"internal/db/postgres/pgtest",
	"internal/db/sqlite/sqlitetest",
	"internal/db/storetest",
	"internal/remote/remotetest",
}

func scansHarnessNames(rel string) bool {
	// Immutable historical SQL migrations are outside this Go source scan.
	// SQL inside Go runtime code is checked like any other string literal.
	if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
		return false
	}
	for _, dir := range harnessTestHelpers {
		if strings.HasPrefix(rel, dir+"/") {
			return false
		}
	}
	switch rel {
	case "internal/harnesses/modules.go":
		// The fixed registration list imports the individual setup modules.
		return false
	case "internal/cli/setup.go", "internal/cli/setup_agents.go":
		// Stage setup installs skills and MCP configuration for coding agents.
		return false
	default:
		return true
	}
}

type harnessNameFinding struct {
	position token.Position
	kind     string
}

// Session environment keys are part of the child process protocol. The
// fake catalog also uses the provider's real model vocabulary.
var adapterProtocolLiterals = map[string][]string{
	"internal/claudecode/cli.go": {"T3CODE_", "T3_"},
	"internal/codex/daemon.go":   {"OPENCODE_SESSION_ID", "CLAUDECODE", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_ENTRYPOINT", "T3CODE_"},
	"internal/t3/fake.go":        {"claude", "Claude", "claude-haiku-5-5"},
}

func findHarnessNames(rel, src string, modules []moduleNames, own string) ([]harnessNameFinding, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var findings []harnessNameFinding
	ast.Inspect(file, func(node ast.Node) bool {
		var value string
		switch n := node.(type) {
		case *ast.Ident:
			value = n.Name
		case *ast.BasicLit:
			if n.Kind == token.STRING {
				value, _ = strconv.Unquote(n.Value)
				if own != "" && slices.Contains(adapterProtocolLiterals[rel], value) {
					return true
				}
			}
		}
		if value == "" {
			return true
		}
		words := markNameBoundaries(value)
		for _, m := range modules {
			if m.kind == own || !m.names.MatchString(words) {
				continue
			}
			findings = append(findings, harnessNameFinding{fset.Position(node.Pos()), m.kind})
		}
		return true
	})
	return findings, nil
}

func checkHarnessNames(t *testing.T, adapters bool, advice string) {
	t.Helper()
	modules := namesOfModules(harnesses.Modules())
	root := filepath.Clean("../..")
	err := filepath.WalkDir(root, func(filename string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !scansHarnessNames(rel) {
			return nil
		}
		own := ownerOf(rel, modules)
		if adapters != (own != "") {
			return nil
		}
		source, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		findings, err := findHarnessNames(rel, string(source), modules, own)
		if err != nil {
			return err
		}
		for _, f := range findings {
			t.Errorf("%s:%d:%d names harness %q. %s", rel, f.position.Line, f.position.Column, f.kind, advice)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestProductCodeDoesNotBranchOnHarnessNames(t *testing.T) {
	checkHarnessNames(t, false, "Read the descriptor, a capability or an operation, or move the behavior into the owning module.")
}

func TestAdaptersDoNotNameOtherHarnesses(t *testing.T) {
	checkHarnessNames(t, true, "Declare harness.Wraps or a descriptor policy instead.")
}

func TestHarnessNameDetector(t *testing.T) {
	modules := namesOfModules([]harness.Module{
		{
			Descriptor: harness.Descriptor{
				Kind: "driftwave", Name: "DriftWave",
				Wraps: &harness.Wraps{Origins: []string{"legacy-origin"}},
			},
			Owners: []string{`./internal/driftwave/`, `internal\driftwavesetup`},
		},
		{Descriptor: harness.Descriptor{Kind: "u7", Name: "Deep Water"}},
	})
	const source = `package product

// DriftWave and "legacy-origin" are fine in comments.
/* db.SaveDriftWave() */
import "example.com/stage/internal/driftwavesetup"

var driftwave struct{ DriftWaveSettings string }
var _ = ` + "`SELECT * FROM connections WHERE kind = 'driftwave'`" + `
var _ = "setup.driftwave.save"
var _ = "driftwave_settings"
var _ = "\x64riftwave"
var _ = "legacy-origin"
var _ = "Deep Water"
var _ = "drift wave"
var _ = "driftwaveform"
var _ = "deep waterfall"
var _ = "codec"
var _ = u7sync
var _ = aboutu7sync
`
	findings, err := findHarnessNames("internal/db/example.go", source, modules, "")
	if err != nil {
		t.Fatal(err)
	}
	var lines []int
	for _, f := range findings {
		lines = append(lines, f.position.Line)
	}
	if want := []int{5, 7, 7, 8, 9, 10, 11, 12, 13, 18}; !slices.Equal(lines, want) {
		t.Fatalf("got findings %v, want lines %v", findings, want)
	}
	for _, owner := range []string{"internal/driftwave/adapter.go", "internal/driftwavesetup/setup.go", "internal/cli/slots_driftwave.go"} {
		own := ownerOf(owner, modules)
		findings, err := findHarnessNames(owner, source, modules, own)
		if err != nil {
			t.Fatal(err)
		}
		if own != "driftwave" || len(findings) != 2 || findings[0].kind != "u7" || findings[1].kind != "u7" {
			t.Errorf("%s has owner %q and findings %v; want only the cross-kind findings", owner, own, findings)
		}
	}
	if own := ownerOf("internal/driftwavesetupfake/setup.go", modules); own != "" {
		t.Errorf("owner path matched a sibling package: %q", own)
	}
}
