package discovery

import (
	"context"
	"slices"
	"sync"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/machine"
)

// Reader adds machine-local evidence to an adapter's workspace inventory.
type Reader struct {
	store     *db.Store
	conns     *machine.Connections
	refreshes sync.Map
}

func NewReader(store *db.Store, conns *machine.Connections) *Reader {
	return &Reader{store: store, conns: conns}
}

func (s *Reader) refreshGate(id int64) chan struct{} {
	if v, ok := s.refreshes.Load(id); ok {
		return v.(chan struct{})
	}
	v, _ := s.refreshes.LoadOrStore(id, make(chan struct{}, 1))
	return v.(chan struct{})
}

func (s *Reader) RefreshingInventory(id int64) bool {
	return len(s.refreshGate(id)) != 0
}

// WithInventory serializes live reads through their update of the
// connection's inventory. A failed read preserves the last good rows.
func (s *Reader) WithInventory(ctx context.Context, id int64, apply func([]execution.WorkspaceInfo) error) error {
	gate := s.refreshGate(id)
	select {
	case gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-gate }()
	ws, err := s.Inventory(ctx, id)
	if err == nil {
		return apply(ws)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if saveErr := s.store.RecordWorkspaceRefreshFailure(ctx, id, err.Error()); saveErr != nil {
		return saveErr
	}
	return &db.HarnessError{Err: err}
}

func (s *Reader) RefreshInventory(ctx context.Context, id int64) ([]execution.WorkspaceInfo, int, error) {
	var ws []execution.WorkspaceInfo
	var count int
	err := s.WithInventory(ctx, id, func(rows []execution.WorkspaceInfo) error {
		ws = rows
		var err error
		count, err = s.store.RefreshWorkspaces(ctx, id, ws)
		return err
	})
	return ws, count, err
}

// Inventory reads the adapter's complete workspace list, then adds filesystem
// evidence for local workspaces on the connection's machine.
func (s *Reader) Inventory(ctx context.Context, id int64) ([]execution.WorkspaceInfo, error) {
	a, err := s.conns.Inventory(id)
	if err != nil {
		return nil, err
	}
	return s.inventory(ctx, id, a)
}

func (s *Reader) inventory(ctx context.Context, id int64, a execution.Inventory) ([]execution.WorkspaceInfo, error) {
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
