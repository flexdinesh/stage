package claudecode_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest/fakestate"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/machine"
)

// trustFake is a fake whose state file (in its temporary home, never the
// real ~/.claude.json) is state, with folder dirs created under its home.
func trustFake(t *testing.T, state string, dirs ...string) (*claudetest.Fake, string) {
	t.Helper()
	f := claudetest.New(t)
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(f.Home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(f.Home, ".claude.json")
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(state, "HOME", f.Home)), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil { // past the umask
		t.Fatal(err)
	}
	return f, path
}

func addTrust(a *claudecode.Adapter, t *testing.T, path string, confirmed bool) (execution.WorkspaceAdded, error) {
	input, _ := json.Marshal(map[string]string{"path": path})
	return a.AddWorkspace(t.Context(), execution.WorkspaceAddRequest{Source: claudecode.SourceTrust, Input: input, RequestID: "r1", Confirmed: confirmed})
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func noLock(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path + ".lock"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock left behind: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*.stage-*"))
	if len(matches) > 0 {
		t.Fatalf("temp files left behind: %v", matches)
	}
}

// A missing entry gets Claude's minimal record, written in place: every
// other byte, the file mode and the lock survive; Claude then lists the
// folder and its trust rule no longer blocks it.
func TestTrustAddsTheRecordAndPreservesTheFile(t *testing.T) {
	state := "{\n  \"numStartups\": 7,\n  \"projects\": {\n    \"HOME/src/repo\": {\n      \"allowedTools\": [],\n      \"hasTrustDialogAccepted\": true\n    }\n  },\n  \"theme\": \"dark\"\n}\n"
	f, path := trustFake(t, state, "notes")
	a := f.Adapter(1)
	dir := filepath.Join(f.Home, "notes")

	added, err := addTrust(a, t, dir+"/", true)
	if err != nil {
		t.Fatal(err)
	}
	if added.Existing || added.Workspace.ID != dir || added.Workspace.WorkspaceRoot != dir || !added.Workspace.Local || !strings.Contains(added.Message, "now trusts") {
		t.Fatalf("added %+v", added)
	}
	if len(added.Facts) != 1 || !strings.Contains(added.Facts[0].Value, path) {
		t.Fatalf("facts %+v", added.Facts)
	}
	want := strings.ReplaceAll("{\n  \"numStartups\": 7,\n  \"projects\": {\n    \"HOME/src/repo\": {\n      \"allowedTools\": [],\n      \"hasTrustDialogAccepted\": true\n    },\n    \"HOME/notes\": {\n      \"hasTrustDialogAccepted\": true\n    }\n  },\n  \"theme\": \"dark\"\n}\n", "HOME", f.Home)
	if got := readFile(t, path); got != want {
		t.Fatalf("state\n%s\nwant\n%s", got, want)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v, %v", st.Mode(), err)
	}
	noLock(t, path)

	ws, err := a.Workspaces(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ws, err = machine.LocalOps{}.Inventory(t.Context(), ws)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(ws, func(w execution.WorkspaceInfo) bool { return w.ID == dir })
	if i < 0 || ws[i].BlockedReason != "" {
		t.Fatalf("listed %+v", ws)
	}
}

// A folder Claude trusts with its own entry changes nothing. Inherited
// trust without an entry writes the folder's own record so Claude lists it.
func TestTrustExistingAndInherited(t *testing.T) {
	state := `{"projects":{"HOME/src/repo":{"hasTrustDialogAccepted":true},"HOME/src/repo/app":{"hasTrustDialogAccepted":false},"HOME/own":{"hasTrustDialogAccepted":true}}}`
	f, path := trustFake(t, state, "own", "src/repo/app", "src/repo/lib")
	a := f.Adapter(1)
	before := readFile(t, path)
	beforeStat, _ := os.Stat(path)

	for _, dir := range []string{"own", "src/repo", "src/repo/app"} {
		added, err := addTrust(a, t, filepath.Join(f.Home, dir), true)
		if err != nil || !added.Existing || !strings.Contains(added.Message, "already trusts") || len(added.Facts) != 0 {
			t.Fatalf("%s: %+v, %v", dir, added, err)
		}
	}
	if st, _ := os.Stat(path); readFile(t, path) != before || !st.ModTime().Equal(beforeStat.ModTime()) {
		t.Fatal("an existing trust changed the state file")
	}

	lib := filepath.Join(f.Home, "src/repo/lib")
	added, err := addTrust(a, t, lib, true)
	if err != nil || added.Existing {
		t.Fatalf("inherited without entry: %+v, %v", added, err)
	}
	if !strings.Contains(readFile(t, path), `"`+lib+`":{"hasTrustDialogAccepted":true}`) {
		t.Fatalf("no own record: %s", readFile(t, path))
	}
}

// CLAUDE_CODE_SANDBOXED trusts every folder, so a listed one is existing.
func TestTrustSandboxedIsExisting(t *testing.T) {
	f, path := trustFake(t, `{"projects":{"HOME/box":{"hasTrustDialogAccepted":false}}}`, "box")
	opts := f.Options(1)
	opts.CLI.Environ = append(opts.CLI.Environ, "CLAUDE_CODE_SANDBOXED=1")
	before := readFile(t, path)
	added, err := addTrust(claudecode.New(opts), t, filepath.Join(f.Home, "box"), true)
	if err != nil || !added.Existing || readFile(t, path) != before {
		t.Fatalf("%+v, %v", added, err)
	}
}

// An explicit CLAUDE_CONFIG_DIR holds the state file Stage patches.
func TestTrustUsesTheConfigDirState(t *testing.T) {
	f, homeState := trustFake(t, `{"projects":{}}`, "proj", "cfg")
	config := filepath.Join(f.Home, "cfg")
	f.Update(func(s *fakestate.State) { s.ConfigDir = config })
	state := filepath.Join(config, ".claude.json")
	if err := os.WriteFile(state, []byte(`{"projects":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := f.Options(1)
	opts.Identity = config
	opts.CLI.Environ = append(opts.CLI.Environ, "CLAUDE_CONFIG_DIR="+config)
	proj := filepath.Join(f.Home, "proj")
	if _, err := addTrust(claudecode.New(opts), t, proj, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFile(t, state), `"`+proj+`"`) || strings.Contains(readFile(t, homeState), `"`+proj+`"`) {
		t.Fatalf("config state %s, home state %s", readFile(t, state), readFile(t, homeState))
	}
}

// A symlinked state file (a dotfile manager) is written through, as Claude
// does, and stays a link.
func TestTrustWritesThroughASymlinkedState(t *testing.T) {
	f, path := trustFake(t, `{"projects":{}}`, "dots", "proj")
	real := filepath.Join(f.Home, "dots", "claude.json")
	if err := os.Rename(path, real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, path); err != nil {
		t.Fatal(err)
	}
	if _, err := addTrust(f.Adapter(1), t, filepath.Join(f.Home, "proj"), true); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Lstat(path); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("state is no longer a link: %v", err)
	}
	if !strings.Contains(readFile(t, real), "proj") {
		t.Fatal("target not written")
	}
}

// Stage writes nothing unconfirmed, outside an existing folder, in home,
// above home, in Claude's worktrees or without Claude's state file.
func TestTrustRefusals(t *testing.T) {
	f, path := trustFake(t, `{"projects":{}}`, "proj/.claude/worktrees/w")
	a := f.Adapter(1)
	file := filepath.Join(f.Home, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	before := readFile(t, path)
	for name, tc := range map[string]struct {
		path      string
		confirmed bool
		want      string
	}{
		"unconfirmed": {filepath.Join(f.Home, "proj"), false, "Confirm before running Trust in Claude Code"},
		"relative":    {"proj", true, "absolute"},
		"missing":     {filepath.Join(f.Home, "gone"), true, "does not exist"},
		"file":        {file, true, "is not a folder"},
		"home":        {f.Home, true, "home folder one session at a time"},
		"above home":  {filepath.Dir(f.Home), true, "including your home folder"},
		"root":        {"/", true, "including your home folder"},
		"worktree":    {filepath.Join(f.Home, "proj/.claude/worktrees/w"), true, "Claude Code worktree"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := addTrust(a, t, tc.path, tc.confirmed)
			var invalid *execution.Invalid
			if !errors.As(err, &invalid) || !strings.Contains(invalid.Reason, tc.want) {
				t.Fatalf("got %v, want Invalid %q", err, tc.want)
			}
		})
	}
	if readFile(t, path) != before {
		t.Fatal("a refused trust changed the state file")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	_, err := addTrust(a, t, filepath.Join(f.Home, "proj"), true)
	var invalid *execution.Invalid
	if !errors.As(err, &invalid) || !strings.Contains(invalid.Reason, "Run claude once") {
		t.Fatalf("missing state: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("trust created a state file")
	}
}

// Claude's lock: a live holder past the wait answers Busy and nothing is
// written; a dead holder's stale lock is taken over; a released lock lets
// the write through.
func TestTrustHonorsClaudesLock(t *testing.T) {
	f, path := trustFake(t, `{"projects":{}}`, "proj")
	opts := f.Options(1)
	opts.StateLockWait = 300 * time.Millisecond
	a := claudecode.New(opts)
	dir := filepath.Join(f.Home, "proj")
	lock := path + ".lock"
	before := readFile(t, path)

	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := addTrust(a, t, dir, true)
	var busy *execution.Busy
	if !errors.As(err, &busy) || readFile(t, path) != before {
		t.Fatalf("held lock: %v", err)
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatal("Stage removed a live holder's lock")
	}

	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := addTrust(a, t, dir, true); err != nil {
		t.Fatalf("stale lock: %v", err)
	}
	noLock(t, path)

	other := filepath.Join(f.Home, "other")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = os.Remove(lock)
	}()
	if _, err := addTrust(a, t, other, true); err != nil {
		t.Fatalf("released lock: %v", err)
	}
	if !strings.Contains(readFile(t, path), other) {
		t.Fatal("not written after release")
	}
}

// A writer that changes the file after Stage's read wins: Stage re-reads
// and patches the new bytes, and gives up with Busy after three tries.
func TestTrustRetriesAConcurrentWriter(t *testing.T) {
	f, path := trustFake(t, `{"projects":{}}`, "proj")
	a := f.Adapter(1)
	dir := filepath.Join(f.Home, "proj")
	writes := 0
	a.BeforeTrustRename(func() {
		if writes++; writes == 1 {
			if err := os.WriteFile(path, []byte(`{"projects":{},"tipsHistory":{"a":1}}`), 0o640); err != nil {
				t.Error(err)
			}
		}
	})
	if _, err := addTrust(a, t, dir, true); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	if writes != 2 || !strings.Contains(got, `"tipsHistory":{"a":1}`) || !strings.Contains(got, dir) {
		t.Fatalf("after %d tries: %s", writes, got)
	}
	noLock(t, path)

	other := filepath.Join(f.Home, "other")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	writes = 0
	a.BeforeTrustRename(func() {
		writes++
		if err := os.WriteFile(path, []byte(`{"projects":{},"n":`+strings.Repeat("1", writes)+`}`), 0o640); err != nil {
			t.Error(err)
		}
	})
	_, err := addTrust(a, t, other, true)
	var busy *execution.Busy
	if !errors.As(err, &busy) || writes != 3 || strings.Contains(readFile(t, path), other) {
		t.Fatalf("after %d tries: %v, %s", writes, err, readFile(t, path))
	}
	noLock(t, path)
}

// The project action trusts its listed folder through the same path, and
// is available only where Claude does not trust the folder.
func TestTrustProjectAction(t *testing.T) {
	f, path := trustFake(t, `{"projects":{"HOME/src/repo":{"hasTrustDialogAccepted":true},"HOME/proj":{}}}`, "proj")
	a := f.Adapter(1)
	dir := filepath.Join(f.Home, "proj")
	availability := func(d string) execution.Availability {
		t.Helper()
		caps, err := a.FeaturesForWorkspace(t.Context(), d, d)
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(caps.Actions, func(x execution.Action) bool { return x.ID == claudecode.ActionTrust })
		if i < 0 || caps.Actions[i].Target != execution.TargetProject || !caps.Actions[i].Allows(execution.CallerAgent) || caps.Actions[i].Confirm == "" {
			t.Fatalf("actions %+v", caps.Actions)
		}
		if err := execution.ValidateActions(caps.Actions, ""); err != nil {
			t.Fatal(err)
		}
		return caps.Actions[i].Availability
	}
	if got := availability(dir); !got.Available {
		t.Fatalf("untrusted folder: %+v", got)
	}
	if got := availability(f.Workspace); got.Available || !strings.Contains(got.Reason, "already trusts") {
		t.Fatalf("trusted folder: %+v", got)
	}
	if got := availability(f.Home); got.Available || !strings.Contains(got.Reason, "home folder") {
		t.Fatalf("home: %+v", got)
	}

	req := execution.ActionRequest{ID: claudecode.ActionTrust, Target: execution.TargetProject, Project: &execution.ActionWorkspace{ExternalProjectID: dir, Directory: dir}, Input: json.RawMessage(`{}`)}
	var invalid *execution.Invalid
	if _, err := a.Act(t.Context(), req); !errors.As(err, &invalid) {
		t.Fatalf("unconfirmed action: %v", err)
	}
	req.Confirmed = true
	res, err := a.Act(t.Context(), req)
	if err != nil || !strings.Contains(res.Message, "now trusts") {
		t.Fatalf("%+v, %v", res, err)
	}
	if !strings.Contains(readFile(t, path), `"`+dir+`":{"hasTrustDialogAccepted":true}`) {
		t.Fatal(readFile(t, path))
	}
	if got := availability(dir); got.Available {
		t.Fatalf("after trust: %+v", got)
	}
}

// The declaration: the source and action are available with machine
// evidence, and unavailable with a reason without it.
func TestTrustDeclaration(t *testing.T) {
	f := claudetest.New(t)
	caps, err := f.Adapter(1).Features(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !caps.Operations[execution.OpWorkspaceAdd].Available || len(caps.WorkspaceSources) != 1 || caps.WorkspaceSources[0].Confirm == "" {
		t.Fatalf("caps %+v", caps)
	}
	if err := caps.ValidateWorkspaceSources(); err != nil {
		t.Fatal(err)
	}
	opts := f.Options(1)
	opts.Inventory = nil
	caps, err = claudecode.New(opts).Features(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if caps.Operations[execution.OpWorkspaceAdd].Available || caps.WorkspaceSources[0].Availability.Available || caps.Actions[0].Availability.Available {
		t.Fatalf("without evidence %+v", caps)
	}
	if err := caps.ValidateWorkspaceSources(); err != nil {
		t.Fatal(err)
	}
}
