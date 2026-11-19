package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/flexdinesh/stage/server/internal/execution"
	"time"
)

// PutObservation keeps normalized state scoped by connection. The T3 cache is
// maintained only for retained API compatibility, never as execution authority.
func (s *Store) PutObservation(ctx context.Context, o execution.Observation) error {
	if err := o.Valid(); err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *Tx) error {
		id, err := sessionIdentity(ctx, tx, o.Session)
		if err != nil {
			return err
		}
		var previous execution.Observation
		err = tx.QueryRow(ctx, "SELECT observation FROM execution_observations WHERE session_id=$1", id).Scan(&previous)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if previous.UpdatedAt.After(o.UpdatedAt) {
			return nil
		}
		if o.Reply == nil && (s.IsT3(o.Session.ConnectionID) || o.ExecutionID == previous.ExecutionID) {
			o.Reply = previous.Reply
		}
		raw, err := json.Marshal(o)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO execution_observations(session_id,observation) VALUES($1,$2)
   ON CONFLICT(session_id) DO UPDATE SET observation=excluded.observation,observed_at=stage_now(),unavailable_reason=''`, id, string(raw))
		if err != nil {
			return err
		}
		if !s.IsT3(o.Session.ConnectionID) {
			return nil
		}
		pending := 0
		if o.State == "needs_input" {
			pending = 1
		}
		active := ""
		if o.Active {
			active = o.ExecutionID
		}
		// settled_at keeps the first observed settle; T3's own settledAt is not carried.
		settled := o.Closed != nil && o.Closed.Signal == execution.ClosedSettled
		var settledAt *time.Time
		if settled {
			settledAt = &o.UpdatedAt
		}
		_, err = tx.Exec(ctx, `INSERT INTO threads(t3_thread_id,t3_project_id,title,status,execution_state,archived,link,created_at,updated_at,synced_at,
   branch,worktree_path,pending_request_count,active_run_id,latest_run_id,run_started_at,run_ended_at,parent_thread_id,relationship,session_id,settled,settled_at)
   VALUES($1,$2,$3,$4,$4,$5,$6,$7,$8,stage_now(),$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
   ON CONFLICT(t3_thread_id) DO UPDATE SET title=excluded.title,status=excluded.status,execution_state=excluded.execution_state,
   parent_thread_id=excluded.parent_thread_id,relationship=excluded.relationship,
   archived=excluded.archived,link=COALESCE(excluded.link,threads.link),updated_at=excluded.updated_at,synced_at=stage_now(),
   branch=excluded.branch,worktree_path=excluded.worktree_path,pending_request_count=excluded.pending_request_count,
   active_run_id=excluded.active_run_id,latest_run_id=excluded.latest_run_id,run_started_at=excluded.run_started_at,run_ended_at=excluded.run_ended_at,
   settled=excluded.settled,settled_at=CASE WHEN excluded.settled THEN COALESCE(threads.settled_at,excluded.settled_at) END
   WHERE excluded.updated_at>=threads.updated_at`, o.Session.ExternalSessionID, o.Project, o.Title, o.State, o.Archived, nullIfEmpty(o.Links.App),
			o.CreatedAt, o.UpdatedAt, nullIfEmpty(o.Branch), nullIfEmpty(o.Directory), pending, nullIfEmpty(active), nullIfEmpty(o.ExecutionID), o.StartedAt, o.EndedAt,
			externalParent(o), childMarker(o.Child), id, settled, settledAt)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "UPDATE threads SET model=$2,runtime_mode=$3,interaction_mode=$4 WHERE session_id=$1", id, nullIfEmpty(o.ModelLabel), nullIfEmpty(o.Config.Permission), nullIfEmpty(o.Config.Mode))
		if err != nil {
			return err
		}
		if o.Reply != nil {
			r := o.Reply
			_, err = tx.Exec(ctx, `UPDATE threads SET last_word_text=$2,last_word_preview=$3,last_word_truncated=$4,last_word_run_id=$5,last_word_item_id=$6,last_word_at=$7,last_word_checked_at=$8
    WHERE session_id=$1 AND (last_word_checked_at IS NULL OR last_word_checked_at<=$8)`, id, r.Text, PreviewText(r.Text), r.Truncated, nullIfEmpty(r.RunID), nullIfEmpty(r.ItemID), r.UpdatedAt, o.UpdatedAt)
		}
		return err
	})
}
func childMarker(child bool) *string {
	if child {
		v := "subagent"
		return &v
	}
	return nil
}
func (s *Store) Observation(ctx context.Context, ref execution.SessionRef) (execution.Observation, error) {
	var o execution.Observation
	err := s.pool.QueryRow(ctx, `SELECT observation FROM execution_observations WHERE session_id=(SELECT id FROM session_identities WHERE connection_id=$1 AND external_id=$2)`, ref.ConnectionID, ref.ExternalSessionID).Scan(&o)
	return o, notFound(err)
}
func (s *Store) ObservationUnavailable(ctx context.Context, ref execution.SessionRef, reason string) error {
	_, err := s.pool.Exec(ctx, `UPDATE execution_observations SET unavailable_reason=$3 WHERE session_id=(SELECT id FROM session_identities WHERE connection_id=$1 AND external_id=$2)`, ref.ConnectionID, ref.ExternalSessionID, reason)
	return err
}

type Attempt struct {
	TaskID     int64                `json:"taskId"`
	Number     int                  `json:"attempt"`
	Intent     *execution.Intent    `json:"-"`
	Checkpoint execution.Checkpoint `json:"-"`
	Phase      string               `json:"phase"`
	Revision   int64                `json:"revision"`
	Error      string               `json:"reason"`
	RetryAt    *time.Time           `json:"retryAt"`
	RetryCount int                  `json:"-"`
}

func (s *Store) Attempt(ctx context.Context, id int64, n int) (Attempt, error) {
	a := Attempt{TaskID: id, Number: n}
	err := s.pool.QueryRow(ctx, `SELECT intent,checkpoint,phase,revision,error,retry_at,retry_count FROM execution_attempts WHERE task_id=$1 AND attempt=$2`, id, n).Scan(&a.Intent, &a.Checkpoint, &a.Phase, &a.Revision, &a.Error, &a.RetryAt, &a.RetryCount)
	return a, notFound(err)
}
func (s *Store) PrepareAttempt(ctx context.Context, id int64, n int, in execution.Intent) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO execution_attempts(task_id,attempt,intent,phase) VALUES($1,$2,$3,'prepared') ON CONFLICT(task_id,attempt) DO UPDATE SET intent=excluded.intent WHERE execution_attempts.intent IS NULL AND execution_attempts.phase='prepared'`, id, n, string(raw))
	return err
}
func (s *Store) SaveCheckpoint(ctx context.Context, a *Attempt, c execution.Checkpoint) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	res, err := s.pool.Exec(ctx, `UPDATE execution_attempts SET checkpoint=$4,phase='unconfirmed',revision=revision+1 WHERE task_id=$1 AND attempt=$2 AND revision=$3 AND phase IN ('prepared','unconfirmed')`, a.TaskID, a.Number, a.Revision, string(raw))
	if err != nil {
		return err
	}
	if res.RowsAffected() != 1 {
		return Conflict("launch changed; reload the task")
	}
	a.Revision++
	a.Checkpoint = c
	a.Phase = "unconfirmed"
	return nil
}
func (s *Store) Unconfirmed(ctx context.Context, a Attempt, cause error) error {
	delay := 5 * time.Second
	for i := 0; i < a.RetryCount && delay < time.Minute; i++ {
		delay = min(delay*2, time.Minute)
	}
	_, err := s.pool.Exec(ctx, `UPDATE execution_attempts SET phase='unconfirmed',error=$4,retry_count=retry_count+1,retry_at=$5 WHERE task_id=$1 AND attempt=$2 AND revision=$3 AND phase IN ('prepared','unconfirmed')`, a.TaskID, a.Number, a.Revision, cause.Error(), time.Now().UTC().Add(delay))
	if err == nil {
		_, err = s.pool.Exec(ctx, "UPDATE tasks SET last_error=$3 WHERE id=$1 AND launch_attempt=$2 AND state='launching'", a.TaskID, a.Number, cause.Error())
	}
	return err
}
func (s *Store) RecoverAttempts(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO execution_attempts(task_id,attempt,phase,error)
 SELECT id,launch_attempt,'unconfirmed','Launch interrupted before its intent was recorded. Link the existing session.' FROM tasks WHERE state='launching' ON CONFLICT DO NOTHING`)
	return err
}

// FinishAttempt atomically attaches execution and closes recovery. User linking
// and automatic reconciliation use the same revision guard and ownership check.
func (s *Store) FinishAttempt(ctx context.Context, a Attempt, o execution.Observation, manual bool) error {
	if o.State == "idle" {
		if !s.IsT3(o.Session.ConnectionID) {
			return Conflict("Wait for the harness to report execution before linking this launch.")
		}
		o.State = "done"
	}
	if err := o.Valid(); err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *Tx) error {
		var connection int64
		var project, dir string
		err := tx.QueryRow(ctx, `SELECT connection_id,external_project_id,workspace FROM task_launch_destinations WHERE task_id=$1 AND attempt=$2`, a.TaskID, a.Number).Scan(&connection, &project, &dir)
		if err != nil {
			return err
		}
		if o.Session.ConnectionID != connection || o.Project != project {
			return Conflict("session belongs to another launch destination")
		}
		if o.Child || o.Archived {
			return Conflict("select an active top-level session")
		}
		sid, err := sessionIdentity(ctx, tx, o.Session)
		if err != nil {
			return err
		}
		var occupied bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE session_id=$1 AND id<>$2)`, sid, a.TaskID).Scan(&occupied); err != nil {
			return err
		}
		if occupied {
			return Conflict("session already belongs to another task")
		}
		res, err := tx.Exec(ctx, `UPDATE execution_attempts SET phase='confirmed',revision=revision+1,error='',retry_at=NULL WHERE task_id=$1 AND attempt=$2 AND revision=$3 AND phase IN ('prepared','unconfirmed')`, a.TaskID, a.Number, a.Revision)
		if err != nil {
			return err
		}
		if res.RowsAffected() != 1 {
			return Conflict("launch already changed; reload the preview")
		}
		var legacyID *string
		if s.IsT3(connection) {
			legacyID = &o.Session.ExternalSessionID
		}
		res, err = tx.Exec(ctx, `UPDATE tasks SET state=$3,t3_thread_id=$4,session_id=$5,launch_reuse=0,last_error=NULL,launched_at=COALESCE(launched_at,stage_now()),finished_at=CASE WHEN $3 IN ('done','failed','interrupted','cancelled') THEN COALESCE($6,stage_now()) ELSE NULL END,updated_at=stage_now() WHERE id=$1 AND launch_attempt=$2 AND state='launching'`, a.TaskID, a.Number, o.State, legacyID, sid, o.EndedAt)
		if err != nil {
			return err
		}
		if res.RowsAffected() != 1 {
			return Conflict("task no longer has this unresolved launch")
		}
		detail := "execution confirmed"
		if manual {
			detail = "existing session linked by user; no launch sent"
		}
		_, err = tx.Exec(ctx, `INSERT INTO task_events(task_id,from_state,to_state,detail) VALUES($1,'launching',$2,$3)`, a.TaskID, o.State, detail)
		return err
	})
}
func (s *Store) RejectAttempt(ctx context.Context, a Attempt, reason string) error {
	return s.inTx(ctx, func(tx *Tx) error {
		res, err := tx.Exec(ctx, `UPDATE execution_attempts SET phase='rejected',revision=revision+1,error=$4 WHERE task_id=$1 AND attempt=$2 AND revision=$3 AND phase IN ('prepared','unconfirmed')`, a.TaskID, a.Number, a.Revision, reason)
		if err != nil {
			return err
		}
		if res.RowsAffected() != 1 {
			return Conflict("launch changed")
		}
		_, err = tx.Exec(ctx, `UPDATE tasks SET state='backlog',launch_reuse=0,last_error=$3,scheduled_hold=1,updated_at=stage_now() WHERE id=$1 AND launch_attempt=$2 AND state='launching'`, a.TaskID, a.Number, reason)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO task_events(task_id,from_state,to_state,detail) VALUES($1,'launching','backlog',$2)`, a.TaskID, fmt.Sprintf("launch rejected before execution: %s", reason))
		return err
	})
}

func externalParent(o execution.Observation) *string {
	if o.Parent != nil {
		return &o.Parent.ExternalSessionID
	}
	return nil
}

// WatchedSessions retains normalized active observations after they leave the
// harness attention feed, so their eventual outcome remains observable.
func (s *Store) WatchedSessions(ctx context.Context, connection int64) ([]execution.SessionRef, error) {
	rows, err := s.pool.Query(ctx, `SELECT si.external_id FROM execution_observations o JOIN session_identities si ON si.id=o.session_id WHERE si.connection_id=$1 AND json_extract(o.observation,'$.child')=0 AND json_extract(o.observation,'$.state') IN ('running','needs_input','done')`, connection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := []execution.SessionRef{}
	for rows.Next() {
		ref := execution.SessionRef{ConnectionID: connection}
		if err := rows.Scan(&ref.ExternalSessionID); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}
