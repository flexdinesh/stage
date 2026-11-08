package api

import (
	"errors"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/flexdinesh/t3-stage/server/internal/db"
	"github.com/flexdinesh/t3-stage/server/internal/t3"
)

type t3StatusJSON struct {
	Connected            bool       `json:"connected"`
	T3URL                string     `json:"t3Url"`
	PublicURL            string     `json:"publicUrl"`
	EnvironmentID        *string    `json:"environmentId"`
	ClientID             *string    `json:"clientId"`
	TokenExpiresAt       *time.Time `json:"tokenExpiresAt"`
	AdminTokenConfigured bool       `json:"adminTokenConfigured"`
	LastError            *string    `json:"lastError"`
	LastFullSyncAt       *time.Time `json:"lastFullSyncAt"`
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (s *server) statusJSON(r *http.Request) (t3StatusJSON, error) {
	st := s.T3.Status(r.Context())
	conn, err := s.Store.GetConnection(r.Context())
	if err != nil {
		return t3StatusJSON{}, err
	}
	out := t3StatusJSON{
		Connected:            st.Connected,
		T3URL:                s.Config.T3URL,
		PublicURL:            s.Config.T3PublicURL,
		EnvironmentID:        strPtr(st.EnvironmentID),
		ClientID:             strPtr(st.ClientID),
		AdminTokenConfigured: s.Config.T3AdminToken != "",
		LastError:            strPtr(st.LastError),
		LastFullSyncAt:       conn.LastFullSyncAt,
	}
	if st.ExpiresAt != nil {
		exp := st.ExpiresAt.UTC()
		out.TokenExpiresAt = &exp
	}
	if out.LastError == nil {
		out.LastError = conn.LastError
	}
	return out, nil
}

func (s *server) t3Status(w http.ResponseWriter, r *http.Request) error {
	out, err := s.statusJSON(r)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, out)
	return nil
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
	if err := s.T3.Pair(r.Context(), code); err != nil {
		// Always 502 "pairing failed" (drop the chain so ErrNotConnected can't map to 503).
		return &db.T3Error{Err: errors.New("pairing failed: " + err.Error())}
	}
	out, err := s.statusJSON(r)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, out)
	return nil
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
