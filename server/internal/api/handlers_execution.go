package api

import (
	"github.com/flexdinesh/stage/server/internal/db"
	"net/http"
	"strconv"
)

func (s *server) executionCapabilities(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(r.PathValue("connectionId"), 10, 64)
	if err != nil {
		return db.Invalid("invalid connection")
	}
	a, err := s.Sync.Adapter(id)
	if err != nil {
		return err
	}
	c, err := a.Features(r.Context())
	if err != nil {
		return &db.HarnessError{Err: err}
	}
	writeJSON(w, 200, c)
	return nil
}
func (s *server) previewLaunchLink(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in struct {
		Link string `json:"link"`
	}
	if err = decodeStrict(r, &in); err != nil {
		return err
	}
	p, err := s.Sync.PreviewExisting(r.Context(), id, in.Link)
	if err != nil {
		return err
	}
	writeJSON(w, 200, p)
	return nil
}
func (s *server) attachLaunchLink(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in struct {
		Link        string `json:"link"`
		Attempt     int    `json:"attempt"`
		Revision    int64  `json:"revision"`
		Environment string `json:"environment"`
	}
	if err = decodeStrict(r, &in); err != nil {
		return err
	}
	t, err := s.Sync.LinkExisting(r.Context(), id, in.Link, in.Attempt, in.Revision, in.Environment)
	if err != nil {
		return err
	}
	writeJSON(w, 200, s.task(s.envID(r.Context()), t))
	return nil
}
