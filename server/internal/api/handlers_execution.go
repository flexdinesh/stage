package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

type taskDestination struct {
	connectionID int64
	projectID    int64
	bindingID    *int64
	workspaceID  string
}

func (s *server) projectFilter(r *http.Request, key string) (*int64, error) {
	id, err := queryInt64(r.URL.Query(), key)
	if err != nil || id == nil {
		return id, err
	}
	canonical, err := s.projectID(r.Context(), *id)
	if errors.Is(err, db.ErrNotFound) {
		return id, nil // an unknown project filter matches no rows
	}
	return &canonical, err
}

func (s *server) validateTaskDestination(ctx context.Context, in db.NewTask) error {
	d := taskDestination{in.ConnectionID, in.ProjectID, in.BindingID, in.WorkspaceID}
	if err := s.Store.ValidateBinding(ctx, d.projectID, d.connectionID, d.bindingID); err != nil {
		return err
	}
	if d.workspaceID != "" {
		if _, err := s.Store.ResolveDestination(ctx, d.connectionID, d.workspaceID); err != nil {
			return err
		}
	}
	if err := s.validateWorkspaceSelection(ctx, d, in.ExecutionConfig); err != nil {
		return err
	}
	if len(in.AttachmentIDs) > 0 {
		return s.requireImages(ctx, d)
	}
	return nil
}

func (s *server) validateDestinationPatch(ctx context.Context, original taskDestination,
	connection db.Optional[int64], project *db.Optional[int64], binding db.Optional[int64], workspace db.Optional[string],
	config *execution.Config, configPatch db.Optional[execution.Config], images []db.Attachment, imagePatch db.Optional[[]int64],
) error {
	next := original
	if connection.Set {
		next.connectionID = connection.V
		if err := s.plannedConnection(ctx, next.connectionID); err != nil {
			return err
		}
	}
	if project.Set {
		id, err := s.projectID(ctx, project.V)
		if err != nil {
			return err
		}
		next.projectID, project.V = id, id
	}
	moved := next.connectionID != original.connectionID
	if moved || binding.Set {
		next.bindingID = binding.Ptr()
		next.workspaceID = ""
	}
	if workspace.Set {
		if binding.Set && !binding.Null {
			return db.Invalid("Choose workspaceId or the legacy bindingId.")
		}
		next.bindingID, next.workspaceID = nil, workspace.V
		if !workspace.Null {
			if _, err := s.Store.ResolveDestination(ctx, next.connectionID, workspace.V); err != nil {
				return err
			}
		} else {
			next.projectID = 0
			if !configPatch.Set {
				// Clearing a saved pin leaves its options as planning data.
				config = nil
			}
		}
	} else if binding.Set {
		if err := s.Store.ValidateBinding(ctx, next.projectID, next.connectionID, next.bindingID); err != nil {
			return err
		}
	} else if !moved && next.workspaceID == "" {
		next.projectID = 0 // Saved planning work has no workspace scope.
	}
	changed := moved || !sameBindingChoice(next.bindingID, original.bindingID) || next.workspaceID != original.workspaceID
	if err := s.requirePatchImages(ctx, next, changed, images, imagePatch); err != nil {
		return err
	}
	if configPatch.Set {
		selection := configPatch.Ptr()
		if changed || !sameSelection(config, selection) {
			return s.validateWorkspaceSelection(ctx, next, selection)
		}
	} else if !moved && changed {
		return s.validateWorkspaceSelection(ctx, next, config)
	}
	return nil
}

func sameBindingChoice(a, b *int64) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func (s *server) executionCapabilities(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(r.PathValue("connectionId"), 10, 64)
	if err != nil {
		return db.Invalid("invalid connection")
	}
	ctx := r.Context()
	d := taskDestination{connectionID: id}
	q := r.URL.Query()
	if q.Has("workspaceId") {
		d.workspaceID = q.Get("workspaceId")
		if d.workspaceID == "" {
			return db.DestinationRequired()
		}
		if q.Has("bindingId") {
			return db.Invalid("Choose workspaceId or the legacy bindingId.")
		}
	}
	if raw := q.Get("projectId"); raw != "" {
		project, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || project < 1 {
			return db.Invalid("invalid Stage project")
		}
		d.projectID, err = s.projectID(ctx, project)
		if err != nil {
			return err
		}
	}
	if q.Has("bindingId") {
		binding, err := queryInt64(q, "bindingId")
		if err != nil || binding == nil || d.projectID == 0 {
			return db.ErrInvalidBinding
		}
		d.bindingID = binding
		if err := s.Store.ValidateBinding(ctx, d.projectID, d.connectionID, d.bindingID); err != nil {
			return err
		}
	}
	c, err := s.capabilities(ctx, d)
	if err != nil {
		return err
	}
	if c.Actions != nil {
		if a, err := s.Engine.Connections.Catalog(id); err == nil {
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
func (s *server) capabilities(ctx context.Context, d taskDestination) (execution.Capabilities, error) {
	var empty execution.Capabilities
	var pin *db.WorkspacePin
	if d.workspaceID != "" {
		w, err := s.Store.ResolveDestination(ctx, d.connectionID, d.workspaceID)
		if err != nil {
			return empty, err
		}
		pin = &db.WorkspacePin{ExternalID: w.ExternalID, Root: w.Workspace}
	}
	a, err := s.Engine.Connections.Catalog(d.connectionID)
	if err != nil {
		return empty, err
	}
	if scoped, ok := execution.As[execution.WorkspaceFeatures](a); ok && (pin != nil || d.projectID != 0) {
		if pin == nil {
			pin, err = s.Store.LegacyDestination(ctx, d.projectID, d.connectionID, d.bindingID)
			if err != nil {
				return empty, err
			}
		}
		if pin == nil {
			return empty, db.DestinationRequired()
		}
		c, err := scoped.FeaturesForWorkspace(ctx, pin.ExternalID, pin.Root)
		if err != nil {
			return empty, &db.HarnessError{Err: err}
		}
		return c, nil
	}
	c, err := a.Features(ctx)
	if err != nil {
		return empty, &db.HarnessError{Err: err}
	}
	return c, nil
}

func (s *server) validateSelection(ctx context.Context, id, project int64, c *execution.Config) error {
	return s.validateWorkspaceSelection(ctx, taskDestination{connectionID: id, projectID: project}, c)
}

func (s *server) validateWorkspaceSelection(ctx context.Context, d taskDestination, c *execution.Config) error {
	if c == nil || (c.Model == "" && c.Permission == "" && c.Mode == "" && len(c.Options) == 0) {
		return nil
	}
	caps, err := s.capabilities(ctx, d)
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
func (s *server) requireImages(ctx context.Context, d taskDestination) error {
	if d.connectionID == 0 {
		return nil
	}
	caps, err := s.capabilities(ctx, d)
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
// when it changes their destination or adds an image.
func (s *server) requirePatchImages(ctx context.Context, d taskDestination, moved bool, current []db.Attachment, patch db.Optional[[]int64]) error {
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
	return s.requireImages(ctx, d)
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
	p, err := s.Engine.Coordinator.PreviewExisting(r.Context(), id, in.Link)
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
	t, err := s.Engine.Coordinator.LinkExisting(r.Context(), id, in.Link, in.Attempt, in.Revision, in.Environment)
	if err != nil {
		return err
	}
	writeJSON(w, 200, s.task(t))
	return nil
}
