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

// NotificationGroup summarizes all retained notifications for one task/thread.
type NotificationGroup struct {
	Notification
	Key         string `json:"key"`
	EventCount  int    `json:"eventCount"`
	UnreadCount int    `json:"unreadCount"`
}

// NotificationCursor follows priority (currently waiting first), then newest ID.
type NotificationCursor struct {
	Waiting bool
	ID      int64
}

// The task URL survives ON DELETE SET NULL and restarts onto a different thread.
// Titles are snapshots and cannot identify a task or an external thread.
const notificationKeySQL = `CASE
  WHEN n.task_id IS NOT NULL THEN 'task:' || n.task_id::text
  WHEN n.url ~ '^/tasks/[0-9]+$' THEN 'task:' || substring(n.url FROM '[0-9]+$')
  WHEN n.t3_thread_id IS NOT NULL THEN 'thread:' || n.t3_thread_id
  ELSE 'notification:' || n.id::text END`

const notificationColumns = `n.id, n.event, n.task_id, COALESCE(tk.t3_thread_id, n.t3_thread_id), n.title, n.project_title,
  n.url, n.created_at, n.read_at,
  CASE WHEN n.task_id IS NOT NULL THEN tk.state
       WHEN th.settled THEN 'settled'
       ELSE th.observed_state END AS current_state`

const notificationJoins = `
LEFT JOIN tasks tk ON tk.id = n.task_id
LEFT JOIN threads th ON n.task_id IS NULL AND th.t3_thread_id = n.t3_thread_id`

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
	return s.listNotificationEvents(ctx, limit, beforeID, nil)
}

// ListNotificationHistory pages only events belonging to key.
func (s *Store) ListNotificationHistory(ctx context.Context, key string, limit int, beforeID *int64) ([]Notification, error) {
	return s.listNotificationEvents(ctx, limit, beforeID, &key)
}

func (s *Store) listNotificationEvents(ctx context.Context, limit int, beforeID *int64, key *string) ([]Notification, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+notificationColumns+`
FROM notifications n`+notificationJoins+`
WHERE ($2::bigint IS NULL OR n.id < $2)
  AND ($3::text IS NULL OR (`+notificationKeySQL+`) = $3)
ORDER BY n.id DESC
LIMIT $1`, limit, beforeID, key)
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

// NotificationReadState captures the read boundary before listing groups.
func (s *Store) NotificationReadState(ctx context.Context) (latestID int64, unread int, err error) {
	err = s.pool.QueryRow(ctx, `SELECT COALESCE(max(id), 0), count(*) FILTER (WHERE read_at IS NULL) FROM notifications`).Scan(&latestID, &unread)
	return
}

// ListNotificationGroups aggregates before applying the cursor/limit. upToID
// prevents a racing insert from being included in the initial read boundary.
func (s *Store) ListNotificationGroups(ctx context.Context, limit int, cursor *NotificationCursor, upToID int64) ([]NotificationGroup, error) {
	var waiting *bool
	var beforeID *int64
	if cursor != nil {
		waiting, beforeID = &cursor.Waiting, &cursor.ID
	}
	rows, err := s.pool.Query(ctx, `
WITH keyed AS (
  SELECT n.id, n.read_at, `+notificationKeySQL+` AS group_key
  FROM notifications n WHERE n.id <= $4
), grouped AS (
  SELECT group_key, max(id) AS id, count(*) AS event_count,
    count(*) FILTER (WHERE read_at IS NULL) AS unread_count
  FROM keyed GROUP BY group_key
), resolved AS (
  SELECT `+notificationColumns+`, g.group_key, g.event_count, g.unread_count
  FROM grouped g JOIN notifications n ON n.id = g.id`+notificationJoins+`
)
SELECT * FROM resolved
WHERE $2::boolean IS NULL OR (COALESCE(current_state = 'needs_input', false), id) < ($2::boolean, $3::bigint)
ORDER BY COALESCE(current_state = 'needs_input', false) DESC, id DESC
LIMIT $1`, limit, waiting, beforeID, upToID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NotificationGroup{}
	for rows.Next() {
		var g NotificationGroup
		if err := rows.Scan(&g.ID, &g.Event, &g.TaskID, &g.T3ThreadID, &g.Title, &g.ProjectTitle,
			&g.URL, &g.CreatedAt, &g.ReadAt, &g.CurrentState, &g.Key, &g.EventCount, &g.UnreadCount); err != nil {
			return nil, err
		}
		out = append(out, g)
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
