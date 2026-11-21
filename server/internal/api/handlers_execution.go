package api

import (
	"context"
	"net/http"
	"slices"
	"strconv"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func (s *server) executionCapabilities(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(r.PathValue("connectionId"), 10, 64)
	if err != nil {
		return db.Invalid("invalid connection")
	}
	var projectID int64
	if raw := r.URL.Query().Get("projectId"); raw != "" {
		projectID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || projectID < 1 {
			return db.Invalid("invalid Stage project")
		}
	}
	c, err := s.capabilities(r.Context(), id, projectID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, c)
	return nil
}
func (s *server) capabilities(ctx context.Context, id, projectID int64) (execution.Capabilities, error) {
	var empty execution.Capabilities
	a, err := s.Sync.Adapter(id)
	if err != nil {
		return empty, err
	}
	if scoped, ok := a.(execution.WorkspaceFeatures); ok && projectID != 0 {
		p, err := s.Store.GetProject(ctx, projectID)
		if err != nil {
			return empty, err
		}
		for _, b := range p.Bindings {
			if b.ConnectionID == id && b.Status == "available" {
				c, err := scoped.FeaturesForWorkspace(ctx, b.ExternalProjectID, b.Workspace)
				if err != nil {
					return empty, &db.HarnessError{Err: err}
				}
				return c, nil
			}
		}
		return empty, db.Conflict("Verify this project's workspace binding before choosing a model or agent.")
	}
	c, err := a.Features(ctx)
	if err != nil {
		return empty, &db.HarnessError{Err: err}
	}
	return c, nil
}
func (s *server) validateSelection(ctx context.Context, id, project int64, c *execution.Config) error {
	if c == nil || (c.Model == "" && c.Permission == "" && c.Mode == "" && len(c.Options) == 0) {
		return nil
	}
	if id == 0 {
		id = s.Store.LegacyT3ID()
	}
	caps, err := s.capabilities(ctx, id, project)
	if err != nil {
		return err
	}
	if err := caps.ValidateConfig(*c); err != nil {
		return db.Invalid("%s", err)
	}
	return nil
}
func sameSelection(a, b *execution.Config) bool {
	return a != nil && b != nil && a.Model == b.Model && a.Permission == b.Permission && a.Mode == b.Mode && slices.Equal(a.Options, b.Options)
}
func (s *server) previewLaunchLink(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in struct {
		Link string `json:"link"`
	}
	if err = decodeStrict(r, &in); err != nil {
		return err
	}
	p, err := s.Sync.PreviewExisting(r.Context(), id, in.Link)
	if err != nil {
		return err
	}
	writeJSON(w, 200, p)
	return nil
}
func (s *server) attachLaunchLink(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in struct {
		Link        string `json:"link"`
		Attempt     int    `json:"attempt"`
		Revision    int64  `json:"revision"`
		Environment string `json:"environment"`
	}
	if err = decodeStrict(r, &in); err != nil {
		return err
	}
	t, err := s.Sync.LinkExisting(r.Context(), id, in.Link, in.Attempt, in.Revision, in.Environment)
	if err != nil {
		return err
	}
	writeJSON(w, 200, s.task(s.envID(r.Context()), t))
	return nil
}
