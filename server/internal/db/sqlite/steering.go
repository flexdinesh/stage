package sqlite

import (
	"context"
	"encoding/json"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

type steering struct{ *runner }

func (t *tx) Steering() store.Steering { return steering{t.r} }

func (r steering) Active(ctx context.Context, treeID int64) (store.Steerer, error) {
	var s store.Steerer
	err := r.QueryRow(ctx, `SELECT id,tree_id,task_id FROM tree_steerers WHERE tree_id=$1 AND ended_at IS NULL AND task_id IS NOT NULL`, treeID).Scan(&s.ID, &s.TreeID, &s.TaskID)
	return s, notFound(err)
}

func (r steering) ActiveByTask(ctx context.Context, taskID int64) (store.Steerer, error) {
	var s store.Steerer
	err := r.QueryRow(ctx, `SELECT id,tree_id,task_id FROM tree_steerers WHERE task_id=$1 AND ended_at IS NULL`, taskID).Scan(&s.ID, &s.TreeID, &s.TaskID)
	return s, notFound(err)
}

func (r steering) ActiveForTrees(ctx context.Context, treeIDs []int64) (map[int64]store.TreeSteerer, error) {
	out := map[int64]store.TreeSteerer{}
	if len(treeIDs) == 0 {
		return out, nil
	}
	rows, err := r.Query(ctx, `SELECT s.tree_id,s.task_id,t.title,t.state,s.created_at,s.delivered_through,s.last_delivered_at,s.last_error,
 (SELECT count(*) FROM steering_events e WHERE e.steerer_id=s.id AND e.id>s.delivered_through AND (e.caused_by_task_id IS NULL OR e.caused_by_task_id<>s.task_id))
FROM tree_steerers s JOIN tasks t ON t.id=s.task_id
WHERE s.ended_at IS NULL AND s.tree_id IN (SELECT value FROM json_each($1))`, jsonArray(treeIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var tree int64
		var s store.TreeSteerer
		if err := rows.Scan(&tree, &s.TaskID, &s.Title, &s.State, &s.Since, &s.DeliveredThrough, &s.LastDeliveredAt, &s.LastError, &s.Pending); err != nil {
			return nil, err
		}
		out[tree] = s
	}
	return out, rows.Err()
}

func (r steering) Request(ctx context.Context, treeID int64, requestID string) (store.SteeringRequest, error) {
	var q store.SteeringRequest
	err := r.QueryRow(ctx, `SELECT id,task_id,request_digest FROM tree_steerers WHERE tree_id=$1 AND request_id=$2`, treeID, requestID).Scan(&q.SteererID, &q.TaskID, &q.Digest)
	return q, notFound(err)
}

func (r steering) Bind(ctx context.Context, treeID, taskID int64, requestID, digest string, a store.Attribution) (int64, error) {
	var id int64
	err := r.QueryRow(ctx, `INSERT INTO tree_steerers(tree_id,task_id,request_id,request_digest,created_at,attribution) VALUES($1,$2,$3,$4,`+nowSQL+`,$5) RETURNING id`,
		treeID, taskID, requestID, digest, a).Scan(&id)
	return id, err
}

func (r steering) End(ctx context.Context, id int64, reason store.SteeringEndReason) (bool, error) {
	res, err := r.Exec(ctx, `UPDATE tree_steerers SET ended_at=`+nowSQL+`, end_reason=$2 WHERE id=$1 AND ended_at IS NULL`, id, string(reason))
	return res.RowsAffected() == 1, err
}

func (steering) LockEvents(context.Context, int64) error { return nil }

func (r steering) InsertEvent(ctx context.Context, e store.NewSteeringEvent) (int64, error) {
	payload, err := json.Marshal(nonNilPayload(e.Payload))
	if err != nil {
		return 0, err
	}
	var conn *int64
	var ext *string
	if e.CausedBy != nil {
		conn, ext = &e.CausedBy.ConnectionID, &e.CausedBy.ExternalSessionID
	}
	var id int64
	err = r.QueryRow(ctx, `INSERT INTO steering_events(tree_id,steerer_id,kind,task_id,payload,caused_by_connection_id,caused_by_external_session_id,caused_by_task_id,created_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,`+nowSQL+`) RETURNING id`, e.TreeID, e.SteererID, e.Kind, e.TaskID, string(payload), conn, ext, e.CausedByTask).Scan(&id)
	return id, err
}

func nonNilPayload(p map[string]any) map[string]any {
	if p == nil {
		return map[string]any{}
	}
	return p
}

func (r steering) Events(ctx context.Context, treeID, after int64, limit int) ([]store.SteeringEvent, error) {
	rows, err := r.Query(ctx, `SELECT e.id,e.tree_id,e.steerer_id,e.kind,e.task_id,e.payload,e.caused_by_connection_id,e.caused_by_external_session_id,e.caused_by_task_id,
 COALESCE(e.caused_by_task_id IS NOT NULL AND e.caused_by_task_id=s.task_id,0),e.created_at
FROM steering_events e JOIN tree_steerers s ON s.id=e.steerer_id
WHERE e.tree_id=$1 AND e.id>$2 ORDER BY e.id LIMIT $3`, treeID, after, limit)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, scanSteeringEvent)
}

func scanSteeringEvent(row rowScanner) (store.SteeringEvent, error) {
	var e store.SteeringEvent
	var payload string
	var conn *int64
	var ext *string
	var causedTask *int64
	if err := row.Scan(&e.ID, &e.TreeID, &e.SteererID, &e.Kind, &e.TaskID, &payload, &conn, &ext, &causedTask, &e.Self, &e.CreatedAt); err != nil {
		return e, err
	}
	e.Payload = json.RawMessage(payload)
	e.CausedBy = steeringCause(conn, ext, causedTask)
	return e, nil
}

func steeringCause(conn *int64, ext *string, task *int64) *store.SteeringCause {
	if conn == nil && task == nil {
		return nil
	}
	c := &store.SteeringCause{TaskID: task}
	if conn != nil && ext != nil {
		c.Session = &store.SessionRef{ConnectionID: *conn, ExternalSessionID: *ext}
	}
	return c
}

func (r steering) LaunchBrief(ctx context.Context, taskID int64, attempt int) (string, error) {
	var brief string
	err := r.QueryRow(ctx, `SELECT brief FROM steering_launch_context WHERE task_id=$1 AND attempt=$2`, taskID, attempt).Scan(&brief)
	return brief, notFound(err)
}

func (r steering) SetLaunchBrief(ctx context.Context, taskID int64, attempt int, brief string) error {
	_, err := r.Exec(ctx, `INSERT INTO steering_launch_context(task_id,attempt,brief) VALUES($1,$2,$3)`, taskID, attempt, brief)
	return err
}
