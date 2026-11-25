package api

import (
	"net/http"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/db"
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

// respondRequest answers for the user only: a request naming an agent's
// session (the Stage-Caller-Session header the CLI and MCP send) or
// forwarded by a worker's agent gateway is refused, so an agent cannot
// approve a session's prompt (best effort: a bare request carries neither).
func (s *server) respondRequest(w http.ResponseWriter, r *http.Request) error {
	if r.Header.Get(agentctx.CallerHeader) != "" || db.ActingMachine(r.Context()) != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Approvals and answers come from you, in Stage or in the harness, not from an agent.", "code": "agent_forbidden"})
		return nil
	}
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
