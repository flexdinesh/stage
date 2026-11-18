package api

import (
	"github.com/flexdinesh/stage/server/internal/db"
	"net/http"
	"strings"
)

func (s *server) updateProject(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in struct {
		Title    string `json:"title"`
		Archived bool   `json:"archived"`
	}
	if err = decodeStrict(r, &in); err != nil {
		return err
	}
	p, err := s.Store.UpdateProject(r.Context(), id, in.Title, in.Archived)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, p)
	return nil
}
func (s *server) connections(w http.ResponseWriter, r *http.Request) error {
	cs, err := s.Store.Connections(r.Context())
	if err != nil {
		return err
	}
	// Unimplemented integrations are storage-only, not selectable product options.
	out := []db.HarnessConnection{}
	for _, c := range cs {
		if _, err := s.Sync.Adapter(c.ID); err == nil {
			out = append(out, c)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"connections": out})
	return nil
}
func (s *server) externalProjects(w http.ResponseWriter, r *http.Request) error {
	adapter, err := s.Sync.Adapter(db.T3ConnectionID)
	if err != nil {
		return err
	}
	ps, err := adapter.Workspaces(r.Context())
	if err != nil {
		return &db.HarnessError{Err: err}
	}
	if _, err = s.Store.UpsertT3Projects(r.Context(), ps); err != nil {
		return err
	}
	out, err := s.Store.ExternalProjects(r.Context(), db.T3ConnectionID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": out})
	return nil
}
func (s *server) bindProject(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in struct {
		ConnectionID      int64  `json:"connectionId"`
		ExternalProjectID string `json:"externalProjectId"`
		Workspace         string `json:"workspace"`
	}
	if err = decodeStrict(r, &in); err != nil {
		return err
	}
	if in.ConnectionID != db.T3ConnectionID {
		return db.Invalid("this connection is not available; select T3")
	}
	adapter, err := s.Sync.Adapter(db.T3ConnectionID)
	if err != nil {
		return err
	}
	ps, err := adapter.Workspaces(r.Context())
	if err != nil {
		return &db.HarnessError{Err: err}
	}
	valid := false
	for _, p := range ps {
		if p.ID == in.ExternalProjectID && p.WorkspaceRoot == in.Workspace && p.DeletedAt == nil {
			valid = true
		}
	}
	if !valid {
		return db.Conflict("workspace changed or is unavailable; refresh the project list and select it again")
	}
	p, err := s.Store.BindProject(r.Context(), db.ProjectBinding{ProjectID: id, ConnectionID: in.ConnectionID, ExternalProjectID: in.ExternalProjectID, Workspace: in.Workspace})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, p)
	return nil
}
func (s *server) unbindProject(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	p, err := s.Store.UnbindProject(r.Context(), id, db.T3ConnectionID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, p)
	return nil
}

// Registration is a separate explicit side effect, never part of creating a
// Stage project. A lost response requires discovery before another registration.
func (s *server) registerWorkspace(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Title     string `json:"title"`
		Workspace string `json:"workspace"`
	}
	if err := decodeStrict(r, &in); err != nil {
		return err
	}
	in.Title = strings.TrimSpace(in.Title)
	in.Workspace = normalizeRoot(in.Workspace)
	if in.Title == "" || in.Workspace == "" {
		return db.Invalid("title and an existing directory are required")
	}
	adapter, err := s.Sync.Adapter(db.T3ConnectionID)
	if err != nil {
		return err
	}
	p, err := adapter.RegisterWorkspace(r.Context(), in.Title, in.Workspace)
	if err != nil {
		return &db.HarnessError{Err: err}
	}
	writeJSON(w, http.StatusCreated, db.ExternalProject{ConnectionID: db.T3ConnectionID, ExternalID: p.ID, Title: p.Title, Workspace: p.WorkspaceRoot})
	return nil
}
