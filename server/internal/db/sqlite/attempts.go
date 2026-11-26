package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
)

type attempts struct{ *runner }

func (t *tx) Attempts() store.Attempts { return attempts{t.r} }

func (r attempts) Get(ctx context.Context, id int64, n int) (store.Attempt, error) {
	a := store.Attempt{TaskID: id, Number: n}
	err := r.QueryRow(ctx, `SELECT intent,checkpoint,phase,revision,error,retry_at,retry_count,observed_session FROM execution_attempts WHERE task_id=$1 AND attempt=$2`, id, n).Scan(&a.Intent, &a.Checkpoint, &a.Phase, &a.Revision, &a.Error, &a.RetryAt, &a.RetryCount, &a.Session)
	return a, notFound(err)
}

func (r attempts) Prepare(ctx context.Context, id int64, n int, in execution.Intent) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	_, err = r.Exec(ctx, `INSERT INTO execution_attempts(task_id,attempt,intent,phase) VALUES($1,$2,$3,'prepared') ON CONFLICT(task_id,attempt) DO UPDATE SET intent=excluded.intent WHERE execution_attempts.intent IS NULL AND execution_attempts.phase='prepared'`, id, n, string(raw))
	return err
}

func (r attempts) SaveCheckpoint(ctx context.Context, a store.Attempt, c execution.Checkpoint) (bool, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return false, err
	}
	res, err := r.Exec(ctx, `UPDATE execution_attempts SET checkpoint=$4,phase='unconfirmed',revision=revision+1 WHERE task_id=$1 AND attempt=$2 AND revision=$3 AND phase IN ('prepared','unconfirmed')`, a.TaskID, a.Number, a.Revision, string(raw))
	return err == nil && res.RowsAffected() == 1, err
}

func (r attempts) Unconfirmed(ctx context.Context, a store.Attempt, cause string, retryAt time.Time, seen *store.ObservedSession) (bool, error) {
	var observed *string
	if seen != nil {
		raw, err := json.Marshal(*seen)
		if err != nil {
			return false, err
		}
		v := string(raw)
		observed = &v
	}
	res, err := r.Exec(ctx, `UPDATE execution_attempts SET phase='unconfirmed',error=$4,retry_count=retry_count+1,retry_at=$5,observed_session=COALESCE($6,observed_session) WHERE task_id=$1 AND attempt=$2 AND revision=$3 AND phase IN ('prepared','unconfirmed')`, a.TaskID, a.Number, a.Revision, cause, retryAt, observed)
	return err == nil && res.RowsAffected() == 1, err
}

func (r attempts) Recover(ctx context.Context) error {
	_, err := r.Exec(ctx, `INSERT INTO execution_attempts(task_id,attempt,phase,error)
 SELECT id,launch_attempt,'unconfirmed','Launch interrupted before its intent was recorded. Link the existing session.' FROM tasks WHERE state='launching' ON CONFLICT DO NOTHING`)
	return err
}

func (r attempts) Confirm(ctx context.Context, a store.Attempt) (bool, error) {
	res, err := r.Exec(ctx, `UPDATE execution_attempts SET phase='confirmed',revision=revision+1,error='',retry_at=NULL WHERE task_id=$1 AND attempt=$2 AND revision=$3 AND phase IN ('prepared','unconfirmed')`, a.TaskID, a.Number, a.Revision)
	return err == nil && res.RowsAffected() == 1, err
}

func (r attempts) Reject(ctx context.Context, a store.Attempt, reason string) (bool, error) {
	res, err := r.Exec(ctx, `UPDATE execution_attempts SET phase='rejected',revision=revision+1,error=$4 WHERE task_id=$1 AND attempt=$2 AND revision=$3 AND phase IN ('prepared','unconfirmed')`, a.TaskID, a.Number, a.Revision, reason)
	return err == nil && res.RowsAffected() == 1, err
}

func (r attempts) Begin(ctx context.Context, taskID int64, n int) error {
	_, err := r.Exec(ctx, `INSERT INTO execution_attempts(task_id,attempt,phase) VALUES($1,$2,'prepared') ON CONFLICT DO NOTHING`, taskID, n)
	return err
}

