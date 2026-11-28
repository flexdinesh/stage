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
	msg := "An agent cannot complete its own task. Report the outcome with a journal note; the user or another agent completes it."
	if reopen {
		msg = "An agent cannot reopen its own task."
	}
	caller, refused, err := s.selfAction(w, r, id, msg)
	if err != nil || refused {
		return err
	}
	var in db.AcceptanceRequest
	if err = decodeStrict(r, &in); err != nil {
		return err
	}
	in.CallerSession = caller
	result, err := s.Store.ChangeAcceptance(r.Context(), id, in, reopen)
	if err != nil {
		return err
	}
	s.treeChanged()
	// An agent's completion notifies (completed, and tree_completed when it
	// finishes the tree); a person's own completion never does.
	if !reopen && !result.Replayed && result.AcceptanceID != nil && s.Push != nil &&
		db.WorkAttribution(r.Context()).Actor.Kind == "agent" {
		s.Push.OnCompleted(r.Context(), id, *result.AcceptanceID)
	}
	writeJSON(w, http.StatusOK, result)
	return nil
}
