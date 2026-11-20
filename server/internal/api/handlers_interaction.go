package api

import (
	"net/http"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Interaction with a task's running session (docs/api.md "Session
// interaction"). A harness without the operation answers 409
// operation_unavailable.

func (s *server) taskRequests(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	rs, err := s.Sync.Requests(r.Context(), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": rs})
	return nil
}

func (s *server) respondRequest(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in execution.Response
	if err := decodeStrict(r, &in); err != nil {
		return err
	}
	t, err := s.Sync.Respond(r.Context(), id, r.PathValue("rid"), in)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, s.task(s.envID(r.Context()), t))
	return nil
}

func (s *server) sendMessage(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in execution.Message
	if err := decodeStrict(r, &in); err != nil {
		return err
	}
	t, err := s.Sync.Send(r.Context(), id, in)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, s.task(s.envID(r.Context()), t))
	return nil
}
