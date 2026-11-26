package harness_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/codex"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/opencode"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// harnessBranch matches code that names a specific harness: kind literals,
// kind constants and T3-only connection lookups. Product code reads the
// descriptor, capabilities and operations instead (docs/execution.md).
var harnessBranch = regexp.MustCompile(`HarnessT3|HarnessOpenCode|IsT3\(|T3ID\(|"t3"|"opencode"|"claude"|"codex"|\bt3\.Kind\b|\bopencode\.Kind\b|\bclaudecode\.Kind\b|\bcodex\.Kind\b`)

// owners may name their harness: the adapters, their connection setup, this
// package, the server and worker compositions, the CLI's per-harness
// status panels, `stage setup`'s coding-agent targets (OpenCode and Claude
// Code as agents to register MCP or install a skill for, not a harness
// branch) and the storage contract suite (test-only code, like a _test.go
// file, that exercises the Store's harness APIs).
var owners = []string{"internal/t3/", "internal/opencode/", "internal/claudecode/", "internal/codex/", "internal/t3setup/", "internal/opencodesetup/", "internal/claudecodesetup/", "internal/codexsetup/", "internal/harness/", "internal/serve/", "internal/workersetup/", "internal/cli/slots_", "internal/cli/setup.go", "internal/cli/setup_agents.go", "internal/db/storetest/"}

// pending still branches on a harness and is being moved behind the
// boundary. Shrink it; never grow it.
var pending = map[string]bool{
	"internal/agenttools/connections.go": true,
	"internal/api/handlers_execution.go": true,
	"internal/api/handlers_misc.go":      true,
	"internal/api/handlers_projects.go":  true,
	"internal/api/handlers_refs.go":      true,
	"internal/api/handlers_schedules.go": true,
	"internal/api/handlers_tasks.go":     true,
	"internal/client/client.go":          true,
	"internal/client/t3.go":              true,
	"internal/db/adopt.go":               true,
	"internal/db/batch.go":               true,
	"internal/db/connections.go":         true,
	"internal/db/execution.go":           true,
	"internal/db/identities.go":          true,
	"internal/db/notifications.go":       true,
	"internal/db/projects.go":            true,
	"internal/db/tasks.go":               true,
	"internal/db/store/threads.go":       true,
	"internal/notify/notify.go":          true,
	"internal/sync/service.go":           true,
}

func TestProductCodeDoesNotBranchOnHarnessNames(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	var offenders []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
		for _, o := range owners {
			if strings.HasPrefix(rel, o) {
				return nil
			}
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if harnessBranch.Match(b) {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(offenders)
	found := map[string]bool{}
	for _, f := range offenders {
		found[f] = true
		if !pending[f] {
			t.Errorf("%s names a harness: read the harness descriptor, capabilities or operations instead, or move the behavior into the harness's adapter or connector", f)
		}
	}
	for f := range pending {
		if !found[f] {
			t.Errorf("%s no longer names a harness: remove it from pending", f)
		}
	}
}

// adapterKinds are the adapter packages (and their connection setup) with
// the harness each owns.
var adapterKinds = map[string]string{
	"internal/t3/": "t3", "internal/t3setup/": "t3",
	"internal/opencode/": "opencode", "internal/opencodesetup/": "opencode",
	"internal/claudecode/": "claude", "internal/claudecodesetup/": "claude",
	"internal/codex/": "codex", "internal/codexsetup/": "codex",
}

// namesKind matches code naming a harness kind: its kind literal, its kind
// constant, an import of its adapter or setup package, or a session origin
// it wraps (harness.Wraps, which only it declares).
func namesKind() map[string]*regexp.Regexp {
	out := map[string]*regexp.Regexp{}
	for pkg, d := range map[string]harness.Descriptor{"t3": t3.Descriptor, "opencode": opencode.Descriptor, "claudecode": claudecode.Descriptor, "codex": codex.Descriptor} {
		alts := []string{regexp.QuoteMeta(`"` + d.Kind + `"`), `\b` + pkg + `\.Kind\b`, `/internal/` + pkg + `(setup)?[/"]`}
		if d.Wraps != nil {
			for _, o := range d.Wraps.Origins {
				alts = append(alts, regexp.QuoteMeta(`"`+o+`"`))
			}
		}
		out[d.Kind] = regexp.MustCompile(strings.Join(alts, "|"))
	}
	return out
}

// modelsProviders may name another harness's kind as the harness's own
// vocabulary: T3's fake catalog lists a "claude" provider instance (T3's
// provider id, not Stage's harness kind).
var modelsProviders = map[string]bool{"internal/t3/fake.go": true}

// No adapter names another harness (ADR 0007 decision 13): a harness that
// runs other harnesses' sessions declares harness.Wraps, and the
// coordinator refuses and attributes from it.
func TestAdaptersDoNotNameOtherHarnesses(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	names := namesKind()
	for dir, own := range adapterKinds {
		err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(dir)), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
			if modelsProviders[rel] {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(b), "\n") {
				if c := strings.Index(line, "//"); c >= 0 {
					line = line[:c]
				}
				for kind, re := range names {
					if kind != own && re.MatchString(line) {
						t.Errorf("%s:%d names harness %s: declare harness.Wraps or a descriptor policy instead", rel, i+1, kind)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
