package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/principal"
)

// ---- explicit session lookup and adoption ----

// adoptSession: POST /api/connections/{connectionId}/sessions/{id}/adopt {title?, initialNote?,
// treeId?} makes the session a task ("Track as task"): 201 new, 200 when a
// task already owns it.
func (s *server) adoptSession(w http.ResponseWriter, r *http.Request) error {
	ref, err := requestSession(r)
	if err != nil {
		return err
	}
	var req struct {
		CallerSession   *db.SessionRef `json:"callerSession"`
		ProjectID       *int64         `json:"projectId"`
		NewProjectTitle string         `json:"newProjectTitle"`
		Title           *string        `json:"title"`
		InitialNote     *string        `json:"initialNote"`
		TreeID          *int64         `json:"treeId"`
	}
	if err := decodeStrict(r, &req); err != nil {
		return err
	}
	if req.ProjectID != nil && strings.TrimSpace(req.NewProjectTitle) != "" {
		return db.Invalid("choose an existing project or a new project title")
	}
	ctx := r.Context()
	if req.ProjectID != nil {
		id, err := s.projectID(ctx, *req.ProjectID)
		if err != nil {
			return err
		}
		req.ProjectID = &id
	}
	workspace, err := s.prepareAdoption(ctx, ref)
	if err != nil {
		return err
	}
	task, created, err := s.Store.AdoptSession(ctx, db.AdoptInput{
		ProjectID: req.ProjectID, NewProjectTitle: req.NewProjectTitle, Workspace: workspace,
		Session: ref, CallerSession: req.CallerSession, Title: req.Title, InitialNote: req.InitialNote, TreeID: req.TreeID,
		TrackUnbound: humanTracking(r), RequireLive: true,
	})
	if err != nil {
		return err
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	if created || req.TreeID != nil {
		s.treeChanged()
	}
	writeJSON(w, status, s.task(task))
	return nil
}

func requestSession(r *http.Request) (db.SessionRef, error) {
	id, err := strconv.ParseInt(r.PathValue("connectionId"), 10, 64)
	ref := db.SessionRef{ConnectionID: id, ExternalSessionID: r.PathValue("id")}
	if err != nil || id <= 0 || strings.TrimSpace(ref.ExternalSessionID) == "" {
		return ref, db.Invalid("connectionId and externalSessionId are required")
	}
	return ref, nil
}

func humanTracking(r *http.Request) bool {
	actor, _ := principal.From(r.Context())
	return actor.IsHuman() || actor.Kind == principal.Personal && r.Header.Get(principal.ToolTransportHeader) != "mcp"
}

// A session's directory can be a worktree. Use it to disambiguate native
// roots only when the adapter reported an exact match.
func adoptionWorkspace(o execution.Observation, ps []execution.WorkspaceInfo) (execution.WorkspaceInfo, error) {
	var matches, exact []execution.WorkspaceInfo
	for _, p := range ps {
		if p.ID == o.Project {
			matches = append(matches, p)
			if o.Directory != "" && p.WorkspaceRoot == o.Directory {
				exact = append(exact, p)
			}
		}
	}
	if len(matches) > 1 {
		if len(exact) != 1 {
			return execution.WorkspaceInfo{}, db.Conflict("session workspace is ambiguous in this harness")
		}
		matches = exact
	}
	if len(matches) == 0 || matches[0].DeletedAt != nil || matches[0].ArchivedAt != nil ||
		matches[0].Evidence != nil && matches[0].Evidence.Gone {
		return execution.WorkspaceInfo{}, db.Conflict("session workspace is unavailable; restore it in its harness before adoption")
	}
	return matches[0], nil
}

func (s *server) prepareAdoption(ctx context.Context, ref db.SessionRef) (*db.AdoptionWorkspace, error) {
	_, err := s.Store.TaskBySession(ctx, ref)
	if err == nil {
		return nil, nil
	}
	if !errors.Is(err, db.ErrNotFound) {
		return nil, err
	}
	if _, err := s.Store.TaskSessionOwner(ctx, ref); !errors.Is(err, db.ErrNotFound) {
		return nil, err
	}
	_, workspace, err := s.verifyAdoption(ctx, ref, execution.SessionReadConfirm, true)
	return workspace, err
}

func (s *server) verifyAdoption(ctx context.Context, ref db.SessionRef, mode execution.SessionReadMode, checkOperation bool) (execution.Observation, *db.AdoptionWorkspace, error) {
	var o execution.Observation
	c, err := s.Store.GetHarnessConnection(ctx, ref.ConnectionID)
	if err != nil {
		return o, nil, err
	}
	active, identity, err := s.Store.ActiveConnectionIdentity(ctx, c.MachineID, c.Harness)
	if err != nil {
		return o, nil, err
	}
	if active != ref.ConnectionID {
		return o, nil, db.ConnectionRetired()
	}
	if checkOperation {
		if err := s.adoptionAvailable(ctx, ref.ConnectionID); err != nil {
			return o, nil, err
		}
	}
	// Inventory refreshes an adapter's roster when its polling observations
	// can be cached, including on older workers.
	inventory, inventoryErr := s.Engine.Discovery.Inventory(ctx, ref.ConnectionID)
	o, err = s.Engine.Coordinator.ReadScopedSession(execution.WithSessionReadMode(ctx, mode), ref)
	if err != nil {
		return o, nil, err
	}
	if err := db.CheckAdoptableSession(o); err != nil {
		return o, nil, err
	}
	if inventoryErr != nil {
		return o, nil, &db.HarnessError{Err: inventoryErr}
	}
	p, err := adoptionWorkspace(o, inventory)
	if err != nil {
		return o, nil, err
	}
	return o, &db.AdoptionWorkspace{
		Binding:            db.ProjectBinding{ConnectionID: ref.ConnectionID, ExternalProjectID: p.ID, Workspace: p.WorkspaceRoot},
		Inventory:          inventory,
		Observation:        o,
		ConnectionIdentity: &identity,
	}, nil
}

func (s *server) adoptionAvailable(ctx context.Context, connection int64) error {
	a, err := s.Engine.Connections.Catalog(connection)
	if err != nil {
		return err
	}
	f, err := a.Features(ctx)
	if err != nil {
		return &db.HarnessError{Err: err}
	}
	if op := f.Operations[execution.OpAdopt]; !op.Available {
		reason := op.Reason
		if reason == "" {
			reason = "This connection does not support tracking sessions."
		}
		return &db.ConflictError{Code: "operation_unavailable", Msg: reason}
	}
	return nil
}

// Resolve an owner entirely from Stage. Observe only an explicitly named unowned session.
func (s *server) sessionContext(w http.ResponseWriter, r *http.Request) error {
	ref, err := requestSession(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	out := struct {
		UnavailableReason string          `json:"unavailableReason,omitempty"`
		Workspace         string          `json:"workspace,omitempty"`
		Session           db.SessionRef   `json:"session"`
		Task              *taskJSON       `json:"task"`
		Project           *db.Project     `json:"project"`
		Title             string          `json:"title"`
		Links             execution.Links `json:"links"`
	}{Session: ref}
	t, err := s.Store.TaskBySession(ctx, ref)
	if err == nil {
		p, e := s.Store.GetProject(ctx, t.ProjectID)
		if e != nil {
			return e
		}
		shaped := s.task(t)
		out.Task = &shaped
		p = s.projectView(p)
		out.Project = &p
		out.Title = t.Title
		o, e := s.Store.Observation(ctx, ref)
		if e == nil {
			out.Links = o.Links
		} else if !errors.Is(e, db.ErrNotFound) {
			return e
		}
	} else {
		if !errors.Is(err, db.ErrNotFound) {
			return err
		}
		human := humanTracking(r)
		o, workspace, e := s.verifyAdoption(ctx, ref, execution.SessionReadPreview, human)
		out.Title = o.Title
		out.Links = o.Links
		if e != nil {
			var conflict *db.ConflictError
			if !errors.As(e, &conflict) || o.Session != ref && conflict.Code != "operation_unavailable" {
				return e
			}
			out.UnavailableReason = conflict.Msg
			writeJSON(w, 200, out)
			return nil
		}
		out.Workspace = workspace.Binding.Workspace
		p, e := s.Store.AdoptionPreviewProject(ctx, workspace.Binding, human)
		if e == nil {
			out.Project = &p
		} else if !errors.Is(e, db.ErrNotFound) {
			return e
		}
	}
	writeJSON(w, 200, out)
	return nil
}

// ---- snippets ----

func validateSnippet(p *db.SnippetPatch, create bool) error {
	if create && !p.Name.Set {
		return db.Invalid("name is required")
	}
	if create && !p.Body.Set {
		return db.Invalid("body is required")
	}
	if err := requiredText("name", &p.Name); err != nil {
		return err
	}
	if p.Body.Set && (p.Body.Null || strings.TrimSpace(p.Body.V) == "") {
		return db.Invalid("body must be a non-empty string")
	}
	return nil
}

func (s *server) resolveSnippetProject(ctx context.Context, p *db.SnippetPatch) error {
	if !p.ProjectID.Set || p.ProjectID.Null {
		return nil
	}
	id, err := s.projectID(ctx, p.ProjectID.V)
	if errors.Is(err, db.ErrNotFound) {
		return db.Invalid("unknown projectId")
	}
	if err != nil {
		return err
	}
	p.ProjectID.V = id
	return nil
}

func (s *server) listSnippets(w http.ResponseWriter, r *http.Request) error {
	sns, err := s.Store.ListSnippets(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"snippets": sns})
	return nil
}

func (s *server) createSnippet(w http.ResponseWriter, r *http.Request) error {
	var p db.SnippetPatch
	if err := decode(r, &p); err != nil {
		return err
	}
	if err := validateSnippet(&p, true); err != nil {
		return err
	}
	if err := s.resolveSnippetProject(r.Context(), &p); err != nil {
		return err
	}
	sn, err := s.Store.CreateSnippet(r.Context(), p)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, sn)
	return nil
}

func (s *server) getSnippet(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	sn, err := s.Store.GetSnippet(r.Context(), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, sn)
	return nil
}

func (s *server) updateSnippet(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var p db.SnippetPatch
	if err := decode(r, &p); err != nil {
		return err
	}
	if err := validateSnippet(&p, false); err != nil {
		return err
	}
	if err := s.resolveSnippetProject(r.Context(), &p); err != nil {
		return err
	}
	sn, err := s.Store.UpdateSnippet(r.Context(), id, p)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, sn)
	return nil
}

func (s *server) deleteSnippet(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteSnippet(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
