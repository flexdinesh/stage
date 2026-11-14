package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Handoff statuses: sent (the block went out with the launch prompt) or
// skipped (the predecessor read failed; the task launched without it).
const (
	HandoffSent    = "sent"
	HandoffSkipped = "skipped"
)

// Handoff is what one launch attempt of a linked follower appended to its
// prompt (docs/api.md "Handoff").
type Handoff struct {
	Attempt        int       `json:"attempt"`
	Status         string    `json:"status"`
	SourceTaskID   *int64    `json:"sourceTaskId"`
	SourceThreadID string    `json:"sourceThreadId"`
	SourceTitle    string    `json:"sourceTitle"`
	Text           string    `json:"text"`   // "" when skipped
	Reason         *string   `json:"reason"` // why skipped
	At             time.Time `json:"at"`
}

// HandoffSource is a follower's settled predecessor: a task (TaskID set; its
// current thread) or a session.
type HandoffSource struct {
	TaskID    *int64
	ThreadID  string
	Title     string
	SettledAt *time.Time
}

// Label names the source in event details and the block: `#12` or
// `session "Title"`.
func (s HandoffSource) Label() string {
	if s.TaskID != nil {
		return fmt.Sprintf("#%d", *s.TaskID)
	}
	return fmt.Sprintf("session %q", s.Title)
}

// HandoffSourceFor is task's settled predecessor, nil when the task is not
// linked, its predecessor has not passed (settled task / threadPassedSQL
// thread), or the predecessor task has no thread.
func (s *Store) HandoffSourceFor(ctx context.Context, task Task) (*HandoffSource, error) {
	var (
		src HandoffSource
		err error
	)
	switch {
	case task.AfterTaskID != nil:
		var thread *string
		err = s.pool.QueryRow(ctx, `
SELECT p.id, p.t3_thread_id, p.title, p.settled_at FROM tasks p
WHERE p.id = $1 AND p.state = 'settled'`, *task.AfterTaskID).Scan(&src.TaskID, &thread, &src.Title, &src.SettledAt)
		if err == nil && thread == nil {
			return nil, nil
		}
		if thread != nil {
			src.ThreadID = *thread
		}
	case task.AfterThreadID != nil:
		err = s.pool.QueryRow(ctx, `
SELECT th.t3_thread_id, th.title, th.settled_at FROM threads th
WHERE th.t3_thread_id = $1 AND `+threadPassedSQL, *task.AfterThreadID).Scan(&src.ThreadID, &src.Title, &src.SettledAt)
	default:
		return nil, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &src, nil
}

// RecordHandoff stores attempt's handoff (replacing an earlier record of the
// same attempt) and a launching task event describing it, in one transaction.
func (s *Store) RecordHandoff(ctx context.Context, taskID int64, h Handoff) error {
	detail := fmt.Sprintf("handoff from %s appended (%d chars)", sourceLabel(h), len([]rune(h.Text)))
	if h.Status == HandoffSkipped {
		reason := ""
		if h.Reason != nil {
			reason = *h.Reason
		}
		detail = "handoff skipped: " + reason
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
INSERT INTO task_handoffs (task_id, attempt, source_task_id, source_thread_id, source_title, status, text, reason)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (task_id, attempt) DO UPDATE SET
  source_task_id = EXCLUDED.source_task_id, source_thread_id = EXCLUDED.source_thread_id,
  source_title = EXCLUDED.source_title, status = EXCLUDED.status, text = EXCLUDED.text,
  reason = EXCLUDED.reason, created_at = now()`,
			taskID, h.Attempt, h.SourceTaskID, h.SourceThreadID, h.SourceTitle, h.Status, h.Text, h.Reason)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO task_events (task_id, from_state, to_state, detail) VALUES ($1, 'launching', 'launching', $2)`,
			taskID, detail)
		return err
	})
}

func sourceLabel(h Handoff) string {
	return HandoffSource{TaskID: h.SourceTaskID, Title: h.SourceTitle}.Label()
}

// GetHandoff is attempt's handoff record; nil when none was recorded.
func (s *Store) GetHandoff(ctx context.Context, taskID int64, attempt int) (*Handoff, error) {
	var h Handoff
	err := s.pool.QueryRow(ctx, `
SELECT attempt, status, source_task_id, source_thread_id, source_title, text, reason, created_at
FROM task_handoffs WHERE task_id = $1 AND attempt = $2`, taskID, attempt).
		Scan(&h.Attempt, &h.Status, &h.SourceTaskID, &h.SourceThreadID, &h.SourceTitle, &h.Text, &h.Reason, &h.At)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &h, nil
}
