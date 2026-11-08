package db

import (
	"context"
	"time"
)

// Notification is an inbox entry (docs/api.md "Notifications inbox").
type Notification struct {
	ID           int64      `json:"id"`
	Event        TaskState  `json:"event"` // needs_input | done | failed
	TaskID       *int64     `json:"taskId"`
	T3ThreadID   *string    `json:"t3ThreadId"` // external thread, or the task's thread
	Title        string     `json:"title"`
	ProjectTitle *string    `json:"projectTitle"`
	URL          string     `json:"url"`
	CreatedAt    time.Time  `json:"createdAt"`
	ReadAt       *time.Time `json:"readAt"`
	// CurrentState: the task's state, or for an external thread `settled`
	// when settled, else its observed state. nil = unknown (deleted/unobserved).
	CurrentState *TaskState `json:"currentState"`
}

// NewNotification is what the notifier records.
type NewNotification struct {
	Event        TaskState
	TaskID       *int64
	T3ThreadID   *string
	Title        string
	ProjectTitle *string
	URL          string
}

// InsertNotification stores n and returns its id.
func (s *Store) InsertNotification(ctx context.Context, n NewNotification) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
INSERT INTO notifications (event, task_id, t3_thread_id, title, project_title, url)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		string(n.Event), n.TaskID, n.T3ThreadID, n.Title, n.ProjectTitle, n.URL).Scan(&id)
	return id, err
}

// ListNotifications returns up to limit entries, newest first, with id <
// beforeID when beforeID is set.
func (s *Store) ListNotifications(ctx context.Context, limit int, beforeID *int64) ([]Notification, error) {
	rows, err := s.pool.Query(ctx, `
SELECT n.id, n.event, n.task_id, COALESCE(tk.t3_thread_id, n.t3_thread_id), n.title, n.project_title,
  n.url, n.created_at, n.read_at,
  CASE WHEN n.task_id IS NOT NULL THEN tk.state
       WHEN th.settled THEN 'settled'
       ELSE th.observed_state END
FROM notifications n
LEFT JOIN tasks tk ON tk.id = n.task_id
LEFT JOIN threads th ON n.task_id IS NULL AND th.t3_thread_id = n.t3_thread_id
WHERE $2::bigint IS NULL OR n.id < $2
ORDER BY n.id DESC
LIMIT $1`, limit, beforeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.Event, &n.TaskID, &n.T3ThreadID, &n.Title, &n.ProjectTitle,
			&n.URL, &n.CreatedAt, &n.ReadAt, &n.CurrentState); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// UnreadNotificationCount counts entries never read.
func (s *Store) UnreadNotificationCount(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE read_at IS NULL`).Scan(&n)
	return n, err
}

// MarkNotificationsRead marks every unread entry with id <= upTo as read.
func (s *Store) MarkNotificationsRead(ctx context.Context, upTo int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE notifications SET read_at = now() WHERE read_at IS NULL AND id <= $1`, upTo)
	return err
}

// PruneNotifications deletes entries older than age.
func (s *Store) PruneNotifications(ctx context.Context, age time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM notifications WHERE created_at < $1`, time.Now().Add(-age))
	return tag.RowsAffected(), err
}
