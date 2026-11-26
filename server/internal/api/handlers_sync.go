package api

import (
	"net/http"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/harness"
)

func (s *server) runSync(w http.ResponseWriter, r *http.Request) error {
	// An unconfigured connection is skipped once another one is configured,
	// so a fresh server with one harness set up syncs only that one.
	var skip []int64
	configured := false
	s.connectors().Each(func(_ int64, _ harness.Descriptor, c harness.Connector) {
		sum := c.Summary(r.Context())
		if sum.Configured {
			configured = true
		} else if sum.ConnectionID != 0 {
			skip = append(skip, sum.ConnectionID)
		}
	})
	if !configured {
		skip = nil
	}
	res, err := s.Sync.FullSync(r.Context(), skip...)
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
	imports, err := s.Store.ImportSummaries(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": ps, "imports": imports})
	return nil
}

// createProject saves Stage metadata without contacting a harness.
func (s *server) createProject(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Title string `json:"title"`
	}
	if err := decodeStrict(r, &body); err != nil {
		return err
	}
	body.Title = strings.TrimSpace(body.Title)
	if body.Title == "" {
		return db.Invalid("title is required")
	}
	p, err := s.Store.InsertProject(r.Context(), db.Project{Title: body.Title})
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
