package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// plannedConnection: id has an adapter or is a connector's active
// connection (set up or not).
func (s *server) plannedConnection(ctx context.Context, id int64) error {
	if _, _, ok := s.connectors().Owner(ctx, id); ok {
		return nil
	}
	_, err := s.Sync.Adapter(id)
	return err
}

// connectionPath is the {connectionId} path value; none means the legacy T3
// connection (the local machine's active T3).
func (s *server) connectionPath(r *http.Request) (int64, error) {
	if r.PathValue("connectionId") == "" {
		return s.Store.LegacyT3ID(), nil
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
		if !c.Active || s.plannedConnection(r.Context(), c.ID) == nil {
			out = append(out, s.configured(r.Context(), c))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"connections": out})
	return nil
}

// resolveConnection is GET /api/connections/resolve?harness=KIND: the
// active configured connection of KIND on the request's machine (the
// forwarding machine of an agent gateway request, else the local machine).
// Agents use it to name their session from the harness's environment
// (harness.Attribution.Env). 404 no_active_connection when there is none.
func (s *server) resolveConnection(w http.ResponseWriter, r *http.Request) error {
	kind := r.URL.Query().Get("harness")
	d, ok := s.registry().Lookup(kind)
	if !ok {
		return &db.InvalidError{Msg: fmt.Sprintf("harness: unknown kind %q", kind)}
	}
	machine := db.LocalMachine
	if m := db.ActingMachine(r.Context()); m != nil {
		machine = *m
	}
	cs, err := s.Store.Connections(r.Context())
	if err != nil {
		return err
	}
	for _, c := range cs {
		if c.Active && c.Harness == d.Kind && c.MachineID == machine {
			if c = s.configured(r.Context(), c); c.Configured {
				writeJSON(w, http.StatusOK, c)
				return nil
			}
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{
		"error": fmt.Sprintf("no configured %s connection on machine %d: set it up in Stage", d.Name, machine),
		"code":  "no_active_connection",
	})
	return nil
}

func (s *server) configured(ctx context.Context, c db.HarnessConnection) db.HarnessConnection {
	if !c.Active {
		c.Configured = false
		return c
	}
	if _, k, ok := s.connectors().Owner(ctx, c.ID); ok {
		c.Configured = k.Summary(ctx).Configured
		return c
	}
	_, adapterErr := s.Sync.Adapter(c.ID)
	c.Configured = adapterErr == nil
	return c
}

// forgetConnection is DELETE /api/connections/{connectionId}: lets go of a
// replaced connection (db.Store.ForgetConnection), or of an active one that
// is disconnected on a machine that answers now (db.Store.RetireAndForget).
func (s *server) forgetConnection(w http.ResponseWriter, r *http.Request) error {
	id, err := s.connectionPath(r)
	if err != nil {
		return err
	}
	c, err := s.Store.GetHarnessConnection(r.Context(), id)
	if err != nil {
		return err
	}
	if !c.Active {
		err = s.Store.ForgetConnection(r.Context(), id)
	} else if !s.machineReachable(c.MachineID) {
		err = db.Conflict("This connection's machine is offline. Forget it once the machine is back.")
	} else if s.configured(r.Context(), c).Configured {
		err = db.Conflict("This connection is in use. Disconnect it first.")
	} else if err = s.Store.RetireAndForget(r.Context(), id); err == nil {
		// Never polled or launched on again, even once the harness is set up
		// anew (a remote machine's adapters follow the record).
		if l, ok := s.Sync.Machines().Local(c.MachineID); ok {
			l.Retired(id)
		}
	}
	if err != nil {
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

// setNewTaskConfig is PUT /api/connections/{id}/new-task-config, body
// {"config": {model, options} | null}: pins (or clears) the model and options
// new tasks on the connection start with. The selection is checked against
// the connection's capabilities. Permission and mode are never pinned.
func (s *server) setNewTaskConfig(w http.ResponseWriter, r *http.Request) error {
	id, err := s.connectionPath(r)
	if err != nil {
		return err
	}
	var in struct {
		Config *execution.Config `json:"config"`
	}
	if err = decodeStrict(r, &in); err != nil {
		return err
	}
	if !s.Store.ConnectionActive(id) {
		return db.Invalid("Only an active connection takes new-task defaults.")
	}
	if c := in.Config; c != nil {
		if c.Permission != "" || c.Mode != "" {
			return db.Invalid("Permission and mode are not pinned: new tasks start at the harness default.")
		}
		if c.Model == "" && len(c.Options) == 0 {
			in.Config = nil
		} else if err := s.validateSelection(r.Context(), id, 0, c); err != nil {
			return err
		}
	}
	c, err := s.Store.SetNewTaskConfig(r.Context(), id, in.Config)
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
	t3 := s.Store.LegacyT3ID()
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
