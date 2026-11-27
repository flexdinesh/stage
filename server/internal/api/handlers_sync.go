package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

func (s *server) runSync(w http.ResponseWriter, r *http.Request) error {
	// An unconfigured connection is skipped once another one is configured,
	// so a fresh server with one harness set up syncs only that one.
	var skip []int64
	configured := false
	s.connectors().Each(func(_ int64, _ harness.Descriptor, c harness.Connector) {
		sum := c.Summary(r.Context())
		if sum.Configured {
			configured = true
		} else if sum.ConnectionID != 0 {
			skip = append(skip, sum.ConnectionID)
		}
	})
	if !configured {
		skip = nil
	}
	res, err := s.Engine.Discovery.FullSync(r.Context(), skip...)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, res)
	return nil
}

// refreshWorkspaces persists a complete live inventory without applying the resolver.
func (s *server) refreshWorkspaces(ctx context.Context, connection int64) ([]execution.WorkspaceInfo, error) {
	ws, _, err := s.Engine.Discovery.RefreshInventory(ctx, connection)
	return ws, err
}

type workspaceInventoryStatus struct {
	db.WorkspaceRefresh
	State string `json:"state"`
}

func (s *server) inventoryStatus(c db.HarnessConnection, refresh db.WorkspaceRefresh) workspaceInventoryStatus {
	state := "ready"
	_, adapterErr := s.Engine.Connections.Adapter(c.ID)
	switch {
	case !c.Active:
		state = "retired"
	case !s.Engine.Connections.Online(c.MachineID) || adapterErr != nil:
		state = "unavailable"
	case s.Engine.Discovery.RefreshingInventory(c.ID):
		state = "refreshing"
	case refresh.LastError != nil:
		state = "stale"
	case refresh.RefreshedAt == nil:
		state = "pending"
	}
	return workspaceInventoryStatus{WorkspaceRefresh: refresh, State: state}
}

func (s *server) workspaces(w http.ResponseWriter, r *http.Request) error {
	id, err := s.connectionPath(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	c, err := s.Store.GetHarnessConnection(ctx, id)
	if err != nil {
		return err
	}
	if c.ForgottenAt != nil {
		return db.ErrNotFound
	}
	if c.Active && r.URL.Query().Get("refresh") != "false" && !s.Engine.Discovery.RefreshingInventory(id) {
		var failed *db.HarnessError
		if _, err := s.refreshWorkspaces(ctx, id); err != nil && !errors.As(err, &failed) {
			return err
		}
	}
	ws, refresh, err := s.Store.WorkspaceCatalog(ctx, id)
	if err != nil {
		return err
	}
	status := s.inventoryStatus(c, refresh)
	if status.State != "ready" {
		for i := range ws {
			ws[i].Available = false
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspaces": ws, "inventory": status})
	return nil
}
