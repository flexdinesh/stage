//go:build postgres

package postgres

import (
	"context"
	"slices"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

type notifications struct{ *runner }

func (t *tx) Notifications() store.Notifications { return notifications{t.r} }

// The task URL survives ON DELETE SET NULL and restarts onto a different thread.
// Titles are snapshots and cannot identify a task or an external thread.
const notificationKeySQL = `CASE
  WHEN n.task_id IS NOT NULL THEN 'task:' || n.task_id
  WHEN n.url ~ '^/tasks/[0-9]+$' THEN 'task:' || substr(n.url, 8)
  WHEN n.session_id IS NOT NULL THEN 'thread:' || (SELECT connection_id || ':' || external_id FROM session_identities WHERE id=n.session_id)
  ELSE 'notification:' || n.id END`

const notificationColumns = `COALESCE((SELECT connection_id FROM session_identities WHERE id=COALESCE(tk.session_id,n.session_id)),1), n.id, n.event, n.task_id, (SELECT external_id FROM session_identities WHERE id=COALESCE(tk.session_id,n.session_id)), n.title, n.project_title,
  n.url, n.created_at, n.read_at,
  CASE WHEN n.task_id IS NOT NULL THEN tk.state
       WHEN o.session_id IS NOT NULL THEN o.observation->>'state'
       WHEN th.settled THEN 'settled'
       ELSE th.observed_state END AS current_state`

const notificationJoins = `
LEFT JOIN tasks tk ON tk.id = n.task_id
LEFT JOIN threads th ON n.task_id IS NULL AND th.session_id = n.session_id
LEFT JOIN execution_observations o ON o.session_id=n.session_id`

func (r notifications) Insert(ctx context.Context, n store.NewNotification, sid *int64) (int64, error) {
	var id int64
	err := r.QueryRow(ctx, `INSERT INTO notifications(event,task_id,title,project_title,url,session_id) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`,
		string(n.Event), n.TaskID, n.Title, n.ProjectTitle, n.URL, sid).Scan(&id)
	return id, err
}

func (r notifications) List(ctx context.Context, limit int, beforeID *int64, key *string) ([]store.Notification, error) {
	rows, err := r.Query(ctx, `SELECT `+notificationColumns+`
FROM notifications n`+notificationJoins+`
WHERE ($2::bigint IS NULL OR n.id < $2::bigint)
  AND ($3::text IS NULL OR (`+notificationKeySQL+`) = $3::text)
ORDER BY n.id DESC
LIMIT $1`, limit, beforeID, key)
	if err != nil {
		return nil, err
	}
	return scanNotifications(rows)
}

func scanNotifications(rows *rowSet) ([]store.Notification, error) {
	defer rows.Close()
	out := []store.Notification{}
	for rows.Next() {
		var n store.Notification
		if err := rows.Scan(&n.ConnectionID, &n.ID, &n.Event, &n.TaskID, &n.T3ThreadID, &n.Title, &n.ProjectTitle,
			&n.URL, &n.CreatedAt, &n.ReadAt, &n.CurrentState); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (r notifications) After(ctx context.Context, after int64, limit int, newest bool) ([]store.Notification, error) {
	order := "ASC"
	if newest {
		order = "DESC"
	}
	rows, err := r.Query(ctx, `SELECT `+notificationColumns+`
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

func (r notifications) ReadState(ctx context.Context) (latestID int64, unread int, err error) {
	err = r.QueryRow(ctx, `SELECT COALESCE(max(id), 0), count(*) FILTER (WHERE read_at IS NULL) FROM notifications`).Scan(&latestID, &unread)
	return
}

func (r notifications) Groups(ctx context.Context, limit int, cursor *store.NotificationCursor, upToID int64) ([]store.NotificationGroup, error) {
	var waiting *bool
	var beforeID *int64
	if cursor != nil {
		waiting, beforeID = &cursor.Waiting, &cursor.ID
	}
	rows, err := r.Query(ctx, `
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
	out := []store.NotificationGroup{}
	for rows.Next() {
		var g store.NotificationGroup
		if err := rows.Scan(&g.ConnectionID, &g.ID, &g.Event, &g.TaskID, &g.T3ThreadID, &g.Title, &g.ProjectTitle,
			&g.URL, &g.CreatedAt, &g.ReadAt, &g.CurrentState, &g.Key, &g.EventCount, &g.UnreadCount); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (r notifications) UnreadCount(ctx context.Context) (int, error) {
	var n int
	err := r.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE read_at IS NULL`).Scan(&n)
	return n, err
}

func (r notifications) MarkRead(ctx context.Context, upTo int64) error {
	_, err := r.Exec(ctx, `UPDATE notifications SET read_at = `+nowSQL+` WHERE read_at IS NULL AND id <= $1`, upTo)
	return err
}

func (r notifications) MarkGroupsRead(ctx context.Context, groupKeys []string) error {
	_, err := r.Exec(ctx, `UPDATE notifications AS n SET read_at = `+nowSQL+`
WHERE n.read_at IS NULL AND (`+notificationKeySQL+`) = ANY($1::text[])`, groupKeys)
	return err
}

func (r notifications) UnreadKeys(ctx context.Context) ([]string, error) {
	rows, err := r.Query(ctx, `SELECT DISTINCT `+notificationKeySQL+` FROM notifications n
WHERE n.read_at IS NULL ORDER BY 1`)
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
	rows, err := r.Query(ctx, `SELECT connection_id, external_id, title, project_title FROM (
 SELECT 1 AS connection_id, th.t3_thread_id AS external_id, th.title, p.title AS project_title, th.updated_at AS updated_at
 FROM threads th LEFT JOIN projects p ON p.id = COALESCE(th.stage_project_id,(SELECT min(b.project_id) FROM project_bindings b WHERE b.connection_id=COALESCE((SELECT connection_id FROM session_identities WHERE id=th.session_id),`+legacyT3SQL+`) AND b.external_project_id=th.t3_project_id AND b.active HAVING count(*)=1))
 LEFT JOIN tasks tk ON tk.session_id = th.session_id
 WHERE `+externalNeedsInput+` AND NOT EXISTS (
 SELECT 1 FROM notifications n WHERE n.session_id = th.session_id AND n.id <= $1)
 UNION ALL
 SELECT si.connection_id, si.external_id, o.observation->>'title', p.title, o.observed_at
 FROM execution_observations o JOIN session_identities si ON si.id=o.session_id
 LEFT JOIN projects p ON p.id=(SELECT min(b.project_id) FROM project_bindings b WHERE b.active AND b.connection_id=si.connection_id AND b.external_project_id=o.observation->>'project' HAVING count(*)=1)
 WHERE NOT EXISTS (SELECT 1 FROM threads th WHERE th.session_id=o.session_id)
 AND o.observation->>'state'='needs_input' AND o.observation->'child'='false'::jsonb
 AND NOT EXISTS (SELECT 1 FROM tasks tk WHERE tk.session_id=o.session_id)
 AND NOT EXISTS (SELECT 1 FROM notifications n WHERE n.session_id=o.session_id AND n.id <= $1)
 ) AS w ORDER BY updated_at DESC, connection_id, external_id`, upToID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.WaitingSession{}
	for rows.Next() {
		var row store.WaitingSession
		if err := rows.Scan(&row.ConnectionID, &row.ThreadID, &row.Title, &row.ProjectTitle); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
