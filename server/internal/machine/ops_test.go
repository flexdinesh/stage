package machine_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/machine"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s %v", args, out, err)
	}
}

func TestLocalInventoryReadsWorkspaceEvidence(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "app")
	linked := filepath.Join(root, "linked")
	sub := filepath.Join(repo, "packages", "web")
	plain := filepath.Join(root, "notes")
	for _, d := range []string{sub, plain} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	git(t, repo, "init", "-q")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	git(t, repo, "remote", "add", "origin", "git@github.com:Org/App.git")
	git(t, repo, "worktree", "add", "-q", linked)
	other := filepath.Join(root, "other")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, other, "init", "-q")
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())

	ws := []execution.WorkspaceInfo{
		{WorkspaceRoot: alias, Local: true, ImportIf: execution.ImportCheckoutTop, Repo: "host/reported", RepoPath: "."},
		{WorkspaceRoot: sub, Local: true, ImportIf: execution.ImportCheckoutTop, Importable: true},
		{WorkspaceRoot: sub, Local: true, ImportIf: execution.ImportInCheckout},
		{WorkspaceRoot: linked, Local: true, ImportIf: execution.ImportCheckoutTop},
		{WorkspaceRoot: other, Local: true, ImportIf: execution.ImportCheckoutTop},
		{WorkspaceRoot: plain, Local: true, ImportIf: execution.ImportInCheckout},
		{WorkspaceRoot: repo, Importable: true, Repo: "host/reported", RepoPath: "."},
	}
	ops := machine.LocalOps{}
	out, err := ops.Inventory(t.Context(), ws)
	if err != nil || len(out) != len(ws) {
		t.Fatalf("inventory %+v: %v", out, err)
	}
	repoKey := filepath.Join(repo, ".git")
	const origin = "github.com/Org/App"
	for i, want := range []struct {
		allowed                         bool
		path, checkout, subpath, remote string
	}{
		{true, repo, repoKey, ".", origin},
		{false, sub, repoKey, "packages/web", origin},
		{true, sub, repoKey, "packages/web", origin},
		{true, linked, repoKey, ".", origin},
		{true, other, filepath.Join(other, ".git"), ".", ""},
		{false, plain, "", "", ""},
	} {
		got := out[i]
		e := got.Evidence
		if e == nil || e.CanonicalPath != want.path || e.CheckoutKey != want.checkout || e.Subpath != want.subpath || e.Remote != want.remote || e.Gone || got.Importable != want.allowed {
			t.Errorf("workspace %d: %+v, evidence %+v, want %+v", i, got, e, want)
		}
		if got.WorkspaceRoot != ws[i].WorkspaceRoot || got.Repo != ws[i].Repo || got.RepoPath != ws[i].RepoPath {
			t.Errorf("workspace %d changed harness facts: %+v", i, got)
		}
	}
	if out[6] != ws[6] || ws[0].Importable || ws[0].Evidence != nil || !ws[1].Importable {
		t.Fatalf("inventory changed its input or non-local row: %+v", out)
	}
	e := out[5].Evidence
	if (runtime.GOOS == "linux" || runtime.GOOS == "darwin") && (e == nil || e.InodeHint == "") {
		t.Fatalf("expected inode hint for a directory on %s", runtime.GOOS)
	}
	if e != nil && e.InodeHint != "" {
		moved := filepath.Join(root, "renamed")
		if err := os.Rename(plain, moved); err != nil {
			t.Fatal(err)
		}
		rows, err := ops.Inventory(t.Context(), []execution.WorkspaceInfo{{WorkspaceRoot: moved, Local: true}})
		if err != nil || len(rows) != 1 || rows[0].Evidence == nil || rows[0].Evidence.InodeHint != e.InodeHint || rows[0].Evidence.CanonicalPath != moved {
			t.Fatalf("renamed directory %+v, evidence before move %+v: %v", rows, e, err)
		}
	}
}

