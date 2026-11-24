package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
)

type acceptances struct{ *runner }

func (t *tx) Acceptances() store.Acceptances { return acceptances{t.r} }

const acceptanceColumns = `a.id,a.task_id,a.accepted_at,a.captured_at,a.provenance,a.evidence,a.note,(SELECT json_object('connectionId',connection_id,'externalSessionId',external_id) FROM session_identities WHERE id=a.source_session_id)`

func scanAcceptance(r rowScanner) (store.Acceptance, error) {
	var a store.Acceptance
	var session *store.SessionRef
	err := r.Scan(&a.ID, &a.TaskID, &a.AcceptedAt, &a.CapturedAt, &a.Provenance, &a.Evidence, &a.Note, &session)
	if a.Evidence.Session == nil {
		a.Evidence.Session = session
	}
	return a, notFound(err)
}

func (r acceptances) List(ctx context.Context, id int64) ([]store.Acceptance, error) {
	rows, err := r.Query(ctx, `SELECT `+acceptanceColumns+` FROM task_acceptances a WHERE a.task_id=$1 ORDER BY a.id DESC`, id)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, scanAcceptance)
}

func (r acceptances) LaunchInputs(ctx context.Context, id int64, attempt int) ([]store.Acceptance, error) {
	rows, err := r.Query(ctx, `SELECT `+acceptanceColumns+` FROM task_launch_inputs i JOIN task_acceptances a ON a.id=i.acceptance_id WHERE i.task_id=$1 AND i.attempt=$2 ORDER BY i.source_task_id`, id, attempt)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, scanAcceptance)
}

func (r acceptances) ThreadEvidence(ctx context.Context, sessionID *int64) (store.ThreadEvidence, bool, error) {
	e := store.ThreadEvidence{Artifacts: []execution.Artifact{}}
	err := r.QueryRow(ctx, `SELECT branch,worktree_path,latest_run_id,last_word_run_id,last_word_item_id,last_word_text,last_word_truncated,last_word_at,updated_at,pull_requests,COALESCE(execution_state,'done'),pending_request_count,active_run_id FROM threads WHERE session_id=$1`, sessionID).Scan(&e.Branch, &e.WorktreePath, &e.RunID, &e.ReplyRunID, &e.ReplyItemID, &e.Reply, &e.ReplyTruncated, &e.ReplyAt, &e.ObservedAt, &e.Artifacts, &e.Status, &e.Pending, &e.ActiveRunID)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ThreadEvidence{}, false, nil
	}
	return e, err == nil, err
}

func (r acceptances) JournalThrough(ctx context.Context, id int64, treeID *int64) (int64, error) {
	var through int64
	err := r.QueryRow(ctx, `SELECT COALESCE(max(id),0) FROM journal_entries WHERE task_id=$1 OR tree_id=$2`, id, treeID).Scan(&through)
	return through, err
}

func (r acceptances) Dependents(ctx context.Context, id int64) ([]store.AcceptanceImpact, error) {
	rows, err := r.Query(ctx, `SELECT t.id,t.title,t.launch_attempt>0 OR t.t3_thread_id IS NOT NULL,`+effectiveModeSQL+` FROM task_needs n JOIN tasks t ON t.id=n.task_id LEFT JOIN task_trees tr ON tr.id=t.tree_id WHERE n.need_id=$1 ORDER BY t.id`, id)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(r rowScanner) (store.AcceptanceImpact, error) {
		var d store.AcceptanceImpact
		err := r.Scan(&d.TaskID, &d.Title, &d.Claimed, &d.StartMode)
		return d, err
	})
}

func (r acceptances) Request(ctx context.Context, id int64, requestID string) (string, store.AcceptanceResult, error) {
	var previous string
	var result store.AcceptanceResult
	err := r.QueryRow(ctx, `SELECT input,result FROM acceptance_requests WHERE task_id=$1 AND request_id=$2`, id, requestID).Scan(&previous, &result)
	return previous, result, notFound(err)
}

func (r acceptances) SaveRequest(ctx context.Context, id int64, requestID, input string, result store.AcceptanceResult) error {
	_, err := r.Exec(ctx, `INSERT INTO acceptance_requests(task_id,request_id,input,result) VALUES($1,$2,$3,$4)`, id, requestID, input, result)
	return err
}

func (r acceptances) Current(ctx context.Context, id int64) (*int64, error) {
	var current *int64
	err := r.QueryRow(ctx, `SELECT acceptance_id FROM tasks WHERE id=$1`, id).Scan(&current)
	return current, err
}

