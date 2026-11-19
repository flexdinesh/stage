package sync

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/gitmeta"
)

// Inventory is a connection's workspace discovery: the adapter's list, with
// repository metadata read through declared shared paths where the adapter
// had none. Every caller that refreshes stored discovery uses it, so a
// refresh never drops metadata another refresh found.
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
	paths, err := s.store.SharedPaths(ctx)
	if err != nil {
		return nil, err
	}
	for i := range ws {
		if ws[i].Repo != "" {
			continue
		}
		for _, p := range paths {
			if p.ConnectionID != id {
				continue
			}
			if dir, ok := gitmeta.Translate(p.HarnessRoot, p.StageRoot, ws[i].WorkspaceRoot); ok {
				ws[i].Repo = gitmeta.OriginRepo(dir)
				break
			}
		}
	}
	return ws, nil
}
