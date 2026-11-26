package coordinator

import (
	"context"
	"slices"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Inventory reads the adapter's complete workspace list, then adds filesystem
// evidence for local workspaces on the connection's machine.
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
	if !slices.ContainsFunc(ws, func(w execution.WorkspaceInfo) bool { return w.Local }) {
		return ws, nil
	}
	ops, ok := s.Ops(s.store.ConnectionMachine(id))
	if !ok {
		return ws, nil
	}
	return ops.Inventory(ctx, ws)
}
