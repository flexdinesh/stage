package api

import (
	"net/http"

	"github.com/flexdinesh/stage/server/internal/db"
)

func (s *server) completionPreview(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	// Cached evidence makes the review usable without the harness. Normal
	// task polling refreshes it; confirmation checks the exact version shown.
	p, err := s.Store.CompletionPreview(r.Context(), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, p)
	return nil
}
func (s *server) completeTask(w http.ResponseWriter, r *http.Request) error {
	return s.changeAcceptance(w, r, false)
}
func (s *server) reopenTask(w http.ResponseWriter, r *http.Request) error {
	return s.changeAcceptance(w, r, true)
}
func (s *server) changeAcceptance(w http.ResponseWriter, r *http.Request, reopen bool) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in db.AcceptanceRequest
	if err = decodeStrict(r, &in); err != nil {
		return err
	}
	result, err := s.Store.ChangeAcceptance(r.Context(), id, in, reopen)
	if err != nil {
		return err
	}
	s.treeChanged()
	writeJSON(w, http.StatusOK, result)
	return nil
}
