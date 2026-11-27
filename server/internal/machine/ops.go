package machine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/gitmeta"
)

// LocalOps runs Ops on this process's machine.
type LocalOps struct{}

var _ Ops = LocalOps{}

// Inventory inspects the workspace directories the harness attests are local.
func (LocalOps) Inventory(_ context.Context, ws []execution.WorkspaceInfo) ([]execution.WorkspaceInfo, error) {
	ws = slices.Clone(ws)
	for i := range ws {
		w := &ws[i]
		if !w.Local {
			continue
		}
		var checkout gitmeta.Info
		w.Evidence, checkout = inspectWorkspace(w.WorkspaceRoot)
		if w.Trust != nil && w.BlockedReason == "" && w.Evidence != nil && !w.Evidence.Gone && !trusted(*w.Trust, w.Evidence.CanonicalPath, checkout) {
			w.BlockedReason = w.Trust.Reason
		}
		switch w.ImportIf {
		case execution.ImportCheckoutTop:
			w.Importable = w.Evidence != nil && w.Evidence.Subpath == "."
		case execution.ImportInCheckout:
			w.Importable = w.Evidence != nil && w.Evidence.CheckoutKey != ""
		}
	}
	return ws, nil
}

// trusted reports whether a trust record covers the canonical directory dir
// (execution.TrustRule): the checkout's main root, else dir or an ancestor up
// to the checkout top, or up to the filesystem root outside a checkout.
func trusted(rule execution.TrustRule, dir string, checkout gitmeta.Info) bool {
	// Records match verbatim, as the harness looks them up.
	records := make(map[string]bool, len(rule.Records))
	for _, r := range rule.Records {
		records[r] = true
	}
	if checkout.Top != "" && records[gitmeta.Main(checkout.Top)] {
		return true
	}
	for p := dir; ; p = filepath.Dir(p) {
		if records[p] {
			return true
		}
		if p == checkout.Top || p == filepath.Dir(p) {
			return false
		}
	}
}

func inspectWorkspace(path string) (*execution.WorkspaceEvidence, gitmeta.Info) {
	if !filepath.IsAbs(path) {
		return nil, gitmeta.Info{}
	}
	dir, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &execution.WorkspaceEvidence{Gone: true}, gitmeta.Info{}
		}
		return nil, gitmeta.Info{}
	}
	info, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return &execution.WorkspaceEvidence{Gone: true}, gitmeta.Info{}
	}
	if err != nil || !info.IsDir() {
		return nil, gitmeta.Info{}
	}
	checkout := gitmeta.Inspect(dir)
	return &execution.WorkspaceEvidence{
		CanonicalPath: dir,
		CheckoutKey:   checkout.CommonDir,
		Subpath:       checkout.Subpath,
		Remote:        checkout.Remote,
		InodeHint:     inodeHint(info),
	}, checkout
}
