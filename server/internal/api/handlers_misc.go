package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// ---- explicit session lookup and adoption ----

// adoptThread: POST /api/connections/1/sessions/{id}/adopt {title?, initialNote?, tags?,
// treeId?} makes the session a task ("Track as task"): 201 new, 200 when a
// task already owns it.
func (s *server) adoptThread(w http.ResponseWriter, r *http.Request) error {
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
		Tags            []string       `json:"tags"`
		TreeID          *int64         `json:"treeId"`
	}
	if err := decodeStrict(r, &req); err != nil {
		return err
	}
	if req.ProjectID != nil && strings.TrimSpace(req.NewProjectTitle) != "" {
		return db.Invalid("choose an existing project or a new project title")
	}
	binding, err := s.prepareAdoption(r, ref)
	if err != nil {
		return err
	}
	task, created, err := s.Store.AdoptThread(r.Context(), db.AdoptInput{
		ProjectID: req.ProjectID, NewProjectTitle: req.NewProjectTitle, Binding: binding,
		Session: ref, CallerSession: req.CallerSession, Title: req.Title, InitialNote: req.InitialNote, Tags: cleanTags(req.Tags), TreeID: req.TreeID,
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
	writeJSON(w, status, s.task(s.envID(r.Context()), task))
	return nil
}

func (s *server) getThread(w http.ResponseWriter, r *http.Request) error {
	if r.PathValue("connectionId") != "1" {
		return s.sessionContext(w, r)
	}

	id := r.PathValue("id")
	if s.connected(r.Context(), db.T3ConnectionID) {
		if err := s.Sync.RefreshSession(r.Context(), id); err != nil {
			return err
		}
	}
	thread, err := s.Store.GetThread(r.Context(), id)
	if err != nil {
		return err
	}
	env := s.envID(r.Context())
	var task *taskJSON
	if thread.TaskID != nil {
		t, err := s.Store.GetTask(r.Context(), *thread.TaskID)
		if err != nil {
			return err
		}
		shaped := s.task(env, t)
		task = &shaped
	}
	writeJSON(w, http.StatusOK, map[string]any{"thread": s.thread(env, thread),
		"task": task})
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

// A session's directory can be a worktree. Verify its external project's workspace with the adapter.
func (s *server) workspaceFor(r *http.Request, o execution.Observation) (*db.ProjectBinding, error) {
	a, err := s.Sync.Adapter(o.Session.ConnectionID)
	if err != nil {
		return nil, err
	}
	ps, err := a.Workspaces(r.Context())
	if err != nil {
		return nil, &db.HarnessError{Err: err}
	}
	var binding *db.ProjectBinding
	for _, p := range ps {
		if p.ID != o.Project || p.DeletedAt != nil || p.ArchivedAt != nil {
			continue
		}
		if binding != nil {
			return nil, db.Conflict("session workspace is ambiguous; confirm its project binding")
		}
		binding = &db.ProjectBinding{ConnectionID: o.Session.ConnectionID, ExternalProjectID: p.ID, Workspace: p.WorkspaceRoot}
	}
	if binding == nil {
		return nil, db.Conflict("session workspace is unavailable; restore its project before adoption")
	}
	return binding, nil
}
func (s *server) prepareAdoption(r *http.Request, ref db.SessionRef) (*db.ProjectBinding, error) {
	ctx := r.Context()
	_, err := s.Store.TaskBySession(ctx, ref)
	if err == nil {
		return nil, nil
	}
	if !errors.Is(err, db.ErrNotFound) {
		return nil, err
	}
	a, err := s.Sync.Adapter(ref.ConnectionID)
	if err != nil {
		return nil, err
	}
	f, err := a.Features(ctx)
	if err != nil {
		return nil, &db.HarnessError{Err: err}
	}
	if op := f.Operations["adopt"]; !op.Available {
		return nil, db.Conflict("adoption unavailable on this connection: %s", op.Reason)
	}
	if err := s.Sync.RefreshScopedSession(ctx, ref); err != nil {
		return nil, err
	}
	o, err := s.Store.Observation(ctx, ref)
	if err != nil {
		return nil, err
	}
	return s.workspaceFor(r, o)
}

// Resolve an owner entirely from Stage. Observe only an explicitly named unowned session.
func (s *server) sessionContext(w http.ResponseWriter, r *http.Request) error {
	ref, err := requestSession(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	out := struct {
		Session db.SessionRef   `json:"session"`
		Task    *taskJSON       `json:"task"`
		Project *db.Project     `json:"project"`
		Title   string          `json:"title"`
		Links   execution.Links `json:"links"`
	}{Session: ref}
	t, err := s.Store.TaskBySession(ctx, ref)
	if err == nil {
		p, e := s.Store.GetProject(ctx, t.ProjectID)
		if e != nil {
			return e
		}
		shaped := s.task("", t)
		out.Task = &shaped
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
		if err = s.Sync.RefreshScopedSession(ctx, ref); err != nil {
			return err
		}
		o, e := s.Store.Observation(ctx, ref)
		if e != nil {
			return e
		}
		out.Title = o.Title
		out.Links = o.Links
		b, e := s.workspaceFor(r, o)
		if e != nil {
			return e
		}
		p, e := s.Store.BoundProject(ctx, *b)
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
