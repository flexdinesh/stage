package db

import (
	"context"
)

// SetPendingQuestionCount stores how many of a thread's pending requests are
// answerable questions (t3_pending_request list). Reads clamp it to
// pending_request_count, and a detail upsert with no pending requests resets it.
func (s *Store) SetPendingQuestionCount(ctx context.Context, threadID string, n int) error {
	_, err := s.pool.Exec(ctx, `UPDATE threads SET pending_question_count = $2 WHERE t3_thread_id = $1`, threadID, n)
	return err
}
