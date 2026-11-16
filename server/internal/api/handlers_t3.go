package api

import (
	"errors"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// t3StatusJSON is T3Status (docs/api.md "GET /api/t3/status").
type t3StatusJSON struct {
	Connected            bool             `json:"connected"`
	State                t3.State         `json:"state"`
	T3URL                string           `json:"t3Url"`
	PublicURL            string           `json:"publicUrl"`
	T3URLSource          string           `json:"t3UrlSource"`
	EnvironmentID        *string          `json:"environmentId"`
	ClientID             *string          `json:"clientId"`
	TokenExpiresAt       *time.Time       `json:"tokenExpiresAt"`
	AdminTokenConfigured bool             `json:"adminTokenConfigured"`
	LastError            *string          `json:"lastError"`
	LastFullSyncAt       *time.Time       `json:"lastFullSyncAt"`
	Reachable            bool             `json:"reachable"`
	CheckedAt            *time.Time       `json:"checkedAt"`
	ProbeError           *string          `json:"probeError"`
	T3                   *t3IdentityJSON  `json:"t3"`
	AutoPair             autoPairJSON     `json:"autoPair"`
	Sync                 syncProgressJSON `json:"sync"`
}

type t3IdentityJSON struct {
	EnvironmentID string `json:"environmentId"`
	Label         string `json:"label"`
	ServerVersion string `json:"serverVersion"`
}

type autoPairJSON struct {
	Available bool    `json:"available"`
	Reason    *string `json:"reason"`
}

type syncProgressJSON struct {
	Running   bool       `json:"running"`
	StartedAt *time.Time `json:"startedAt"`
	Projects  int        `json:"projects"`
	Threads   int        `json:"threads"`
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// statusJSON builds T3Status; refresh refreshes a stale probe first.
func (s *server) statusJSON(r *http.Request, refresh bool) (t3StatusJSON, error) {
	setup := s.Setup.Status(r.Context(), refresh)
	st := setup.Conn
	conn, err := s.Store.GetConnection(r.Context())
	if err != nil {
		return t3StatusJSON{}, err
	}
	out := t3StatusJSON{
		Connected:            st.Connected,
		State:                setup.State,
		T3URL:                setup.Endpoint.URL,
		PublicURL:            setup.Endpoint.PublicURL,
		T3URLSource:          setup.Endpoint.Source,
		EnvironmentID:        strPtr(st.EnvironmentID),
		ClientID:             strPtr(st.ClientID),
		AdminTokenConfigured: s.Config.T3AdminToken != "",
		LastError:            strPtr(st.LastError),
		LastFullSyncAt:       conn.LastFullSyncAt,
		Reachable:            st.Probe.Reachable,
		ProbeError:           strPtr(st.Probe.Err),
		AutoPair:             autoPairJSON{Available: setup.AutoPair.Available, Reason: strPtr(setup.AutoPair.Reason)},
	}
	if st.ExpiresAt != nil {
		exp := st.ExpiresAt.UTC()
		out.TokenExpiresAt = &exp
	}
	if out.LastError == nil {
		out.LastError = conn.LastError
	}
	if !st.Probe.CheckedAt.IsZero() {
		at := st.Probe.CheckedAt.UTC()
		out.CheckedAt = &at
	}
	if id := st.Probe.Identity; id != nil {
		out.T3 = &t3IdentityJSON{EnvironmentID: id.EnvironmentID, Label: id.Label, ServerVersion: id.ServerVersion}
	}
	if s.Sync != nil {
		p := s.Sync.Progress()
		out.Sync = syncProgressJSON{Running: p.Running, StartedAt: p.StartedAt, Projects: p.Projects, Threads: p.Threads}
	}
	return out, nil
}

func (s *server) writeStatus(w http.ResponseWriter, r *http.Request, refresh bool) error {
	out, err := s.statusJSON(r, refresh)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *server) t3Status(w http.ResponseWriter, r *http.Request) error {
	return s.writeStatus(w, r, true)
}

func (s *server) t3Pair(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Code string `json:"code"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	code := strings.TrimSpace(body.Code)
	if code == "" {
		return db.Invalid("code is required")
	}
	if err := s.Setup.Pair(r.Context(), code); err != nil {
		return err
	}
	return s.writeStatus(w, r, false)
}

// t3PairAuto pairs through the t3 CLI on this machine.
func (s *server) t3PairAuto(w http.ResponseWriter, r *http.Request) error {
	if err := s.Setup.AutoPair(r.Context()); err != nil {
		return err
	}
	return s.writeStatus(w, r, false)
}

// t3SetURL saves the T3 URL setting; {"url": null} resets it.
func (s *server) t3SetURL(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		URL db.Optional[string] `json:"url"`
	}
	if err := decodeStrict(r, &body); err != nil {
		return err
	}
	if !body.URL.Set {
		return db.Invalid("url is required (null resets to the default)")
	}
	if err := s.Setup.SetURL(r.Context(), body.URL.Ptr()); err != nil {
		return err
	}
	return s.writeStatus(w, r, true)
}

func (s *server) runSync(w http.ResponseWriter, r *http.Request) error {
	res, err := s.Sync.FullSync(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, res)
	return nil
}

func (s *server) catalog(w http.ResponseWriter, r *http.Request) error {
	snap, err := s.Sync.Catalog(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, catalogShape(snap))
	return nil
}

// ---- projects ----

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
	writeJSON(w, http.StatusOK, map[string]any{"projects": ps})
	return nil
}

func normalizeRoot(root string) string {
	root = strings.TrimSpace(root)
	if strings.HasPrefix(root, "/") {
		return path.Clean(root)
	}
	return root
}

// createProject: app project with the root → 409; T3 project with the root
// (mirror, refreshed from T3 first) → returned as mirrored (T3 owns title and
// archive state; the requested title is ignored); else create in T3.
func (s *server) createProject(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Title         string `json:"title"`
		WorkspaceRoot string `json:"workspaceRoot"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	title, root := strings.TrimSpace(body.Title), normalizeRoot(body.WorkspaceRoot)
	if title == "" || root == "" {
		return db.Invalid("title and workspaceRoot are required")
	}
	ctx := r.Context()
	existing, err := s.Store.ProjectByWorkspaceRoot(ctx, root)
	switch {
	case err == nil && existing.Source == db.SourceApp:
		return db.Conflict("project for %s already exists", root)
	case err == nil:
		writeJSON(w, http.StatusCreated, existing)
		return nil
	case !errors.Is(err, db.ErrNotFound):
		return err
	}
	t3Projects, err := s.T3.ListProjects(ctx)
	if err != nil {
		return &db.T3Error{Err: err}
	}
	if _, err := s.Store.UpsertT3Projects(ctx, t3Projects); err != nil {
		return err
	}
	if existing, err := s.Store.ProjectByWorkspaceRoot(ctx, root); err == nil {
		writeJSON(w, http.StatusCreated, existing)
		return nil
	} else if !errors.Is(err, db.ErrNotFound) {
		return err
	}
	tp, err := s.T3.CreateProject(ctx, t3.CreateProjectInput{Title: title, WorkspaceRoot: root})
	if err != nil {
		return &db.T3Error{Err: err}
	}
	if tp.Title != "" {
		title = tp.Title
	}
	p, err := s.Store.InsertProject(ctx, db.Project{
		T3ProjectID: tp.ID, Title: title, WorkspaceRoot: root, Source: db.SourceApp,
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, p)
	return nil
}

func (s *server) getProject(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	p, err := s.Store.GetProject(r.Context(), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, p)
	return nil
}
