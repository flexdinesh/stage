package api

import (
	"errors"
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
	rs, err := s.Engine.Coordinator.Requests(r.Context(), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": rs})
	return nil
}

// selfAction refuses (403 self_action_forbidden, true) a request whose
// caller session (agentctx.CallerHeader, which the CLI and MCP tools send)
// belongs to task id: an agent may complete, reopen, answer or message other
// work, never its own task or session. It applies to any principal naming a
// caller. Best effort: a request naming no caller session passes. It returns
// the named caller session for attribution.
func (s *server) selfAction(w http.ResponseWriter, r *http.Request, id int64, msg string) (*db.SessionRef, bool, error) {
	ref, own, err := s.callerOwns(r, id)
	if err != nil || !own {
		return ref, false, err
	}
	writeJSON(w, http.StatusForbidden, map[string]string{"error": msg, "code": "self_action_forbidden"})
	return ref, true, nil
}

// callerOwns reports whether the request's caller session belongs to task
// id, returning the named caller session (nil when none is named).
func (s *server) callerOwns(r *http.Request, id int64) (*db.SessionRef, bool, error) {
	v := r.Header.Get(agentctx.CallerHeader)
	if v == "" {
		return nil, false, nil
	}
	ref, err := agentctx.ParseSession(v)
	if err != nil {
		return nil, false, db.Invalid("%s: %v", agentctx.CallerHeader, err)
	}
	owner, err := s.Store.TaskBySession(r.Context(), ref)
	if errors.Is(err, db.ErrNotFound) {
		return &ref, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &ref, owner.ID == id, nil
}

// selfForbidden is 403 self_action_forbidden (selfAction) as an error.
type selfForbidden struct{ msg string }

func (e selfForbidden) Error() string { return e.msg }

// forbidSelfSessionAction refuses a session action from task id's own
// session: closing it can infer completion, which an agent never does for
// its own work (ADR 0011).
func (s *server) forbidSelfSessionAction(r *http.Request, id int64) error {
	_, own, err := s.callerOwns(r, id)
	if err != nil {
		return err
	}
	if own {
		return selfForbidden{"An agent cannot run actions on its own session. The user or another agent runs them."}
	}
	return nil
}

// respondRequest answers a pending request of task id's session; never
// from that session itself (selfAction).
func (s *server) respondRequest(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	if _, refused, err := s.selfAction(w, r, id, "An agent cannot answer its own session's request. The user answers it in Stage or in the harness."); err != nil || refused {
		return err
	}
	var in execution.Response
	if err := decodeStrict(r, &in); err != nil {
		return err
	}
	t, err := s.Engine.Coordinator.Respond(r.Context(), id, r.PathValue("rid"), in)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, s.task(t))
	return nil
}

// sendMessage sends task id's session a message; never from that session
// itself (selfAction).
func (s *server) sendMessage(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	if _, refused, err := s.selfAction(w, r, id, "An agent cannot message its own session."); err != nil || refused {
		return err
	}
	// The body is text only: the coordinator gives each call its request ID.
	var in struct {
		Text string `json:"text"`
	}
	if err := decodeStrict(r, &in); err != nil {
		return err
	}
	t, err := s.Engine.Coordinator.Send(r.Context(), id, execution.Message{Text: in.Text})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, s.task(t))
	return nil
}