func TestLocalInventoryOnlyMarksMissingDirectoriesGone(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing")
	dangling := filepath.Join(root, "dangling")
	loop := filepath.Join(root, "loop")
	file := filepath.Join(root, "file")
	if err := os.Symlink(missing, dangling); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(loop, loop); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		path string
		gone bool
	}{
		{missing, true},
		{dangling, true},
		{loop, false},
		{file, false},
		{"relative", false},
		{"", false},
	} {
		ws := []execution.WorkspaceInfo{{WorkspaceRoot: tc.path, Local: true, ImportIf: execution.ImportInCheckout, Importable: true}}
		out, err := machine.LocalOps{}.Inventory(t.Context(), ws)
		if err != nil || len(out) != 1 {
			t.Fatalf("inventory %+v: %v", out, err)
		}
		evidence := out[0].Evidence
		if (tc.gone && (evidence == nil || *evidence != (execution.WorkspaceEvidence{Gone: true}))) ||
			(!tc.gone && evidence != nil) || out[0].Importable {
			t.Errorf("path %q want gone=%v, got evidence %+v and importable=%v", tc.path, tc.gone, evidence, out[0].Importable)
		}
	}
}

// TrustRule mirrors Claude Code's `claude --bg` workspace trust: the main
// checkout root's record, else the nearest record from the folder up to the
// checkout top, or up to the filesystem root outside a checkout.
func TestLocalInventoryAppliesTrustRule(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	trustedParent := filepath.Join(root, "workspace")
	plain := filepath.Join(trustedParent, "notes", "deep")
	repo := filepath.Join(trustedParent, "repo")
	repoSub := filepath.Join(repo, "pkg")
	trustedRepo := filepath.Join(root, "trusted-repo")
	trustedSub := filepath.Join(trustedRepo, "pkg")
	linked := filepath.Join(root, "linked")
	untrusted := filepath.Join(root, "elsewhere")
	explicitFalse := filepath.Join(trustedParent, "declined")
	for _, d := range []string{plain, repoSub, trustedSub, untrusted, explicitFalse} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	git(t, repo, "init", "-q")
	git(t, trustedRepo, "init", "-q")
	git(t, trustedRepo, "commit", "-q", "--allow-empty", "-m", "init")
	git(t, trustedRepo, "worktree", "add", "-q", linked)
	gone := filepath.Join(root, "gone")
	t.Setenv("PATH", t.TempDir())

	sloppy := filepath.Join(root, "sloppy")
	if err := os.Mkdir(sloppy, 0o755); err != nil {
		t.Fatal(err)
	}
	rule := &execution.TrustRule{Records: []string{trustedParent, trustedRepo, sloppy + "/", "relative"}, Reason: "Not trusted."}
	cases := []struct {
		path    string
		blocked bool
	}{
		{trustedParent, false},
		{plain, false},         // outside a checkout, an ancestor's record counts
		{explicitFalse, false}, // a folder without its own record inherits
		{repo, true},           // a checkout stops the walk at its top
		{repoSub, true},
		{trustedRepo, false},
		{trustedSub, false}, // walks up to the trusted checkout top
		{linked, false},     // a linked worktree uses its main checkout's record
		{untrusted, true},
		{gone, false},  // no evidence of a folder: nothing to judge
		{sloppy, true}, // records match verbatim, like Claude's lookup
	}
	ws := make([]execution.WorkspaceInfo, 0, len(cases)+2)
	for _, c := range cases {
		ws = append(ws, execution.WorkspaceInfo{WorkspaceRoot: c.path, Local: true, Trust: rule})
	}
	ws = append(ws,
		execution.WorkspaceInfo{WorkspaceRoot: untrusted, Local: true, Trust: rule, BlockedReason: "Adapter reason."},
		execution.WorkspaceInfo{WorkspaceRoot: untrusted, Local: true})
	out, err := machine.LocalOps{}.Inventory(t.Context(), ws)
	if err != nil || len(out) != len(ws) {
		t.Fatalf("inventory %+v: %v", out, err)
	}
	for i, c := range cases {
		want := ""
		if c.blocked {
			want = "Not trusted."
		}
		if out[i].BlockedReason != want {
			t.Errorf("%s: blocked reason %q, want %q", c.path, out[i].BlockedReason, want)
		}
	}
	if got := out[len(cases)].BlockedReason; got != "Adapter reason." {
		t.Errorf("an adapter's own reason was replaced: %q", got)
	}
	if got := out[len(cases)+1].BlockedReason; got != "" {
		t.Errorf("a row without a trust rule was blocked: %q", got)
	}
	if ws[len(cases)-2].BlockedReason != "" {
		t.Fatal("inventory changed its input")
	}
}
