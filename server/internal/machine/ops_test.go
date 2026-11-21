package machine_test

import (
	"os"
	"os/exec"
	"path/filepath"
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

// A workspace under a declared shared path gets its repository metadata
// from the Stage-side checkout; one outside it, or with a Repo already,
// keeps what it had.
func TestLocalInventoryReadsRepoThroughSharedRoots(t *testing.T) {
	stageRoot := t.TempDir()
	repo := filepath.Join(stageRoot, "app")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "config"), []byte("[remote \"origin\"]\n\turl = https://github.com/o/app.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "packages", "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	in := []execution.WorkspaceInfo{{ID: "a", WorkspaceRoot: "/harness/app"}, {ID: "b", WorkspaceRoot: "/elsewhere/other"}, {ID: "c", WorkspaceRoot: "/harness/app", Repo: "github.com/kept/repo"}, {ID: "d", WorkspaceRoot: "/harness/app/packages/web"}}
	out, err := machine.LocalOps{}.Inventory(t.Context(), in, []machine.SharedRoot{{HarnessRoot: "/harness", StageRoot: stageRoot}})
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Repo != "github.com/o/app" || out[0].RepoPath != "." || out[1].Repo != "" || out[2].Repo != "github.com/kept/repo" ||
		out[3].Repo != "github.com/o/app" || out[3].RepoPath != "packages/web" {
		t.Fatalf("repos %+v", out)
	}
	if in[0].Repo != "" {
		t.Fatal("inventory changed its input")
	}
}

// Checkout proves the Git common directory and subproject only through a
// shared root; two roots onto one checkout prove the same family, a linked
// worktree shares its main checkout's common directory, and a symlink out
// of the root proves nothing.
func TestLocalCheckoutRequiresExplicitSharedRoots(t *testing.T) {
	ops := machine.LocalOps{}
	root := t.TempDir()
	git(t, root, "init", "-q")
	git(t, root, "commit", "-q", "--allow-empty", "-m", "init")
	sub := filepath.Join(root, "app")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	t3Roots := []machine.SharedRoot{{HarnessRoot: "/t3", StageRoot: root}}
	ocRoots := []machine.SharedRoot{{HarnessRoot: "/oc", StageRoot: root}}
	a, err := ops.Checkout(t.Context(), t3Roots, "/t3/app")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ops.Checkout(t.Context(), ocRoots, "/oc/app")
	if a.Common == "" || !a.SameCheckout(b) || a.Subproject != "app" || a.RepoPath != "app" {
		t.Fatal(a, b)
	}
	if top, _ := ops.Checkout(t.Context(), ocRoots, "/oc"); top.SameCheckout(a) {
		t.Fatal("different subproject matched")
	}

	// A linked worktree reaches the same common directory.
	wt := filepath.Join(t.TempDir(), "wt")
	git(t, root, "worktree", "add", "-q", wt)
	if err := os.Mkdir(filepath.Join(wt, "app"), 0o700); err != nil {
		t.Fatal(err)
	}
	linked, _ := ops.Checkout(t.Context(), []machine.SharedRoot{{HarnessRoot: "/linked", StageRoot: wt}}, "/linked/app")
	if !linked.SameCheckout(a) {
		t.Fatalf("linked worktree %+v vs %+v", linked, a)
	}

	// A symlinked root resolves to the same checkout.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	viaLink, _ := ops.Checkout(t.Context(), []machine.SharedRoot{{HarnessRoot: "/l", StageRoot: link}}, "/l/app")
	if !viaLink.SameCheckout(a) {
		t.Fatalf("symlinked root %+v", viaLink)
	}

	if err := os.Symlink(t.TempDir(), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{sub, "/t3/missing", "/oc/escape"} {
		if p, _ := ops.Checkout(t.Context(), ocRoots, dir); p.Common != "" {
			t.Errorf("unexpected proof for %s", dir)
		}
	}
	if p, _ := ops.Checkout(t.Context(), nil, "/t3/app"); p != (machine.CheckoutEvidence{}) {
		t.Fatal("proof without a shared root")
	}

	// Another repository never matches.
	other := t.TempDir()
	git(t, other, "init", "-q")
	if o, _ := ops.Checkout(t.Context(), []machine.SharedRoot{{HarnessRoot: "/o", StageRoot: other}}, "/o"); o.Common == "" || o.SameCheckout(a) {
		t.Fatalf("other repository %+v", o)
	}
}
