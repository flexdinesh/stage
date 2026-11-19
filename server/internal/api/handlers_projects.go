package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
)

func (s *server) openCodeStatus(w http.ResponseWriter, r *http.Request) error {
	if s.OpenCode == nil {
		return db.Conflict("OpenCode setup is unavailable on this server.")
	}
	writeJSON(w, 200, s.OpenCode.Status(r.Context()))
	return nil
}
func (s *server) openCodeSettings(w http.ResponseWriter, r *http.Request) error {
	if s.OpenCode == nil {
		return db.Conflict("OpenCode setup is unavailable on this server.")
	}
	var in config.OpenCodeEdit
	if err := decodeStrict(r, &in); err != nil {
		return err
	}
	if err := s.OpenCode.Save(r.Context(), in); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *server) openCodeConnectLocal(w http.ResponseWriter, r *http.Request) error {
	if s.OpenCode == nil {
		return db.Conflict("OpenCode setup is unavailable on this server.")
	}
	var in struct {
		NewInstance bool `json:"newInstance"`
	}
	if err := decodeStrict(r, &in); err != nil {
		return err
	}
	if err := s.OpenCode.ConnectLocal(r.Context(), in.NewInstance); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
func (s *server) openCodePair(w http.ResponseWriter, r *http.Request) error {
	if s.OpenCode == nil {
		return db.Conflict("OpenCode setup is unavailable on this server.")
	}
	var in struct {
		Link        string `json:"link"`
		NewInstance bool   `json:"newInstance"`
	}
	if err := decodeStrict(r, &in); err != nil {
		return err
	}
	if err := s.OpenCode.Pair(r.Context(), in.Link, in.NewInstance); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
func (s *server) openCodeDisconnect(w http.ResponseWriter, r *http.Request) error {
	if s.OpenCode == nil {
		return db.Conflict("OpenCode setup is unavailable on this server.")
	}
	if err := s.OpenCode.Disconnect(r.Context()); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *server) plannedConnection(id int64) error {
	if s.OpenCode != nil && s.OpenCode.ID() == id {
		return nil
	}
	_, err := s.Sync.Adapter(id)
	return err
}

// connectionPath is the {connectionId} path value; none means the active
// T3 connection.
func (s *server) connectionPath(r *http.Request) (int64, error) {
	if r.PathValue("connectionId") == "" {
		return s.Store.T3ID(), nil
	}
	id, err := strconv.ParseInt(r.PathValue("connectionId"), 10, 64)
	if err != nil || id < 1 {
		return 0, db.Invalid("Choose a valid harness connection.")
	}
	return id, nil
}

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

// connections lists every connection: the active one per harness (when
// usable) and the replaced ones, so tasks on them still name their harness.
func (s *server) connections(w http.ResponseWriter, r *http.Request) error {
	cs, err := s.Store.Connections(r.Context())
	if err != nil {
		return err
	}
	out := []db.HarnessConnection{}
	for _, c := range cs {
		if !c.Active || s.plannedConnection(c.ID) == nil {
			out = append(out, s.configured(r.Context(), c))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"connections": out})
	return nil
}
func (s *server) configured(ctx context.Context, c db.HarnessConnection) db.HarnessConnection {
	if !c.Active {
		c.Configured = false
		return c
	}
	_, adapterErr := s.Sync.Adapter(c.ID)
	c.Configured = adapterErr == nil
	if c.Harness == db.HarnessT3 && s.Setup != nil {
		st := s.Setup.Status(ctx, false)
		c.Configured = st.Conn.HasToken || st.Conn.Connected || st.Endpoint.Source != "default"
	}
	return c
}

// forgetConnection is DELETE /api/connections/{connectionId}: lets go of a
// replaced connection (db.Store.ForgetConnection).
func (s *server) forgetConnection(w http.ResponseWriter, r *http.Request) error {
	id, err := s.connectionPath(r)
	if err != nil {
		return err
	}
	if err := s.Store.ForgetConnection(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// setAutoComplete is PUT /api/connections/{connectionId}/auto-complete {autoComplete}.
func (s *server) setAutoComplete(w http.ResponseWriter, r *http.Request) error {
	id, err := s.connectionPath(r)
	if err != nil {
		return err
	}
	var in struct {
		AutoComplete *bool `json:"autoComplete"`
	}
	if err = decodeStrict(r, &in); err != nil {
		return err
	}
	if in.AutoComplete == nil {
		return db.Invalid("autoComplete is required")
	}
	c, err := s.Store.SetAutoComplete(r.Context(), id, *in.AutoComplete)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"connection": s.configured(r.Context(), c)})
	return nil
}
func (s *server) externalProjects(w http.ResponseWriter, r *http.Request) error {
	id, err := s.connectionPath(r)
	if err != nil {
		return err
	}
	if _, err = s.Sync.Adapter(id); err != nil {
		return err
	}
	ps, err := s.Sync.Inventory(r.Context(), id)
	if err != nil {
		return &db.HarnessError{Err: err}
	}
	if _, err = s.Store.RefreshWorkspaces(r.Context(), id, ps); err != nil {
		return err
	}
	out, err := s.Store.ExternalProjects(r.Context(), id)
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
	adapter, err := s.Sync.Adapter(in.ConnectionID)
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
	connection, err := s.connectionPath(r)
	if err != nil {
		return err
	}
	p, err := s.Store.UnbindProject(r.Context(), id, connection)
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
	t3 := s.Store.T3ID()
	adapter, err := s.Sync.Adapter(t3)
	if err != nil {
		return err
	}
	p, err := adapter.RegisterWorkspace(r.Context(), in.Title, in.Workspace)
	if err != nil {
		return &db.HarnessError{Err: err}
	}
	writeJSON(w, http.StatusCreated, db.ExternalProject{ConnectionID: t3, ExternalID: p.ID, Title: p.Title, Workspace: p.WorkspaceRoot})
	return nil
}
