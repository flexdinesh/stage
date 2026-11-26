package machine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/gitmeta"
)

// LocalOps runs Ops on this process's machine.
type LocalOps struct{}

var _ Ops = LocalOps{}

// Inventory reads each workspace's Git evidence without a git binary
// through the first root it sits under (RootsFor, gitmeta.Translate),
// never walking above that root: the origin remote and the subproject path
// (a workspace that already has a Repo, reported by its harness, keeps
// it), and Importable for a harness that imports Git checkouts
// (WorkspaceInfo.ImportIf). A workspace under no root keeps what the
// harness said; one with ImportIf is then not importable.
func (LocalOps) Inventory(_ context.Context, ws []execution.WorkspaceInfo, roots []SharedRoot) ([]execution.WorkspaceInfo, error) {
	ws = slices.Clone(ws)
	for i := range ws {
		w := &ws[i]
		top, dir := "", ""
		for _, r := range RootsFor(*w, roots) {
			if d, ok := gitmeta.Translate(r.HarnessRoot, r.StageRoot, w.WorkspaceRoot); ok {
				// The walk to the checkout top stays inside the root.
				if root, err := filepath.EvalSymlinks(r.StageRoot); err == nil {
					top, dir = gitmeta.Top(d, root), d
				}
				break
			}
		}
		switch w.ImportIf {
		case execution.ImportCheckoutTop:
			w.Importable = top != "" && top == dir
		case execution.ImportInCheckout:
			w.Importable = top != ""
		}
		if w.Repo == "" && top != "" {
			w.Repo, w.RepoPath = gitmeta.Checkout(dir, top)
		}
	}
	return ws, nil
}

// identityRoot maps a path local to the machine to itself.
var identityRoot = SharedRoot{HarnessRoot: string(filepath.Separator), StageRoot: string(filepath.Separator)}

// RootsFor are the roots a workspace's Git evidence is read through: its
// connection's declared shared paths, then, for a path local to the
// machine (WorkspaceInfo.Local), the path itself.
func RootsFor(w execution.WorkspaceInfo, declared []SharedRoot) []SharedRoot {
	if !w.Local {
		return declared
	}
	return append(slices.Clip(declared), identityRoot)
}

// Checkout asks git where the directory's checkout is, through the first
// root the directory sits under. A target whose symlinks resolve outside
// the root, or anything git cannot answer, proves nothing.
func (LocalOps) Checkout(ctx context.Context, roots []SharedRoot, directory string) (CheckoutEvidence, error) {
	for _, r := range roots {
		sub, ok := beneath(r.HarnessRoot, directory)
		if !ok {
			continue
		}
		root, err := filepath.EvalSymlinks(r.StageRoot)
		if err != nil {
			return CheckoutEvidence{}, nil
		}
		dir, err := filepath.EvalSymlinks(filepath.Join(root, sub))
		if err != nil {
			return CheckoutEvidence{}, nil
		}
		if _, ok = beneath(root, dir); !ok {
			return CheckoutEvidence{}, nil
		}
		git := func(args ...string) string {
			c := exec.CommandContext(ctx, "git", args...)
			c.Dir = dir
			c.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_OPTIONAL_LOCKS=0"}
			out, err := c.Output()
			if err != nil {
				return ""
			}
			return strings.TrimSpace(string(out))
		}
		top := git("rev-parse", "--show-toplevel")
		if top == "" {
			return CheckoutEvidence{}, nil
		}
		sub, ok = beneath(top, dir)
		if !ok {
			return CheckoutEvidence{}, nil
		}
		common, ok := fileKey(git("rev-parse", "--path-format=absolute", "--git-common-dir"))
		if !ok {
			return CheckoutEvidence{}, nil
		}
		return CheckoutEvidence{Common: common, Subproject: sub, RepoPath: filepath.ToSlash(sub), Remote: gitmeta.RemoteIdentity(git("config", "--local", "--no-includes", "--get", "remote.origin.url"))}, nil
	}
	return CheckoutEvidence{}, nil
}

func beneath(root, dir string) (string, bool) {
	rel, err := filepath.Rel(root, dir)
	return rel, err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
