package sync

import (
	"context"
	"slices"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/machine"
)

// Inventory is a connection's workspace discovery: the adapter's harness
// facts, with the Git evidence its machine's ops read where the files live
// (machine.Ops.Inventory: through declared shared paths, or the path itself
// when it is local to the machine). Every caller that refreshes stored
// discovery or compares workspaces uses it, so a refresh never drops
// metadata another refresh found.
func (s *Service) Inventory(ctx context.Context, id int64) ([]execution.WorkspaceInfo, error) {
	a, err := s.Adapter(id)
	if err != nil {
		return nil, err
	}
	return s.inventory(ctx, id, a)
}

func (s *Service) inventory(ctx context.Context, id int64, a execution.Adapter) ([]execution.WorkspaceInfo, error) {
	ws, err := a.Workspaces(ctx)
	if err != nil {
		return nil, err
	}
	roots, err := s.SharedRoots(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(roots) == 0 && !slices.ContainsFunc(ws, func(w execution.WorkspaceInfo) bool { return w.Local }) {
		return ws, nil
	}
	ops, ok := s.Ops(s.store.ConnectionMachine(id))
	if !ok {
		return ws, nil
	}
	return ops.Inventory(ctx, ws, roots)
}

// SharedRoots are connection id's declared shared paths, for its machine's
// ops.
func (s *Service) SharedRoots(ctx context.Context, id int64) ([]machine.SharedRoot, error) {
	paths, err := s.store.MachineSharedPaths(ctx, s.store.ConnectionMachine(id))
	if err != nil {
		return nil, err
	}
	var roots []machine.SharedRoot
	for _, p := range paths {
		if p.ConnectionID == id {
			roots = append(roots, machine.SharedRoot{HarnessRoot: p.HarnessRoot, StageRoot: p.StageRoot})
		}
	}
	return roots, nil
}
