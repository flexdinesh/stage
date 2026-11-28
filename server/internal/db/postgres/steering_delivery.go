//go:build postgres

package postgres

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

// Steering delivery (the dispatcher's reads and writes, ADR 0013).

const steererStateCols = `id,tree_id,task_id,delivered_through,last_error,finish_event_id`

func scanSteererState(row rowScanner) (store.SteererState, error) {
	var s store.SteererState
	err := row.Scan(&s.ID, &s.TreeID, &s.TaskID, &s.DeliveredThrough, &s.LastError, &s.FinishEventID)
	return s, err
}

func (r steering) ActiveStates(ctx context.Context) ([]store.SteererState, error) {
	rows, err := r.Query(ctx, `SELECT `+steererStateCols+` FROM tree_steerers WHERE ended_at IS NULL AND task_id IS NOT NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, scanSteererState)
}

func (r steering) ActiveState(ctx context.Context, id int64) (store.SteererState, error) {
	s, err := scanSteererState(r.QueryRow(ctx, `SELECT `+steererStateCols+` FROM tree_steerers WHERE id=$1 AND ended_at IS NULL AND task_id IS NOT NULL`, id))
	return s, notFound(err)
}

func (r steering) Pending(ctx context.Context, id, after int64) (store.SteeringPending, error) {
	var p store.SteeringPending
	err := r.QueryRow(ctx, `SELECT count(*),COALESCE(min(e.id),0),COALESCE(max(e.id),0) FROM steering_events e JOIN tree_steerers s ON s.id=e.steerer_id
WHERE e.steerer_id=$1 AND e.id>$2 AND e.caused_by_task_id IS DISTINCT FROM s.task_id`, id, after).Scan(&p.Count, &p.FirstID, &p.LastID)
	return p, err
}

func (r steering) SetDelivered(ctx context.Context, id, through int64, delivered bool) (bool, error) {
	q := `UPDATE tree_steerers SET delivered_through=greatest(delivered_through,$2) WHERE id=$1 AND ended_at IS NULL`
	if delivered {
		q = `UPDATE tree_steerers SET delivered_through=greatest(delivered_through,$2),last_delivered_at=` + nowSQL + `,last_error=NULL WHERE id=$1 AND ended_at IS NULL`
	}
	res, err := r.Exec(ctx, q, id, through)
	if err != nil {
		return false, err
	}
	return res.RowsAffected() == 1, nil
}

func (r steering) SetError(ctx context.Context, id int64, msg *string) error {
	_, err := r.Exec(ctx, `UPDATE tree_steerers SET last_error=$2 WHERE id=$1 AND ended_at IS NULL`, id, msg)
	return err
}

func (r steering) MarkFinish(ctx context.Context, id, eventID int64) (bool, error) {
	res, err := r.Exec(ctx, `UPDATE tree_steerers SET finish_event_id=$2 WHERE id=$1 AND finish_event_id IS NULL`, id, eventID)
	if err != nil {
		return false, err
	}
	return res.RowsAffected() == 1, nil
}

func (r steering) SetFinalRevision(ctx context.Context, id, revision int64) error {
	_, err := r.Exec(ctx, `UPDATE tree_steerers SET final_state_revision=$2 WHERE id=$1`, id, revision)
	return err
}

func (r steering) AwaitingCompletion(ctx context.Context) ([]store.SteererCompletion, error) {
	rows, err := r.Query(ctx, `SELECT id,tree_id,task_id,final_state_revision FROM tree_steerers
WHERE end_reason='tree_finished' AND final_state_revision IS NOT NULL AND completion IS NULL AND task_id IS NOT NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(row rowScanner) (store.SteererCompletion, error) {
		var c store.SteererCompletion
		err := row.Scan(&c.ID, &c.TreeID, &c.TaskID, &c.FinalStateRevision)
		return c, err
	})
}

func (r steering) SetCompletion(ctx context.Context, id int64, completion string) (bool, error) {
	res, err := r.Exec(ctx, `UPDATE tree_steerers SET completion=$2 WHERE id=$1 AND completion IS NULL`, id, completion)
	if err != nil {
		return false, err
	}
	return res.RowsAffected() == 1, nil
}

func (r steering) MembersAccepted(ctx context.Context, treeID int64) (bool, error) {
	var ok bool
	err := r.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE tree_id=$1) AND NOT EXISTS(SELECT 1 FROM tasks WHERE tree_id=$1 AND acceptance_id IS NULL)`, treeID).Scan(&ok)
	return ok, err
}
