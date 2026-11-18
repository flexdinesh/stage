package db

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Notification is an inbox entry (docs/api.md "Notifications inbox").
type Notification struct {
	ConnectionID int64      `json:"connectionId"`
	ID           int64      `json:"id"`
	Event        TaskState  `json:"event"` // needs_input | ready | done | failed | settled
	TaskID       *int64     `json:"taskId"`
	T3ThreadID   *string    `json:"externalSessionId"` // external thread, or the task's thread
	Title        string     `json:"title"`
	ProjectTitle *string    `json:"projectTitle"`
	URL          string     `json:"url"`
	CreatedAt    time.Time  `json:"createdAt"`
	ReadAt       *time.Time `json:"readAt"`
	// CurrentState: the task's state, or for an external thread `settled`
	// when settled, else its observed state. nil = unknown (deleted/unobserved).
	CurrentState *TaskState `json:"currentState"`
	// PendingQuestionCount: answerable questions the task's or external
	// thread's session is waiting on now.
	PendingQuestionCount int `json:"-"`
}

// NewNotification is what the notifier records.
type NewNotification struct {
	Session      *SessionRef
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
  WHEN n.task_id IS NOT NULL THEN 'task:' || n.task_id
  WHEN n.url GLOB '/tasks/[0-9]*' AND substr(n.url, 8) NOT GLOB '*[^0-9]*' THEN 'task:' || substr(n.url, 8)
  WHEN n.session_id IS NOT NULL THEN 'thread:' || (SELECT connection_id || ':' || external_id FROM session_identities WHERE id=n.session_id)
 WHEN n.t3_thread_id IS NOT NULL THEN 'thread:1:' || n.t3_thread_id
  ELSE 'notification:' || n.id END`

// NotificationKey is notificationKeySQL for a loaded row. n.T3ThreadID is the
// task's current thread only when TaskID is set, where the task key wins.
func NotificationKey(n Notification) string {
	switch {
	case n.TaskID != nil:
		return "task:" + strconv.FormatInt(*n.TaskID, 10)
	case isTaskURL(n.URL):
		return "task:" + strings.TrimPrefix(n.URL, "/tasks/")
	case n.T3ThreadID != nil:
		connection := n.ConnectionID
		if connection == 0 {
			connection = T3ConnectionID
		}
		return "thread:" + strconv.FormatInt(connection, 10) + ":" + *n.T3ThreadID
	}
	return "notification:" + strconv.FormatInt(n.ID, 10)
}

// isTaskURL: `/tasks/` followed by one or more ASCII digits only.
func isTaskURL(u string) bool {
	id, ok := strings.CutPrefix(u, "/tasks/")
	if !ok || id == "" {
		return false
	}
	for _, c := range []byte(id) {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// TagForKey is a group key's push tag (`task:<id>` → `task-<id>`,
// `thread:<id>` → `thread-<id>`); identity-less keys have none ("").
func TagForKey(key string) string {
	if kind, id, ok := strings.Cut(key, ":"); ok && (kind == "task" || kind == "thread") {
		return kind + "-" + id
	}
	return ""
}

const notificationColumns = `COALESCE((SELECT connection_id FROM session_identities WHERE id=COALESCE(tk.session_id,n.session_id)),1), n.id, n.event, n.task_id, COALESCE((SELECT external_id FROM session_identities WHERE id=COALESCE(tk.session_id,n.session_id)),tk.t3_thread_id,n.t3_thread_id), n.title, n.project_title,
  n.url, n.created_at, n.read_at,
  CASE WHEN n.task_id IS NOT NULL THEN tk.state
       WHEN th.settled THEN 'settled'
       ELSE th.observed_state END AS current_state,
  COALESCE(min(COALESCE(tth.pending_question_count, th.pending_question_count),
    COALESCE(tth.pending_request_count, th.pending_request_count)), 0) AS pending_question_count`

const notificationJoins = `
LEFT JOIN tasks tk ON tk.id = n.task_id
LEFT JOIN threads tth ON tth.session_id = tk.session_id
LEFT JOIN threads th ON n.task_id IS NULL AND th.session_id = n.session_id`

// InsertNotification stores n and returns its id.
func (s *Store) InsertNotification(ctx context.Context, n NewNotification) (int64, error) {
	var id int64
	err := s.inTx(ctx, func(tx *Tx) error {
		var sid *int64
		ref := n.Session
		if ref == nil && n.T3ThreadID != nil {
			ref = &SessionRef{ConnectionID: T3ConnectionID, ExternalSessionID: *n.T3ThreadID}
		}
		if ref != nil {
			value, err := sessionIdentity(ctx, tx, *ref)
			if err != nil {
				return err
			}
			sid = &value
		}
		return tx.QueryRow(ctx, `INSERT INTO notifications(event,task_id,t3_thread_id,title,project_title,url,session_id) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
			string(n.Event), n.TaskID, n.T3ThreadID, n.Title, n.ProjectTitle, n.URL, sid).Scan(&id)
	})
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
WHERE ($2 IS NULL OR n.id < $2)
  AND ($3 IS NULL OR (`+notificationKeySQL+`) = $3)
