package api

import (
	"net/http"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
)

// ---- explicit session lookup and adoption ----

// adoptThread: POST /api/connections/1/sessions/{id}/adopt {title?, initialNote?, tags?,
// treeId?} makes the session a task ("Track as task"): 201 new, 200 when a
// task already owns it.
func (s *server) adoptThread(w http.ResponseWriter, r *http.Request) error {
	if r.PathValue("connectionId") != "1" {
		return db.Invalid("this session connection is unavailable; use a supported connection")
	}

	id := r.PathValue("id")
	var req struct {
		ProjectID       *int64   `json:"projectId"`
		NewProjectTitle string   `json:"newProjectTitle"`
		Title           *string  `json:"title"`
		InitialNote     *string  `json:"initialNote"`
		Tags            []string `json:"tags"`
		TreeID          *int64   `json:"treeId"`
	}
	if err := decodeStrict(r, &req); err != nil {
		return err
	}
	if err := s.refreshSession(r, id); err != nil {
		return err
	}
	var binding *db.ProjectBinding
	if req.ProjectID != nil || strings.TrimSpace(req.NewProjectTitle) != "" {
		if req.ProjectID != nil && strings.TrimSpace(req.NewProjectTitle) != "" {
			return db.Invalid("choose an existing project or a new project title, not both")
		}
		thread, err := s.Store.GetThread(r.Context(), id)
		if err != nil {
			return err
		}
		if thread.TaskID == nil {
			adapter, err := s.Sync.Adapter(db.T3ConnectionID)
			if err != nil {
				return err
			}
			projects, err := adapter.Workspaces(r.Context())
			if err != nil {
				return &db.HarnessError{Err: err}
			}
			for _, p := range projects {
				if p.ID == thread.T3ProjectID && p.DeletedAt == nil {
					binding = &db.ProjectBinding{ConnectionID: db.T3ConnectionID, ExternalProjectID: p.ID, Workspace: p.WorkspaceRoot}
					break
				}
			}
			if binding == nil {
				return db.Conflict("session workspace is unavailable; restore its T3 project before adopting")
			}
		}
	}
	task, created, err := s.Store.AdoptThread(r.Context(), db.AdoptInput{
		ProjectID: req.ProjectID, NewProjectTitle: req.NewProjectTitle, Binding: binding,
		ThreadID: id, Title: req.Title, InitialNote: req.InitialNote, Tags: cleanTags(req.Tags), TreeID: req.TreeID,
	})
	if err != nil {
		return err
	}
	status := http.StatusOK
	if created {
		if err := s.Sync.PollTask(r.Context(), task); err != nil {
			return err
		}
		task, err = s.Store.GetTask(r.Context(), task.ID)
		if err != nil {
			return err
		}
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
		return db.Invalid("this session connection is unavailable; use a supported connection")
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
