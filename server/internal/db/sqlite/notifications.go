package sqlite

import (
	"context"
	"slices"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

type notifications struct{ *runner }

func (t *tx) Notifications() store.Notifications { return notifications{t.r} }

// The task URL survives ON DELETE SET NULL and restarts onto a different session.
// Titles are snapshots and cannot identify a task or an external session.
const notificationKeySQL = `CASE
  WHEN n.task_id IS NOT NULL THEN 'task:' || n.task_id
  WHEN n.tree_id IS NOT NULL THEN 'tree:' || n.tree_id
  WHEN n.schedule_id IS NOT NULL THEN 'schedule:' || n.schedule_id
  WHEN n.url GLOB '/tasks/[0-9]*' AND substr(n.url, 8) NOT GLOB '*[^0-9]*' THEN 'task:' || substr(n.url, 8)
  WHEN n.url GLOB '/trees/[0-9]*' AND substr(n.url, 8) NOT GLOB '*[^0-9]*' THEN 'tree:' || substr(n.url, 8)
  WHEN n.url GLOB '/schedules/[0-9]*' AND substr(n.url, 12) NOT GLOB '*[^0-9]*' THEN 'schedule:' || substr(n.url, 12)
  WHEN n.session_id IS NOT NULL THEN 'session:' || (SELECT connection_id || ':' || external_id FROM session_identities WHERE id=n.session_id)
  ELSE 'notification:' || n.id END`

const notificationColumns = `COALESCE((SELECT connection_id FROM session_identities WHERE id=COALESCE(tk.session_id,n.session_id)),0), n.id, n.event, n.task_id, n.tree_id, n.schedule_id, (SELECT external_id FROM session_identities WHERE id=COALESCE(tk.session_id,n.session_id)), n.title, n.project_title,
  n.url, n.created_at, CASE WHEN r.notification_id IS NULL THEN n.created_at ELSE r.read_at END AS read_at,
  CASE WHEN n.task_id IS NOT NULL THEN tk.state
       WHEN o.session_id IS NOT NULL THEN json_extract(o.observation,'$.state') END AS current_state, n.note`

const notificationJoins = `
LEFT JOIN tasks tk ON tk.id = n.task_id
LEFT JOIN execution_observations o ON o.session_id=n.session_id`

func (r notifications) Insert(ctx context.Context, n store.NewNotification, sid *int64) (int64, error) {
	var id int64
	err := r.QueryRow(ctx, `INSERT INTO notifications(event,task_id,title,project_title,url,session_id,created_at,note,tree_id,schedule_id) VALUES($1,$2,$3,$4,$5,$6,COALESCE($7,strftime('%Y-%m-%dT%H:%M:%f000Z','now')),$8,$9,$10) RETURNING id`,
		string(n.Event), n.TaskID, n.Title, n.ProjectTitle, n.URL, sid, n.ObservedAt, n.Note, n.TreeID, n.ScheduleID).Scan(&id)
	return id, err
}

func (r notifications) InsertRecipients(ctx context.Context, id, ownerID int64) error {
	_, err := r.Exec(ctx, `INSERT INTO notification_recipients(notification_id, user_id)
SELECT n.id, u.id FROM notifications n CROSS JOIN installation_access a CROSS JOIN users u
LEFT JOIN notification_preferences p ON p.user_id = u.id
WHERE n.id = $1 AND a.id = 1 AND (
  (a.mode = 'personal' AND u.id = $2) OR
  (a.mode <> 'personal' AND u.email IS NOT NULL AND u.status = 'active'
    AND (p.user_id IS NULL AND n.event <> 'schedule_started' OR n.event IN (SELECT value FROM json_each(p.notify_events))))
)`, id, ownerID)
	return err
}

func (r notifications) ForDelivery(ctx context.Context, userID, id int64) (store.Notification, error) {
	n, err := scanNotification(r.QueryRow(ctx, `SELECT `+notificationColumns+`
FROM notification_recipients r JOIN notifications n ON n.id = r.notification_id`+notificationJoins+`
WHERE r.user_id = $1 AND r.notification_id = $2 AND r.read_at IS NULL`, userID, id))
	return n, notFound(err)
}

func (r notifications) List(ctx context.Context, userID int64, limit int, beforeID *int64, key *string) ([]store.Notification, error) {
	rows, err := r.Query(ctx, `SELECT `+notificationColumns+`
FROM notifications n`+notificationJoins+`
LEFT JOIN notification_recipients r ON r.notification_id = n.id AND r.user_id = $4
WHERE ($2 IS NULL OR n.id < $2)
  AND ($3 IS NULL OR (`+notificationKeySQL+`) = $3)
ORDER BY n.id DESC
LIMIT $1`, limit, beforeID, key, userID)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, scanNotification)
}

func scanNotification(row rowScanner) (store.Notification, error) {
	var n store.Notification
	err := row.Scan(&n.ConnectionID, &n.ID, &n.Event, &n.TaskID, &n.TreeID, &n.ScheduleID, &n.ExternalSessionID, &n.Title, &n.ProjectTitle,
		&n.URL, &n.CreatedAt, &n.ReadAt, &n.CurrentState, &n.Note)
	return n, err
}

func (r notifications) After(ctx context.Context, userID, after int64, limit int, newest bool) ([]store.Notification, error) {
	order := "ASC"
	if newest {
		order = "DESC"
	}
	rows, err := r.Query(ctx, `SELECT `+notificationColumns+`
FROM notifications n`+notificationJoins+`
LEFT JOIN notification_recipients r ON r.notification_id = n.id AND r.user_id = $3
WHERE n.id > $1
ORDER BY n.id `+order+`
LIMIT $2`, after, limit, userID)
	if err != nil {
		return nil, err
	}
	out, err := collectRows(rows, scanNotification)
	if newest {
		slices.Reverse(out)
	}
	return out, err
}

