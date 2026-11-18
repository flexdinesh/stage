package db

import (
	"context"
	"fmt"
	"time"
)

// Handoff statuses: sent (the block went out with the launch prompt) or
// skipped (the need's read failed; the task launched without its block).
const (
	HandoffSent    = "sent"
	HandoffSkipped = "skipped"
)

// Handoff is what one launch attempt appended to its prompt for one need
// (docs/api.md "Handoff").
type Handoff struct {
	Session        *SessionRef `json:"session,omitempty"`
	Attempt        int         `json:"attempt"`
	Status         string      `json:"status"`
	SourceTaskID   *int64      `json:"sourceTaskId"`
	SourceThreadID string      `json:"sourceThreadId"`
	SourceTitle    string      `json:"sourceTitle"`
	Text           string      `json:"text"`   // "" when skipped
	Reason         *string     `json:"reason"` // why skipped
	At             time.Time   `json:"at"`
}

// HandoffSource is a need a launch reads: a task (TaskID set; its current
// thread). Records from before trees may name a session (TaskID nil).
type HandoffSource struct {
	TaskID    *int64
	ThreadID  string
	Title     string
	SettledAt *time.Time // nil: finished (done) but not settled yet
}

// Label names the source in event details and the block: `#12` or
// `session "Title"`.
func (s HandoffSource) Label() string {
	if s.TaskID != nil {
		return fmt.Sprintf("#%d", *s.TaskID)
	}
	return fmt.Sprintf("session %q", s.Title)
}

// RecordHandoff stores attempt's handoff for one source (replacing an
// earlier record of the same attempt and source) and a launching task event
// describing it, in one transaction.
func (s *Store) RecordHandoff(ctx context.Context, taskID int64, h Handoff) error {
	detail := fmt.Sprintf("handoff from %s appended (%d chars)", sourceLabel(h), len([]rune(h.Text)))
	if h.Status == HandoffSkipped {
		reason := ""
		if h.Reason != nil {
			reason = *h.Reason
		}
		detail = fmt.Sprintf("handoff from %s skipped: %s", sourceLabel(h), reason)
	}
	return s.inTx(ctx, func(tx *Tx) error {
		var sourceID *int64
		if h.Session != nil {
			id, err := sessionIdentity(ctx, tx, *h.Session)
			if err != nil {
				return err
			}
			sourceID = &id
		}
		_, err := tx.Exec(ctx, `
INSERT INTO task_handoffs (task_id, attempt, source_task_id, source_thread_id, source_title, status, text, reason,source_session_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8,$9)
ON CONFLICT (task_id, attempt, source_task_id) DO UPDATE SET
  source_thread_id = EXCLUDED.source_thread_id,source_session_id=excluded.source_session_id,
  source_title = EXCLUDED.source_title, status = EXCLUDED.status, text = EXCLUDED.text,
  reason = EXCLUDED.reason, created_at = `+nowSQL+``,
			taskID, h.Attempt, h.SourceTaskID, h.SourceThreadID, h.SourceTitle, h.Status, h.Text, h.Reason, sourceID)
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

// GetHandoffs are attempt's handoff records, by source task id ([] when
// none).
func (s *Store) GetHandoffs(ctx context.Context, taskID int64, attempt int) ([]Handoff, error) {
	rows, err := s.pool.Query(ctx, `
SELECT attempt, status, source_task_id, source_thread_id, source_title, text, reason, created_at,(SELECT json_object('connectionId',connection_id,'externalSessionId',external_id) FROM session_identities WHERE id=task_handoffs.source_session_id)
FROM task_handoffs WHERE task_id = $1 AND attempt = $2 ORDER BY source_task_id, id`, taskID, attempt)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(r rowScanner) (Handoff, error) {
		var h Handoff
		err := r.Scan(&h.Attempt, &h.Status, &h.SourceTaskID, &h.SourceThreadID, &h.SourceTitle, &h.Text, &h.Reason, &h.At, &h.Session)
		return h, err
	})
}