func (r attempts) Claim(ctx context.Context, taskID int64, n int) (bool, error) {
	claim, err := r.Exec(ctx, `INSERT INTO task_launch_claims(task_id,attempt) VALUES($1,$2) ON CONFLICT DO NOTHING`, taskID, n)
	return err == nil && claim.RowsAffected() > 0, err
}

func (r attempts) PinInputs(ctx context.Context, taskID int64, n int) error {
	_, err := r.Exec(ctx, `INSERT INTO task_launch_inputs(task_id,attempt,source_task_id,acceptance_id)
    SELECT $1,$2,d.id,d.acceptance_id FROM task_needs n JOIN tasks d ON d.id=n.need_id
    WHERE n.task_id=$1 AND d.acceptance_id IS NOT NULL
    ON CONFLICT DO NOTHING`, taskID, n)
	return err
}

func (r attempts) Destination(ctx context.Context, taskID int64, n int) (store.LaunchDestination, error) {
	var b store.LaunchDestination
	err := r.QueryRow(ctx, `SELECT connection_id,external_project_id,workspace,binding_id,binding_revision FROM task_launch_destinations WHERE task_id=$1 AND attempt=$2`, taskID, n).Scan(&b.ConnectionID, &b.ExternalProjectID, &b.Workspace, &b.BindingID, &b.BindingRevision)
	return b, notFound(err)
}

func (r attempts) HasDestination(ctx context.Context, taskID int64, n int) (bool, error) {
	var exists bool
	err := r.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_launch_destinations WHERE task_id=$1 AND attempt=$2)`, taskID, n).Scan(&exists)
	return exists, err
}

// effectiveBindingSQL selects the chosen binding or, when unset, the active
// primary. The chosen binding must still belong to this project and connection.
func effectiveBindingSQL(owner string) string {
	return `b.project_id=` + owner + `.project_id AND b.connection_id=` + owner + `.connection_id
 AND b.active=1 AND b.status='available'
 AND b.id=COALESCE(` + owner + `.binding_id, (SELECT pb.id FROM project_bindings pb
 WHERE pb.project_id=` + owner + `.project_id AND pb.connection_id=` + owner + `.connection_id
 AND pb.active=1 AND pb.is_primary=1))`
}

func (r attempts) BoundDestination(ctx context.Context, taskID int64) (store.LaunchDestination, error) {
	var b store.LaunchDestination
	err := r.QueryRow(ctx, `SELECT b.connection_id,b.external_project_id,b.workspace,b.id,b.revision FROM tasks t JOIN project_bindings b ON `+effectiveBindingSQL("t")+` WHERE t.id=$1`, taskID).Scan(&b.ConnectionID, &b.ExternalProjectID, &b.Workspace, &b.BindingID, &b.BindingRevision)
	return b, notFound(err)
}

func (r attempts) PinDestination(ctx context.Context, taskID int64, n int, b store.LaunchDestination) error {
	_, err := r.Exec(ctx, `INSERT INTO task_launch_destinations(task_id,attempt,connection_id,external_project_id,workspace,binding_id,binding_revision) VALUES($1,$2,$3,$4,$5,$6,$7)`, taskID, n, b.ConnectionID, b.ExternalProjectID, b.Workspace, b.BindingID, b.BindingRevision)
	return err
}

func (r attempts) RecordHandoff(ctx context.Context, taskID int64, h store.Handoff, sourceID *int64) error {
	_, err := r.Exec(ctx, `
