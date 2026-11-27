package api

import (
	"net/http"

	"github.com/flexdinesh/stage/server/internal/db"
)

func (s *server) listProjectGroups(w http.ResponseWriter, r *http.Request) error {
	archived := r.URL.Query().Get("archived")
	if archived != "" {
		if err := validEnum("archived", archived, []string{"true", "false", "all"}); err != nil {
			return err
		}
	}
	if projectSummaryRequest(r) {
		summaries, err := s.Store.ListProjectGroupSummaries(r.Context(), archived)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, map[string]any{"groups": summaries})
		return nil
	}
	groups, err := s.Store.ListProjectGroups(r.Context(), archived)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": groups})
	return nil
}

func (s *server) getProjectGroup(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	if projectSummaryRequest(r) {
		project, err := s.Store.GetProject(r.Context(), id)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, db.SummarizeProjectGroup(project))
		return nil
	}
	group, err := s.Store.GetProjectGroup(r.Context(), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, group)
	return nil
}

func (s *server) editProjectGroup(w http.ResponseWriter, r *http.Request) error {
	var in db.GroupEditInput
	if err := decodeStrict(r, &in); err != nil {
		return err
	}
	edit, err := s.Store.EditProjectGroup(r.Context(), in)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, edit)
	return nil
}
