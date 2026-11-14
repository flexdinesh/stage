package db

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/flexdinesh/stage/server/internal/t3"
)

// SetPendingQuestionCount stores how many of a thread's pending requests are
// answerable questions (t3_pending_request list). Reads clamp it to
// pending_request_count, and a detail upsert with no pending requests resets it.
func (s *Store) SetPendingQuestionCount(ctx context.Context, threadID string, n int) error {
	_, err := s.pool.Exec(ctx, `UPDATE threads SET pending_question_count = $2 WHERE t3_thread_id = $1`, threadID, n)
	return err
}

// QuestionResponse is an answer stage sent to T3.
type QuestionResponse struct {
	ThreadID    string
	RequestID   string
	Answers     map[string]t3.Answer
	RespondedAt time.Time
}

// GetQuestionResponse returns ErrNotFound when stage never answered it.
func (s *Store) GetQuestionResponse(ctx context.Context, threadID, requestID string) (QuestionResponse, error) {
	r := QuestionResponse{ThreadID: threadID, RequestID: requestID}
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT answers, responded_at FROM question_responses
WHERE t3_thread_id = $1 AND request_id = $2`, threadID, requestID).Scan(&raw, &r.RespondedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(raw, &r.Answers); err != nil {
		return r, err
	}
	return r, nil
}

// RecordQuestionResponse records a sent answer (first write wins) and returns
// the stored row.
func (s *Store) RecordQuestionResponse(ctx context.Context, threadID, requestID string, answers map[string]t3.Answer) (QuestionResponse, error) {
	raw, err := json.Marshal(answers)
	if err != nil {
		return QuestionResponse{}, err
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO question_responses (t3_thread_id, request_id, answers)
VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, threadID, requestID, raw); err != nil {
		return QuestionResponse{}, err
	}
	return s.GetQuestionResponse(ctx, threadID, requestID)
}
