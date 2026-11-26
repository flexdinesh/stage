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
		w.Evidence = inspectWorkspace(w.WorkspaceRoot)
		switch w.ImportIf {
		case execution.ImportCheckoutTop:
			w.Importable = w.Evidence != nil && w.Evidence.Subpath == "."
		case execution.ImportInCheckout:
			w.Importable = w.Evidence != nil && w.Evidence.CheckoutKey != ""
		}
	}
	return ws, nil
}

func inspectWorkspace(path string) *execution.WorkspaceEvidence {
	if !filepath.IsAbs(path) {
		return nil
	}
	dir, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &execution.WorkspaceEvidence{Gone: true}
		}
		return nil
	}
	info, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return &execution.WorkspaceEvidence{Gone: true}
	}
	if err != nil || !info.IsDir() {
		return nil
	}
	checkout := gitmeta.Inspect(dir)
	return &execution.WorkspaceEvidence{
		CanonicalPath: dir,
		CheckoutKey:   checkout.CommonDir,
		Subpath:       checkout.Subpath,
		Remote:        checkout.Remote,
		InodeHint:     inodeHint(info),
	}
}
