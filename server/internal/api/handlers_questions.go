package api

import (
	"net/http"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// ---- pending questions (docs/api.md "Pending questions") ----

func (s *server) listQuestions(w http.ResponseWriter, r *http.Request) error {
	if !s.T3.Status(r.Context()).Connected {
		return t3.ErrNotConnected
	}
	out, err := s.Sync.PendingQuestions(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *server) answerQuestion(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Answers map[string]t3.Answer `json:"answers"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	threadID, requestID := r.PathValue("id"), strings.TrimSpace(r.PathValue("requestId"))
	if requestID == "" {
		return db.Invalid("requestId is required")
	}
	ctx := r.Context()
	if !s.T3.Status(ctx).Connected {
		return t3.ErrNotConnected
	}
	res, err := s.Sync.AnswerQuestion(ctx, threadID, requestID, body.Answers)
	if err != nil {
		return err
	}
	th, err := s.Store.GetThread(ctx, threadID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"requestId": res.RequestID, "respondedAt": res.RespondedAt, "thread": s.thread(s.envID(ctx), th)})
	return nil
}
