package harness_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// harnessBranch matches code that names a specific harness: kind literals,
// kind constants and T3-only connection lookups. Product code reads the
// descriptor, capabilities and operations instead (docs/execution.md).
var harnessBranch = regexp.MustCompile(`HarnessT3|HarnessOpenCode|IsT3\(|T3ID\(|"t3"|"opencode"|\bt3\.Kind\b|\bopencode\.Kind\b`)

// owners may name their harness: the adapters, their connection setup, this
// package, the server composition and the CLI's per-harness status panels.
var owners = []string{"internal/t3/", "internal/opencode/", "internal/t3setup/", "internal/opencodesetup/", "internal/harness/", "internal/serve/", "internal/cli/slots_"}

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
	"internal/db/schedules.go":           true,
	"internal/db/tasks.go":               true,
	"internal/db/types.go":               true,
	"internal/notify/notify.go":          true,
	"internal/sync/service.go":           true,
	"internal/sync/session.go":           true,
	"internal/sync/watch.go":             true,
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