const unreadNotificationCountSQL = `SELECT count(*) FROM notification_recipients WHERE user_id = $1 AND read_at IS NULL`

func (r notifications) ReadState(ctx context.Context, userID int64) (latestID int64, unread int, err error) {
	err = r.QueryRow(ctx, `SELECT COALESCE(max(id), 0), (`+unreadNotificationCountSQL+`) FROM notifications`, userID).Scan(&latestID, &unread)
	return
}

func (r notifications) Groups(ctx context.Context, userID int64, limit int, cursor *store.NotificationCursor, upToID int64) ([]store.NotificationGroup, error) {
	var waiting *bool
	var beforeID *int64
	if cursor != nil {
		waiting, beforeID = &cursor.Waiting, &cursor.ID
	}
	rows, err := r.Query(ctx, `
WITH keyed AS (
  SELECT n.id, r.notification_id IS NOT NULL AND r.read_at IS NULL AS unread, `+notificationKeySQL+` AS group_key
  FROM notifications n
  LEFT JOIN notification_recipients r ON r.notification_id = n.id AND r.user_id = $5
  WHERE n.id <= $4
), grouped AS (
  SELECT group_key, max(id) AS id, count(*) AS event_count,
    count(*) FILTER (WHERE unread) AS unread_count
  FROM keyed GROUP BY group_key
), resolved AS (
  SELECT `+notificationColumns+`, g.group_key, g.event_count, g.unread_count
  FROM grouped g JOIN notifications n ON n.id = g.id`+notificationJoins+`
  LEFT JOIN notification_recipients r ON r.notification_id = n.id AND r.user_id = $5
)
SELECT * FROM resolved
WHERE $2 IS NULL OR (COALESCE(current_state = 'needs_input', false), id) < ($2, $3)
ORDER BY COALESCE(current_state = 'needs_input', false) DESC, id DESC
LIMIT $1`, limit, waiting, beforeID, upToID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.NotificationGroup{}
	for rows.Next() {
		var g store.NotificationGroup
		if err := rows.Scan(&g.ConnectionID, &g.ID, &g.Event, &g.TaskID, &g.TreeID, &g.ScheduleID, &g.ExternalSessionID, &g.Title, &g.ProjectTitle,
			&g.URL, &g.CreatedAt, &g.ReadAt, &g.CurrentState, &g.Note, &g.Key, &g.EventCount, &g.UnreadCount); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (r notifications) UnreadCount(ctx context.Context, userID int64) (int, error) {
	return rowTo[int](r.QueryRow(ctx, unreadNotificationCountSQL, userID))
}

func (r notifications) MarkRead(ctx context.Context, userID, upTo int64) error {
	_, err := r.Exec(ctx, `UPDATE notification_recipients SET read_at = `+nowSQL+`
WHERE user_id = $1 AND read_at IS NULL AND notification_id <= $2`, userID, upTo)
	return err
}

func (r notifications) MarkGroupsRead(ctx context.Context, userID int64, groupKeys []string) error {
	_, err := r.Exec(ctx, `UPDATE notification_recipients AS r SET read_at = `+nowSQL+`
FROM notifications n
WHERE r.user_id = $1 AND r.read_at IS NULL AND n.id = r.notification_id
  AND (`+notificationKeySQL+`) IN (SELECT value FROM json_each($2))`, userID, jsonArray(groupKeys))
	return err
}

func (r notifications) UnreadKeys(ctx context.Context, userID int64) ([]string, error) {
	rows, err := r.Query(ctx, `SELECT DISTINCT `+notificationKeySQL+`
FROM notification_recipients r JOIN notifications n ON n.id = r.notification_id
WHERE r.user_id = $1 AND r.read_at IS NULL ORDER BY 1`, userID)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, rowTo[string])
}

func (r notifications) Prune(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.Exec(ctx, `DELETE FROM notifications WHERE created_at < $1`, before)
	return tag.RowsAffected(), err
}

func (r notifications) Waiting(ctx context.Context, upToID int64) ([]store.WaitingSession, error) {
	rows, err := r.Query(ctx, `SELECT connection_id, external_id, title, project_title, directory FROM (
 SELECT si.connection_id, si.external_id, json_extract(o.observation,'$.title') AS title, p.title AS project_title, NULLIF(json_extract(o.observation,'$.directory'),'') AS directory, o.observed_at AS updated_at
 FROM execution_observations o JOIN session_identities si ON si.id=o.session_id
 LEFT JOIN projects p ON p.id=(SELECT min(b.project_id) FROM project_bindings b WHERE b.active=1 AND b.connection_id=si.connection_id AND b.external_project_id=json_extract(o.observation,'$.project') HAVING count(*)=1)
 WHERE json_extract(o.observation,'$.state')='needs_input' AND json_extract(o.observation,'$.child')=0
 AND NOT EXISTS (SELECT 1 FROM tasks tk WHERE tk.session_id=o.session_id)
 AND NOT EXISTS (SELECT 1 FROM notifications n WHERE n.session_id=o.session_id AND n.id <= $1)
 ) ORDER BY updated_at DESC, connection_id, external_id`, upToID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.WaitingSession{}
	for rows.Next() {
		var row store.WaitingSession
		if err := rows.Scan(&row.ConnectionID, &row.ExternalSessionID, &row.Title, &row.ProjectTitle, &row.Directory); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