func (r acceptances) MarkReopened(ctx context.Context, id int64) error {
	_, err := r.Exec(ctx, `INSERT INTO completion_inferences(task_id,execution_id,signal)
 SELECT t.id,json_extract(o.observation,'$.executionId'),'reopened' FROM tasks t JOIN execution_observations o ON o.session_id=t.session_id
 WHERE t.id=$1 AND COALESCE(json_extract(o.observation,'$.executionId'),'')<>''
 ON CONFLICT(task_id,execution_id) DO UPDATE SET signal='reopened',at=stage_now()`, id)
	return err
}

func (r acceptances) Insert(ctx context.Context, id int64, provenance string, evidence store.AcceptedEvidence, note string) (int64, error) {
	var acceptanceID int64
	err := r.QueryRow(ctx, `INSERT INTO task_acceptances(task_id,provenance,evidence,note) VALUES($1,$2,$3,$4) RETURNING id`, id, provenance, evidence, note).Scan(&acceptanceID)
	return acceptanceID, err
}

func (r acceptances) SetCurrent(ctx context.Context, id, revision int64, acceptanceID *int64) error {
	if _, err := r.Exec(ctx, `UPDATE tasks SET acceptance_id=$2,acceptance_revision=$3,recorded_work=1,updated_at=stage_now() WHERE id=$1`, id, acceptanceID, revision); err != nil {
		return err
	}
	_, err := r.Exec(ctx, `UPDATE task_trees SET recorded_work=1 WHERE id=(SELECT tree_id FROM tasks WHERE id=$1)`, id)
	return err
}

func (r acceptances) JournalEvent(ctx context.Context, id int64, event, detail string, createdBy int64, sourceMachine *int64) error {
	_, err := r.Exec(ctx, `INSERT INTO journal_entries(kind,task_id,tree_id,event_type,text,created_by_user_id,source_machine_id) SELECT 'event',id,tree_id,$2,$3,$4,$5 FROM tasks WHERE id=$1`, id, event, detail, createdBy, sourceMachine)
	return err
}

func (r acceptances) ResetDependentsReady(ctx context.Context, id int64) error {
	_, err := r.Exec(ctx, `UPDATE tasks SET ready_at=NULL WHERE id IN (SELECT task_id FROM task_needs WHERE need_id=$1) AND state='backlog'`, id)
	return err
}

func (r acceptances) InferenceEligible(ctx context.Context, id int64, ref store.SessionRef, executionID string) (bool, error) {
	var eligible bool
	err := r.QueryRow(ctx, `SELECT t.state='done' AND t.acceptance_id IS NULL AND c.auto_complete=1
 AND NOT EXISTS(SELECT 1 FROM completion_inferences WHERE task_id=t.id AND execution_id=$4)
 FROM tasks t JOIN harness_connections c ON c.id=t.connection_id JOIN session_identities si ON si.id=t.session_id
 WHERE t.id=$1 AND si.connection_id=$2 AND si.external_id=$3`, id, ref.ConnectionID, ref.ExternalSessionID, executionID).Scan(&eligible)
	return eligible, notFound(err)
}

func (r acceptances) RecordInference(ctx context.Context, id int64, executionID, signal string, acceptanceID int64) error {
	_, err := r.Exec(ctx, `INSERT INTO completion_inferences(task_id,execution_id,signal,acceptance_id) VALUES($1,$2,$3,$4)`, id, executionID, signal, acceptanceID)
	return err
}

func (r acceptances) ClaimState(ctx context.Context, id int64, attempt int) (store.ClaimState, error) {
	var c store.ClaimState
	err := r.QueryRow(ctx, `SELECT t.launch_reuse,EXISTS(SELECT 1 FROM task_launch_claims c WHERE c.task_id=t.id AND c.attempt=$2),t.adopted,t.acceptance_id,t.tree_id,`+effectiveModeSQL+` FROM tasks t LEFT JOIN task_trees tr ON tr.id=t.tree_id WHERE t.id=$1`, id, attempt).Scan(&c.Reuse, &c.Claimed, &c.Adopted, &c.Accepted, &c.TreeID, &c.StartMode)
	return c, err
}

func (r acceptances) UnacceptedNeeds(ctx context.Context, id int64) ([]int64, error) {
	rows, err := r.Query(ctx, `SELECT d.id FROM task_needs n JOIN tasks d ON d.id=n.need_id WHERE n.task_id=$1 AND d.acceptance_id IS NULL ORDER BY d.id`, id)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, rowTo[int64])
}

func (r acceptances) Released(ctx context.Context, id int64) (bool, error) {
	var eligible bool
	err := r.QueryRow(ctx, `SELECT `+releasedSQL+` FROM tasks t LEFT JOIN task_trees tr ON tr.id=t.tree_id WHERE t.id=$1`, id).Scan(&eligible)
	return eligible, err
}
