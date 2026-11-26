package api

import (
	"context"
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
	ws, err := s.Engine.Discovery.Inventory(ctx, connection)
	if err != nil {
		return nil, &db.HarnessError{Err: err}
	}
	if _, err := s.Store.RefreshWorkspaces(ctx, connection, ws); err != nil {
		return nil, err
	}
	return ws, nil
}

func (s *server) workspaces(w http.ResponseWriter, r *http.Request) error {
	id, err := s.connectionPath(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	if _, err := s.refreshWorkspaces(ctx, id); err != nil {
		return err
	}
	ws, err := s.Store.Workspaces(ctx, id)
	if err != nil {
		return err
	}
	if ws == nil {
		ws = []db.Workspace{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspaces": ws})
	return nil
}

func (s *server) resolveWorkspaces(w http.ResponseWriter, r *http.Request) error {
	id, err := s.connectionPath(r)
	if err != nil {
		return err
	}
	var in struct {
		Apply        *bool  `json:"apply"`
		PreviewToken string `json:"previewToken"`
	}
	if err := decodeStrict(r, &in); err != nil {
		return err
	}
	if in.Apply == nil {
		return db.Invalid("apply is required")
	}
	if *in.Apply && in.PreviewToken == "" {
		return db.Invalid("previewToken is required to apply workspace resolution")
	}
	ctx := r.Context()
	if *in.Apply {
		ws, err := s.Engine.Discovery.Inventory(ctx, id)
		if err != nil {
			return &db.HarnessError{Err: err}
		}
		result, err := s.Store.ConfirmWorkspaceResolution(ctx, id, ws, in.PreviewToken)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, result)
		return nil
	}
	if _, err := s.refreshWorkspaces(ctx, id); err != nil {
		return err
	}
	result, err := s.Store.ResolveWorkspaces(ctx, id, false)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, result)
	return nil
}

func (s *server) reviewWorkspaces(w http.ResponseWriter, r *http.Request) error {
	out, err := s.Store.ReviewWorkspaces(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *server) setWorkspaceDeclined(declined bool) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := s.connectionPath(r)
		if err != nil {
			return err
		}
		var in struct {
			ExternalID string  `json:"externalId"`
			Workspace  *string `json:"workspace"`
		}
		if err := decodeStrict(r, &in); err != nil {
			return err
		}
		if in.ExternalID == "" || in.Workspace == nil {
			return db.Invalid("externalId and workspace are required")
		}
		if err := s.Store.SetWorkspaceDeclined(r.Context(), id, in.ExternalID, *in.Workspace, declined); err != nil {
			return err
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
}
