package claudecode_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// worktreeIntent is a Claude worktree launch from base ("" = Claude's
// default).
func worktreeIntent(f *claudetest.Fake, req, base string) execution.Intent {
	in := intent(f, req)
	in.Workspace = execution.Workspace{Kind: "worktree", Base: base}
	return in
}

// A predecessor base: the dependent launches `claude --bg -w` from the
// checkout that has the predecessor's branch, with worktree.baseRef head,
// so Claude's new worktree starts at the predecessor's commit. Stage makes
// no worktree itself.
func TestPredecessorBaseLaunch(t *testing.T) {
	f := claudetest.New(t)
	f.GitInit()
	a := f.Adapter(conn)
	pred, err := a.Launch(t.Context(), worktreeIntent(f, "req-a", ""), execution.Checkpoint{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	predDir := claudecode.WorktreeDir(f.Workspace, claudecode.WorktreeName("req-a"))
	if pred.Directory != predDir || pred.Branch != "worktree-stage-req-a" || !pred.Base.Available {
		t.Fatalf("predecessor %+v", pred)
	}
	if err := os.WriteFile(filepath.Join(predDir, "pred.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.Git(predDir, "add", "pred.txt")
	f.Git(predDir, "commit", "-q", "-m", "pred")
	tip := f.Git(predDir, "rev-parse", "HEAD")

	dep, err := a.Launch(t.Context(), worktreeIntent(f, "req-b", pred.Branch), execution.Checkpoint{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ls := f.Launches()
	last := ls[len(ls)-1]
	if last.Dir != predDir || !slices.Contains(last.Args, "--settings") || !slices.Contains(last.Args, `{"worktree":{"baseRef":"head"}}`) {
		t.Fatalf("dependent launch dir %s args %q", last.Dir, last.Args)
	}
	depDir := claudecode.WorktreeDir(f.Workspace, claudecode.WorktreeName("req-b"))
	if dep.Directory != depDir || dep.Project != f.Workspace {
		t.Fatalf("dependent %+v", dep)
	}
	if got := f.Git(depDir, "rev-parse", "HEAD"); got != tip {
		t.Fatalf("dependent starts at %s, want the predecessor's %s", got, tip)
	}
	if args := ls[0].Args; slices.Contains(args, "--settings") {
		t.Fatalf("a launch without a base keeps Claude's default: %q", args)
	}
}

// With hooks, a based launch passes one --settings: the hooks file's head
// variant, the same hooks plus worktree.baseRef head (Claude keeps one
// --settings value), and Claude still branches from the predecessor.
func TestPredecessorBaseWithHooks(t *testing.T) {
	f := claudetest.New(t)
	f.GitInit()
	h := f.Hooks()
	a := f.HookedAdapter(conn, h)
	pred, err := a.Launch(t.Context(), worktreeIntent(f, "req-a", ""), execution.Checkpoint{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.Git(pred.Directory, "commit", "-q", "--allow-empty", "-m", "pred")
	tip := f.Git(pred.Directory, "rev-parse", "HEAD")
	dep, err := a.Launch(t.Context(), worktreeIntent(f, "req-b", pred.Branch), execution.Checkpoint{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ls := f.Launches()
	for i, want := range []string{h.SettingsPath(), h.HeadSettingsPath()} {
		args := ls[i].Args
		if n := strings.Count(strings.Join(args, "\x00")+"\x00", "\x00--settings\x00"); n != 1 || args[slices.Index(args, "--settings")+1] != want {
			t.Fatalf("launch %d args %q, want one --settings %s", i, args, want)
		}
	}
	if got := f.Git(dep.Directory, "rev-parse", "HEAD"); got != tip {
		t.Fatalf("dependent starts at %s, want the predecessor's %s", got, tip)
	}
	var plain, head map[string]any
	for p, v := range map[string]*map[string]any{h.SettingsPath(): &plain, h.HeadSettingsPath(): &head} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %v", p, fi.Mode())
		}
		if err := json.Unmarshal(b, v); err != nil {
			t.Fatal(err)
		}
	}
	wt, _ := head["worktree"].(map[string]any)
	if wt["baseRef"] != "head" || !reflect.DeepEqual(head["hooks"], plain["hooks"]) || plain["worktree"] != nil {
		t.Fatalf("head %v plain %v", head, plain)
	}
}

// A base no checkout has checked out, a base on the project folder and a
// branch name are rejected before claude runs.
func TestPredecessorBaseRejections(t *testing.T) {
	f := claudetest.New(t)
	f.GitInit()
	a := f.Adapter(conn)
	var rejected *execution.Rejected
	if _, err := a.Launch(t.Context(), worktreeIntent(f, "req-n", "gone"), execution.Checkpoint{}, nil); !errors.As(err, &rejected) || !strings.Contains(err.Error(), "branch gone") {
		t.Fatalf("missing checkout: %v", err)
	}
	root := intent(f, "req-r")
	root.Workspace.Base = "main"
	named := worktreeIntent(f, "req-x", "main")
	named.Workspace.Branch = "feat"
	for _, in := range []execution.Intent{root, named} {
		if _, err := a.Launch(t.Context(), in, execution.Checkpoint{}, nil); !errors.As(err, &rejected) {
			t.Fatalf("%s: %v", in.RequestID, err)
		}
	}
	if n := len(f.Launches()); n != 0 {
		t.Fatalf("a rejected launch reached claude: %d", n)
	}
}

// Base availability: a session in the project folder, a worktree on
// another branch or detached, and a removed worktree cannot supply a base.
func TestBaseAvailability(t *testing.T) {
	f := claudetest.New(t)
	f.GitInit()
	a := f.Adapter(conn)
	root, err := a.Launch(t.Context(), intent(f, "req-root"), execution.Checkpoint{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if root.Base.Available || !strings.Contains(root.Base.Reason, "project folder") {
		t.Fatalf("root %+v", root.Base)
	}
	wt, err := a.Launch(t.Context(), worktreeIntent(f, "req-wt", ""), execution.Checkpoint{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	observe := func() execution.Availability {
		t.Helper()
		o, err := a.Observe(t.Context(), wt.Session)
		if err != nil {
			t.Fatal(err)
		}
		return o.Base
	}
	if b := observe(); !b.Available {
		t.Fatalf("worktree %+v", b)
	}
	f.Git(wt.Directory, "switch", "-q", "-c", "other")
	if b := observe(); b.Available || !strings.Contains(b.Reason, "now on other") {
		t.Fatalf("switched %+v", b)
	}
	f.Git(wt.Directory, "switch", "-q", "--detach")
	if b := observe(); b.Available || !strings.Contains(b.Reason, "detached") {
		t.Fatalf("detached %+v", b)
	}
	f.Git(f.Workspace, "worktree", "remove", "--force", wt.Directory)
	if b := observe(); b.Available || !strings.Contains(b.Reason, "removed") {
		t.Fatalf("removed %+v", b)
	}
}
