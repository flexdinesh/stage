package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func (s *server) projectID(ctx context.Context, id int64) (int64, error) {
	return s.Store.ProjectID(ctx, id)
}

func (s *server) projectView(p db.Project) db.Project {
	registry := s.registry()
	for _, b := range p.Bindings {
		for i := range b.Facts {
			if b.Facts[i].ID == "archived" {
				b.Facts[i].Value = "in " + registry.Name(b.Harness)
			}
		}
	}
	return p
}

func (s *server) projectPath(r *http.Request) (int64, error) {
	id, err := pathID(r)
	if err != nil {
		return 0, err
	}
	return s.projectID(r.Context(), id)
}

func (s *server) listProjects(w http.ResponseWriter, r *http.Request) error {
	archived := r.URL.Query().Get("archived")
	if archived != "" {
		if err := validEnum("archived", archived, []string{"true", "false", "all"}); err != nil {
			return err
		}
	}
	ps, err := s.Store.ListProjects(r.Context(), archived)
	if err != nil {
		return err
	}
	if projectSummaryRequest(r) {
		destinations, err := s.destinations(r.Context())
		if err != nil {
			return err
		}
		summaries := make([]db.ProjectSummary, 0, len(ps))
		for _, p := range ps {
			summaries = append(summaries, projectSummary(p))
		}
		groups, err := s.Store.ListProjectGroupSummaries(r.Context(), archived)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, map[string]any{"projects": summaries, "groups": groups, "destinations": destinations})
		return nil
	}
	imports, err := s.Store.ImportSummaries(r.Context())
	if err != nil {
		return err
	}
	for i := range ps {
		ps[i] = s.projectView(ps[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": ps, "imports": imports})
	return nil
}

func (s *server) createProject(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Title    string          `json:"title"`
		Bindings json.RawMessage `json:"bindings"`
	}
	if err := decodeStrict(r, &in); err != nil {
		return err
	}
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		return db.Invalid("title is required")
	}
	if in.Bindings != nil {
		return db.Invalid("Project bindings were retired. Use POST /api/project-groups/membership to group an exact workspace.")
	}
	p, err := s.Store.InsertProject(r.Context(), db.Project{Title: in.Title})
	if err != nil {
		return err
	}
	if projectSummaryRequest(r) {
		writeJSON(w, http.StatusCreated, projectSummary(p))
		return nil
	}
	writeJSON(w, http.StatusCreated, s.projectView(p))
	return nil
}

func (s *server) getProject(w http.ResponseWriter, r *http.Request) error {
	id, err := s.projectPath(r)
	if err != nil {
		return err
	}
	p, err := s.Store.GetProject(r.Context(), id)
	if err != nil {
		return err
	}
	if projectSummaryRequest(r) {
		writeJSON(w, http.StatusOK, projectSummary(p))
		return nil
	}
	writeJSON(w, http.StatusOK, s.projectView(p))
	return nil
}

// plannedConnection: id has an adapter or is a connector's active
// connection (set up or not).
func (s *server) plannedConnection(ctx context.Context, id int64) error {
	if _, _, ok := s.connectors().Owner(ctx, id); ok {
		return nil
	}
	_, err := s.Engine.Connections.Adapter(id)
	return err
}

// connectionPath is the {connectionId} path value.
func (s *server) connectionPath(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("connectionId"), 10, 64)
	if err != nil || id < 1 {
		return 0, db.Invalid("Choose a valid harness connection.")
	}
	return id, nil
}

func (s *server) updateProject(w http.ResponseWriter, r *http.Request) error {
	id, err := s.projectPath(r)
	if err != nil {
		return err
	}
	// An omitted field keeps its current value.
	var in struct {
		Title    *string `json:"title"`
		Archived *bool   `json:"archived"`
	}
	if err = decodeStrict(r, &in); err != nil {
		return err
	}
	p, err := s.Store.UpdateProject(r.Context(), id, in.Title, in.Archived)
	if err != nil {
		return err
	}
	if projectSummaryRequest(r) {
		writeJSON(w, http.StatusOK, projectSummary(p))
		return nil
	}
	writeJSON(w, http.StatusOK, s.projectView(p))
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
	machine := s.Engine.Connections.Machines().DefaultID()
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
	_, adapterErr := s.Engine.Connections.Adapter(c.ID)
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
	} else if m, ok := s.Engine.Connections.Machines().Get(c.MachineID); !ok || !m.Online() {
		err = db.Conflict("This connection's machine is offline. Remove it once the machine is back.")
	} else if s.configured(r.Context(), c).Configured {
		err = db.Conflict("This connection is in use. Disconnect it first.")
	} else if err = s.Store.RetireAndForget(r.Context(), id); err == nil {
		m.Retired(id)
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
		} else if err := s.validateSelection(r.Context(), id, c); err != nil {
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

func (s *server) projectEvents(w http.ResponseWriter, r *http.Request) error {
	id, err := s.projectPath(r)
	if err != nil {
		return err
	}
	events, err := s.Store.ProjectEvents(r.Context(), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
	return nil
}