ORDER BY n.id DESC
LIMIT $1`, limit, beforeID, key)
	if err != nil {
		return nil, err
	}
	return scanNotifications(rows)
}

func scanNotifications(rows *rowSet) ([]Notification, error) {
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ConnectionID, &n.ID, &n.Event, &n.TaskID, &n.T3ThreadID, &n.Title, &n.ProjectTitle,
			&n.URL, &n.CreatedAt, &n.ReadAt, &n.CurrentState, &n.PendingQuestionCount); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// NotificationsAfter returns up to limit entries with id > after in ascending
// order: the oldest ones, or with newest the newest ones.
func (s *Store) NotificationsAfter(ctx context.Context, after int64, limit int, newest bool) ([]Notification, error) {
	order := "ASC"
	if newest {
		order = "DESC"
	}
	rows, err := s.pool.Query(ctx, `SELECT `+notificationColumns+`
FROM notifications n`+notificationJoins+`
WHERE n.id > $1
ORDER BY n.id `+order+`
LIMIT $2`, after, limit)
	if err != nil {
		return nil, err
	}
	out, err := scanNotifications(rows)
	if newest {
		slices.Reverse(out)
	}
	return out, err
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
WHERE $2 IS NULL OR (COALESCE(current_state = 'needs_input', false), id) < ($2, $3)
ORDER BY COALESCE(current_state = 'needs_input', false) DESC, id DESC
LIMIT $1`, limit, waiting, beforeID, upToID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NotificationGroup{}
	for rows.Next() {
		var g NotificationGroup
		if err := rows.Scan(&g.ConnectionID, &g.ID, &g.Event, &g.TaskID, &g.T3ThreadID, &g.Title, &g.ProjectTitle,
			&g.URL, &g.CreatedAt, &g.ReadAt, &g.CurrentState, &g.PendingQuestionCount, &g.Key, &g.EventCount, &g.UnreadCount); err != nil {
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
	_, err := s.pool.Exec(ctx, `UPDATE notifications SET read_at = `+nowSQL+` WHERE read_at IS NULL AND id <= $1`, upTo)
	return err
}

// MarkNotificationGroupsRead marks every unread entry in the given groups read.
func (s *Store) MarkNotificationGroupsRead(ctx context.Context, groupKeys []string) error {
	_, err := s.pool.Exec(ctx, `UPDATE notifications AS n SET read_at = `+nowSQL+`
WHERE n.read_at IS NULL AND (`+notificationKeySQL+`) IN (SELECT value FROM json_each($1))`, jsonArray(groupKeys))
	return err
}

// UnreadNotificationTags returns the push tags (`task-<id>`, `thread-<id>`) of
// groups with unread entries. Identity-less groups have no tag.
func (s *Store) UnreadNotificationTags(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT `+notificationKeySQL+` FROM notifications n
WHERE n.read_at IS NULL ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tags := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		if tag := TagForKey(key); tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags, rows.Err()
}

// PruneNotifications deletes entries older than age.
func (s *Store) PruneNotifications(ctx context.Context, age time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM notifications WHERE created_at < $1`, time.Now().Add(-age))
	return tag.RowsAffected(), err
}

// WaitingSession is current external attention without a recorded inbox event.
// It has no event ID or read state: reading it cannot manufacture a notification.
type WaitingSession struct {
	ConnectionID int64   `json:"connectionId"`
	ThreadID     string  `json:"externalSessionId"`
	Title        string  `json:"title"`
	ProjectTitle *string `json:"projectTitle"`
}

func (s *Store) WaitingWithoutNotifications(ctx context.Context, upToID int64) ([]WaitingSession, error) {
	rows, err := s.pool.Query(ctx, `SELECT th.t3_thread_id, th.title, p.title
 FROM threads th LEFT JOIN projects p ON p.id = COALESCE(th.stage_project_id,(SELECT min(b.project_id) FROM project_bindings b WHERE b.connection_id=1 AND b.external_project_id=th.t3_project_id AND b.active=1 HAVING count(*)=1))
 LEFT JOIN tasks tk ON tk.session_id = th.session_id
 WHERE `+externalNeedsInput+` AND NOT EXISTS (
 SELECT 1 FROM notifications n WHERE n.session_id = th.session_id AND n.id <= $1)
 ORDER BY th.updated_at DESC, th.t3_thread_id`, upToID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WaitingSession{}
	for rows.Next() {
		row := WaitingSession{ConnectionID: T3ConnectionID}
		if err := rows.Scan(&row.ThreadID, &row.Title, &row.ProjectTitle); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
