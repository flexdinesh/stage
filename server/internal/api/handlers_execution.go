package api

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"

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
	if c.Actions != nil {
		if a, err := s.Sync.Adapter(id); err == nil {
			checked, err := validateActionCapabilities(a, c)
			if err != nil {
				s.log.Warn("discarded invalid action declarations", "connection", id, "err", err)
				c.Actions = nil
			} else {
				c.Actions = visibleActions(checked.Actions, "", actionCaller(r))
			}
		} else {
			c.Actions = nil
		}
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
	if scoped, ok := execution.As[execution.WorkspaceFeatures](a); ok && projectID != 0 {
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
	caps, err := s.capabilities(ctx, id, project)
	if err != nil {
		return err
	}
	if err := caps.ValidateConfig(*c); err != nil {
		return db.Invalid("%s", err)
	}
	return nil
}

// requireImages refuses images on a connection whose capabilities do not
// offer the images operation, with the operation's reason (as MCP does;
// capabilities that can't be read refuse too). Connection 0 has no
// destination yet: its launch checks.
func (s *server) requireImages(ctx context.Context, id, project int64) error {
	if id == 0 {
		return nil
	}
	caps, err := s.capabilities(ctx, id, project)
	if err != nil {
		return err
	}
	op := caps.Operations["images"]
	if op.Available {
		return nil
	}
	reason := strings.TrimSpace(op.Reason)
	if reason == "" {
		reason = "This connection does not offer images."
	}
	return db.Invalid("Images are unavailable on this connection: %s Remove them or choose another connection.", reason)
}

// requirePatchImages checks the images a patch leaves on a task or schedule
// when it moves them to another connection or adds one.
func (s *server) requirePatchImages(ctx context.Context, connection, project int64, moved bool, current []db.Attachment, patch db.Optional[[]int64]) error {
	ids := make([]int64, 0, len(current))
	for _, a := range current {
		ids = append(ids, a.ID)
	}
	added := false
	if patch.Set {
		added = slices.ContainsFunc(patch.V, func(id int64) bool { return !slices.Contains(ids, id) })
		ids = patch.V
	}
	if len(ids) == 0 || !moved && !added {
		return nil
	}
	return s.requireImages(ctx, connection, project)
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
	writeJSON(w, 200, s.task(t))
	return nil
}
