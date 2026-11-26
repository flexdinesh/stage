package discovery

import (
	"context"
	"slices"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/machine"
)

// Reader adds machine-local evidence to an adapter's workspace inventory.
type Reader struct {
	store *db.Store
	conns *machine.Connections
}

func NewReader(store *db.Store, conns *machine.Connections) Reader {
	return Reader{store: store, conns: conns}
}

// Inventory reads the adapter's complete workspace list, then adds filesystem
// evidence for local workspaces on the connection's machine.
func (s Reader) Inventory(ctx context.Context, id int64) ([]execution.WorkspaceInfo, error) {
	a, err := s.conns.Inventory(id)
	if err != nil {
		return nil, err
	}
	return s.inventory(ctx, id, a)
}

func (s Reader) inventory(ctx context.Context, id int64, a execution.Inventory) ([]execution.WorkspaceInfo, error) {
	ws, err := a.Workspaces(ctx)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(ws, func(w execution.WorkspaceInfo) bool { return w.Local }) {
		return ws, nil
	}
	ops, ok := s.conns.Ops(s.store.ConnectionMachine(id))
	if !ok {
		return ws, nil
	}
	return ops.Inventory(ctx, ws)
}
