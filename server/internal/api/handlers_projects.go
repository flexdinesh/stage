package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
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

func bindingPath(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("bindingId"), 10, 64)
	if err != nil || id <= 0 {
		return 0, db.Invalid("A positive bindingId is required.")
	}
	return id, nil
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
		available := availableDestinations(destinations)
		summaries := make([]db.ProjectSummary, 0, len(ps))
		for _, p := range ps {
			summaries = append(summaries, projectSummary(p, available))
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

type workspaceBindingInput struct {
	ConnectionID      int64   `json:"connectionId"`
	ExternalProjectID string  `json:"externalProjectId"`
	Workspace         *string `json:"workspace"`
}

func (in workspaceBindingInput) binding() (db.ProjectBinding, error) {
	if in.ConnectionID <= 0 || in.ExternalProjectID == "" || in.Workspace == nil {
		return db.ProjectBinding{}, db.Invalid("connectionId, externalProjectId and workspace are required")
	}
	return db.ProjectBinding{ConnectionID: in.ConnectionID, ExternalProjectID: in.ExternalProjectID, Workspace: *in.Workspace}, nil
}

// verifyProjectBindings checks the selected workspace against a complete live
// inventory on each connection. The store checks the refreshed rows on write.
func (s *server) verifyProjectBindings(ctx context.Context, bindings []db.ProjectBinding) error {
	inventories := make(map[int64][]execution.WorkspaceInfo)
	for _, b := range bindings {
		ws, ok := inventories[b.ConnectionID]
		if !ok {
			var err error
			ws, err = s.refreshWorkspaces(ctx, b.ConnectionID)
			if err != nil {
				return err
			}
			inventories[b.ConnectionID] = ws
		}
		if !slices.ContainsFunc(ws, func(w execution.WorkspaceInfo) bool {
			return w.ID == b.ExternalProjectID && w.WorkspaceRoot == b.Workspace &&
				w.DeletedAt == nil && (w.Evidence == nil || !w.Evidence.Gone)
		}) {
			return db.Conflict("workspace changed or is unavailable; refresh the project list and select it again")
		}
	}
	return nil
}

func (s *server) createProject(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Title    string                  `json:"title"`
		Bindings []workspaceBindingInput `json:"bindings"`
	}
	if err := decodeStrict(r, &in); err != nil {
		return err
	}
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		return db.Invalid("title is required")
	}
	project := db.Project{Title: in.Title}
	for _, input := range in.Bindings {
		b, err := input.binding()
		if err != nil {
			return err
		}
		project.Bindings = append(project.Bindings, b)
	}
	ctx := r.Context()
	if err := s.verifyProjectBindings(ctx, project.Bindings); err != nil {
		return err
	}
	p, err := s.Store.InsertProject(ctx, project)
	if err != nil {
		return err
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
		destinations, err := s.destinations(r.Context())
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, projectSummary(p, availableDestinations(destinations)))
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

func (s *server) bindProject(w http.ResponseWriter, r *http.Request) error {
	id, err := s.projectPath(r)
	if err != nil {
		return err
	}
	var in struct {
		workspaceBindingInput
		Primary  bool   `json:"primary"`
		Replaces *int64 `json:"replaces"`
	}
	if err := decodeStrict(r, &in); err != nil {
		return err
	}
	b, err := in.binding()
	if err != nil {
		return err
	}
	b.ProjectID, b.Primary = id, in.Primary
	if in.Replaces != nil {
		if *in.Replaces <= 0 {
			return db.Invalid("replaces must be a positive bindingId")
		}
		b.Replaces = *in.Replaces
	}
	ctx := r.Context()
	if err := s.verifyProjectBindings(ctx, []db.ProjectBinding{b}); err != nil {
		return err
	}
	p, err := s.Store.BindProject(ctx, b)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, s.projectView(p))
	return nil
}

func (s *server) unbindProject(w http.ResponseWriter, r *http.Request) error {
	id, err := s.projectPath(r)
	if err != nil {
		return err
	}
	binding, err := bindingPath(r)
	if err != nil {
		return err
	}
	p, err := s.Store.UnbindProject(r.Context(), id, binding)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, s.projectView(p))
	return nil
}

func (s *server) setPrimaryBinding(w http.ResponseWriter, r *http.Request) error {
	id, err := s.projectPath(r)
	if err != nil {
		return err
	}
	binding, err := bindingPath(r)
	if err != nil {
		return err
	}
	p, err := s.Store.SetPrimaryBinding(r.Context(), id, binding)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, s.projectView(p))
	return nil
}

func (s *server) mergeProjects(w http.ResponseWriter, r *http.Request) error {
	id, err := s.projectPath(r)
	if err != nil {
		return err
	}
	var in struct {
		From         int64  `json:"from"`
		Confirm      bool   `json:"confirm"`
		PreviewToken string `json:"previewToken"`
	}
	if err := decodeStrict(r, &in); err != nil {
		return err
	}
	if in.From <= 0 {
		return db.Invalid("from must be a positive project id")
	}
	if in.Confirm && in.PreviewToken == "" {
		return db.Invalid("previewToken is required to confirm a merge")
	}
	ctx := r.Context()
	from, err := s.projectID(ctx, in.From)
	if err != nil {
		return err
	}
	result, err := s.Store.MergeProjects(ctx, id, from, in.Confirm, in.PreviewToken)
	if err != nil {
		var changed *db.PreviewChangedError
		if errors.As(err, &changed) {
			if fresh, ok := changed.Fresh.(db.MergeResult); ok {
				fresh.Project = s.projectView(fresh.Project)
				changed.Fresh = fresh
			}
		}
		return err
	}
	result.Project = s.projectView(result.Project)
	writeJSON(w, http.StatusOK, result)
	return nil
}

func (s *server) dismissProjectMerge(w http.ResponseWriter, r *http.Request) error {
	id, err := s.projectPath(r)
	if err != nil {
		return err
	}
	var in struct {
		OtherID int64 `json:"otherId"`
	}
	if err := decodeStrict(r, &in); err != nil {
		return err
	}
	if in.OtherID <= 0 {
		return db.Invalid("otherId must be a positive project id")
	}
	ctx := r.Context()
	other, err := s.projectID(ctx, in.OtherID)
	if err != nil {
		return err
	}
	if err := s.Store.DismissProjectMerge(ctx, id, other); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *server) splitProject(w http.ResponseWriter, r *http.Request) error {
	id, err := s.projectPath(r)
	if err != nil {
		return err
	}
	var in struct {
		BindingID int64  `json:"bindingId"`
		Title     string `json:"title"`
	}
	if err := decodeStrict(r, &in); err != nil {
		return err
	}
	if in.BindingID <= 0 {
		return db.Invalid("A positive bindingId is required.")
	}
	result, err := s.Store.SplitProject(r.Context(), id, in.BindingID, in.Title)
	if err != nil {
		return err
	}
	result.Project, result.Source = s.projectView(result.Project), s.projectView(result.Source)
	writeJSON(w, http.StatusCreated, result)
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
