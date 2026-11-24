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

func (r attempts) BoundDestination(ctx context.Context, taskID int64) (store.LaunchDestination, error) {
	var b store.LaunchDestination
	err := r.QueryRow(ctx, `SELECT b.connection_id,b.external_project_id,b.workspace,b.id,b.revision FROM tasks t JOIN project_bindings b ON b.project_id=t.project_id AND b.connection_id=t.connection_id AND b.active=1 AND b.status='available' WHERE t.id=$1`, taskID).Scan(&b.ConnectionID, &b.ExternalProjectID, &b.Workspace, &b.BindingID, &b.BindingRevision)
	return b, notFound(err)
}

func (r attempts) PinDestination(ctx context.Context, taskID int64, n int, b store.LaunchDestination) error {
	_, err := r.Exec(ctx, `INSERT INTO task_launch_destinations(task_id,attempt,connection_id,external_project_id,workspace,binding_id,binding_revision) VALUES($1,$2,$3,$4,$5,$6,$7)`, taskID, n, b.ConnectionID, b.ExternalProjectID, b.Workspace, b.BindingID, b.BindingRevision)
	return err
}

func (r attempts) RecordBase(ctx context.Context, taskID int64, b store.LaunchBase, sourceID *int64) error {
	_, err := r.Exec(ctx, `
INSERT INTO task_launch_bases (task_id, attempt, base_ref, start_from_origin, source, source_task_id, source_title, reason, source_session_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (task_id, attempt) DO UPDATE SET
  base_ref = EXCLUDED.base_ref, start_from_origin = EXCLUDED.start_from_origin, source = EXCLUDED.source,
  source_task_id = EXCLUDED.source_task_id,
  source_title = EXCLUDED.source_title, reason = EXCLUDED.reason, source_session_id=excluded.source_session_id, created_at = `+nowSQL+``,
		taskID, b.Attempt, b.BaseRef, b.StartFromOrigin, b.Source, b.SourceTaskID, b.SourceTitle, b.Reason, sourceID)
	return err
}

func (r attempts) Base(ctx context.Context, taskID int64, attempt int) (*store.LaunchBase, error) {
	var b store.LaunchBase
	err := r.QueryRow(ctx, `
SELECT attempt, base_ref, start_from_origin, source, source_task_id, (SELECT external_id FROM session_identities WHERE id=task_launch_bases.source_session_id), source_title, reason, created_at, (SELECT json_object('connectionId',connection_id,'externalSessionId',external_id) FROM session_identities WHERE id=task_launch_bases.source_session_id)
FROM task_launch_bases WHERE task_id = $1 AND attempt = $2`, taskID, attempt).
		Scan(&b.Attempt, &b.BaseRef, &b.StartFromOrigin, &b.Source, &b.SourceTaskID, &b.SourceThreadID, &b.SourceTitle, &b.Reason, &b.At, &b.Session)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
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
SELECT attempt, status, source_task_id, COALESCE((SELECT external_id FROM session_identities WHERE id=task_handoffs.source_session_id),''), source_title, text, reason, created_at,(SELECT json_object('connectionId',connection_id,'externalSessionId',external_id) FROM session_identities WHERE id=task_handoffs.source_session_id)
FROM task_handoffs WHERE task_id = $1 AND attempt = $2 ORDER BY source_task_id, id`, taskID, attempt)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(r rowScanner) (store.Handoff, error) {
		var h store.Handoff
		err := r.Scan(&h.Attempt, &h.Status, &h.SourceTaskID, &h.SourceThreadID, &h.SourceTitle, &h.Text, &h.Reason, &h.At, &h.Session)
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

func (r threads) SetProject(ctx context.Context, threadID string, project *int64) error {
	_, err := r.Exec(ctx, `UPDATE threads SET stage_project_id = $2 WHERE t3_thread_id = $1`, threadID, project)
	return err
}

func (r threads) Mirror(ctx context.Context, o execution.Observation, m store.ThreadMirror) (bool, error) {
	var existed bool
	if err := r.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM threads WHERE t3_thread_id = $1)`, o.Session.ExternalSessionID).Scan(&existed); err != nil {
		return false, err
	}
	_, err := r.Exec(ctx, `INSERT INTO threads(t3_thread_id,t3_project_id,title,status,execution_state,archived,created_at,updated_at,synced_at,
   branch,worktree_path,pending_request_count,active_run_id,latest_run_id,run_started_at,run_ended_at,parent_thread_id,relationship,session_id,settled,settled_at)
   VALUES($1,$2,$3,$4,$4,$5,$6,$7,stage_now(),$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
   ON CONFLICT(t3_thread_id) DO UPDATE SET title=excluded.title,status=excluded.status,execution_state=excluded.execution_state,
   parent_thread_id=excluded.parent_thread_id,relationship=excluded.relationship,
   archived=excluded.archived,updated_at=excluded.updated_at,synced_at=stage_now(),
   branch=excluded.branch,worktree_path=excluded.worktree_path,pending_request_count=excluded.pending_request_count,
   active_run_id=excluded.active_run_id,latest_run_id=excluded.latest_run_id,run_started_at=excluded.run_started_at,run_ended_at=excluded.run_ended_at,
   settled=excluded.settled,settled_at=CASE WHEN excluded.settled THEN COALESCE(threads.settled_at,excluded.settled_at) END
   WHERE excluded.updated_at>=threads.updated_at`, o.Session.ExternalSessionID, o.Project, o.Title, o.State, o.Archived,
		o.CreatedAt, o.UpdatedAt, m.Branch, m.WorktreePath, m.PendingRequests, m.ActiveRunID, m.LatestRunID, o.StartedAt, o.EndedAt,
		m.ParentThreadID, m.Relationship, m.SessionID, m.Settled, m.SettledAt)
	if err != nil {
		return false, err
	}
	_, err = r.Exec(ctx, "UPDATE threads SET model=$2,runtime_mode=$3,interaction_mode=$4 WHERE session_id=$1", m.SessionID, m.Model, m.RuntimeMode, m.InteractionMode)
	return !existed, err
}

func (r threads) SetSessionLastWord(ctx context.Context, sessionID int64, checked time.Time, lw store.LastWordRow) error {
	_, err := r.Exec(ctx, `UPDATE threads SET last_word_text=$2,last_word_preview=$3,last_word_truncated=$4,last_word_run_id=$5,last_word_at=$6,last_word_checked_at=$7
    WHERE session_id=$1 AND (last_word_checked_at IS NULL OR last_word_checked_at<=$7)`, sessionID, lw.Text, lw.Preview, lw.Truncated, lw.RunID, lw.At, checked)
	return err
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
