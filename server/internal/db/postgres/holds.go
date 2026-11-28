//go:build postgres

package postgres

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

type holds struct{ *runner }

func (t *tx) Holds() store.Holds { return holds{t.r} }

const holdColumns = `h.id,h.task_id,h.state_revision,h.steerer_id,h.held_at,h.hold_until,h.state`

func scanHold(row rowScanner) (store.Hold, error) {
	var h store.Hold
	err := row.Scan(&h.ID, &h.TaskID, &h.StateRevision, &h.SteererID, &h.HeldAt, &h.Until, &h.State)
	return h, notFound(err)
}

func (r holds) Insert(ctx context.Context, h store.NewHold) (int64, error) {
	var id int64
	err := r.QueryRow(ctx, `INSERT INTO notification_holds(task_id,state_revision,steerer_id,held_at,hold_until,attribution) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`,
		h.TaskID, h.StateRevision, h.SteererID, h.HeldAt, h.Until, h.Attribution).Scan(&id)
	return id, err
}

func (r holds) Get(ctx context.Context, id int64) (store.Hold, error) {
	return scanHold(r.QueryRow(ctx, `SELECT `+holdColumns+` FROM notification_holds h WHERE h.id=$1`, id))
}

func (r holds) HeldForTask(ctx context.Context, taskID int64) (store.Hold, error) {
	return scanHold(r.QueryRow(ctx, `SELECT `+holdColumns+` FROM notification_holds h WHERE h.task_id=$1 AND h.state='held'`, taskID))
}

func (r holds) Held(ctx context.Context) ([]store.HeldHold, error) {
	rows, err := r.Query(ctx, `SELECT `+holdColumns+`,t.state,t.state_revision,t.answered_revision,t.tree_id,t.connection_id,
 s.tree_id,s.ended_at IS NOT NULL,s.task_id,st.state
FROM notification_holds h JOIN tasks t ON t.id=h.task_id JOIN tree_steerers s ON s.id=h.steerer_id LEFT JOIN tasks st ON st.id=s.task_id
WHERE h.state='held' ORDER BY h.id`)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(row rowScanner) (store.HeldHold, error) {
		var h store.HeldHold
		err := row.Scan(&h.ID, &h.TaskID, &h.StateRevision, &h.SteererID, &h.HeldAt, &h.Until, &h.State,
			&h.TaskState, &h.TaskRevision, &h.AnsweredRevision, &h.TaskTreeID, &h.ConnectionID,
			&h.SteererTreeID, &h.SteererEnded, &h.SteererTaskID, &h.SteererState)
		return h, err
	})
}

func (r holds) Finish(ctx context.Context, id int64, state store.HoldState, reason *string, notificationID *int64) (bool, error) {
	res, err := r.Exec(ctx, `UPDATE notification_holds SET state=$2,reason=$3::text,notification_id=$4::bigint WHERE id=$1 AND state='held'`, id, string(state), reason, notificationID)
	return res.RowsAffected() == 1, err
}

func (r holds) CancelTask(ctx context.Context, taskID int64, reason string, revision *int64) error {
	_, err := r.Exec(ctx, `UPDATE notification_holds SET state='cancelled',reason=$2 WHERE task_id=$1 AND state='held' AND ($3::bigint IS NULL OR state_revision=$3::bigint)`, taskID, reason, revision)
	return err
}