INSERT INTO task_handoffs (task_id, attempt, source_task_id, source_title, status, text, reason, source_session_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (task_id, attempt, source_task_id) DO UPDATE SET
  source_session_id = EXCLUDED.source_session_id,
  source_title = EXCLUDED.source_title, status = EXCLUDED.status, text = EXCLUDED.text,
  reason = EXCLUDED.reason, created_at = `+nowSQL+``,
		taskID, h.Attempt, h.SourceTaskID, h.SourceTitle, h.Status, h.Text, h.Reason, sourceID)
	return err
}

func (r attempts) Handoffs(ctx context.Context, taskID int64, attempt int) ([]store.Handoff, error) {
	rows, err := r.Query(ctx, `
SELECT attempt, status, source_task_id, source_title, text, reason, created_at,(SELECT json_object('connectionId',connection_id,'externalSessionId',external_id) FROM session_identities WHERE id=task_handoffs.source_session_id)
FROM task_handoffs WHERE task_id = $1 AND attempt = $2 ORDER BY source_task_id, id`, taskID, attempt)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(r rowScanner) (store.Handoff, error) {
		var h store.Handoff
		err := r.Scan(&h.Attempt, &h.Status, &h.SourceTaskID, &h.SourceTitle, &h.Text, &h.Reason, &h.At, &h.Session)
		return h, err
	})
}

type observations struct{ *runner }

func (t *tx) Observations() store.Observations { return observations{t.r} }

func (r observations) Get(ctx context.Context, sessionID int64) (execution.Observation, error) {
	var o execution.Observation
	err := r.QueryRow(ctx, "SELECT observation FROM execution_observations WHERE session_id=$1", sessionID).Scan(&o)
	return o, notFound(err)
}

func (r observations) ByRef(ctx context.Context, ref store.SessionRef) (execution.Observation, error) {
	var o execution.Observation
	err := r.QueryRow(ctx, `SELECT observation FROM execution_observations WHERE session_id=(SELECT id FROM session_identities WHERE connection_id=$1 AND external_id=$2)`, ref.ConnectionID, ref.ExternalSessionID).Scan(&o)
	return o, notFound(err)
}

func (r observations) Put(ctx context.Context, sessionID int64, o execution.Observation) error {
	raw, err := json.Marshal(o)
	if err != nil {
		return err
	}
	_, err = r.Exec(ctx, `INSERT INTO execution_observations(session_id,observation) VALUES($1,$2)
   ON CONFLICT(session_id) DO UPDATE SET observation=excluded.observation,observed_at=stage_now(),unavailable_reason=''`, sessionID, string(raw))
	return err
}

func (r observations) Unavailable(ctx context.Context, ref store.SessionRef, reason string) error {
	_, err := r.Exec(ctx, `UPDATE execution_observations SET unavailable_reason=$3 WHERE session_id=(SELECT id FROM session_identities WHERE connection_id=$1 AND external_id=$2)`, ref.ConnectionID, ref.ExternalSessionID, reason)
	return err
}

func (r observations) NeedsInputCount(ctx context.Context) (int, error) {
	var n int
	err := r.QueryRow(ctx, `
SELECT (SELECT count(*) FROM tasks WHERE state = 'needs_input')
  + (SELECT count(*) FROM execution_observations o WHERE json_extract(o.observation,'$.state')='needs_input'
     AND json_extract(o.observation,'$.child')=0 AND NOT EXISTS (SELECT 1 FROM tasks t WHERE t.session_id=o.session_id))`).Scan(&n)
	return n, err
}

func (r observations) Watched(ctx context.Context, connection int64) ([]store.SessionRef, error) {
	rows, err := r.Query(ctx, `SELECT si.external_id FROM execution_observations o JOIN session_identities si ON si.id=o.session_id WHERE si.connection_id=$1 AND json_extract(o.observation,'$.child')=0 AND json_extract(o.observation,'$.state') IN ('running','needs_input','done')`, connection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := []store.SessionRef{}
	for rows.Next() {
		ref := store.SessionRef{ConnectionID: connection}
		if err := rows.Scan(&ref.ExternalSessionID); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func (r projects) AdoptionTarget(ctx context.Context, connectionID int64, externalID string, workspace *string) (int64, bool, error) {
	q := "SELECT project_id FROM project_bindings WHERE active=1 AND status='available' AND connection_id=$1 AND external_project_id=$2"
	args := []any{connectionID, externalID}
	if workspace != nil {
		q += " AND workspace=$3"
		args = append(args, *workspace)
	}
	var id int64
	err := r.QueryRow(ctx, "SELECT min(project_id) FROM ("+q+") HAVING count(*)=1", args...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}
