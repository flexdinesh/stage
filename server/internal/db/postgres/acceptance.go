//go:build postgres

package postgres

import (
	"context"
	"encoding/json"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

type acceptances struct{ *runner }

func (t *tx) Acceptances() store.Acceptances { return acceptances{t.r} }

var acceptanceColumns = `a.id,a.task_id,a.accepted_at,a.captured_at,a.provenance,a.evidence,a.note,(SELECT jsonb_build_object('connectionId',connection_id,'externalSessionId',external_id) FROM session_identities WHERE id=a.source_session_id),` + attributionColumns("a.attribution")

func scanAcceptance(r rowScanner) (store.Acceptance, error) {
	var a store.Acceptance
	var session *store.SessionRef
	err := r.Scan(&a.ID, &a.TaskID, &a.AcceptedAt, &a.CapturedAt, &a.Provenance, &a.Evidence, &a.Note, &session, &a.Attribution, &a.Actor.DisplayName)
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

func (r acceptances) ReleaseCause(ctx context.Context, id int64) (store.Acceptance, error) {
	return scanAcceptance(r.QueryRow(ctx, `SELECT `+acceptanceColumns+` FROM task_needs n
 JOIN tasks t ON t.id=n.need_id JOIN task_acceptances a ON a.id=t.acceptance_id
 WHERE n.task_id=$1 ORDER BY a.accepted_at DESC,a.id DESC LIMIT 1`, id))
}

func (r acceptances) JournalThrough(ctx context.Context, id int64, treeID *int64) (int64, error) {
	var through int64
	err := r.QueryRow(ctx, `SELECT COALESCE(max(id),0) FROM journal_entries WHERE task_id=$1 OR tree_id=$2`, id, treeID).Scan(&through)
	return through, err
}

func (r acceptances) Dependents(ctx context.Context, id int64) ([]store.AcceptanceImpact, error) {
	rows, err := r.Query(ctx, `SELECT t.id,t.title,t.launch_attempt>0 OR t.session_id IS NOT NULL,`+effectiveModeSQL+` FROM task_needs n JOIN tasks t ON t.id=n.task_id LEFT JOIN task_trees tr ON tr.id=t.tree_id WHERE n.need_id=$1 ORDER BY t.id`, id)
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
 SELECT t.id,o.observation->>'executionId','reopened' FROM tasks t JOIN execution_observations o ON o.session_id=t.session_id
 WHERE t.id=$1 AND COALESCE(o.observation->>'executionId','')<>''
 ON CONFLICT(task_id,execution_id) DO UPDATE SET signal='reopened',at=`+nowSQL, id)
	return err
}

func (r acceptances) Insert(ctx context.Context, id int64, provenance string, evidence store.AcceptedEvidence, note string, sourceSessionID *int64, attribution store.Attribution) (int64, error) {
	var acceptanceID int64
	// evidence is text: its exact JSON bytes are the accepted snapshot.
	raw, err := json.Marshal(evidence)
	if err != nil {
		return 0, err
	}
	err = r.QueryRow(ctx, `INSERT INTO task_acceptances(task_id,provenance,evidence,note,source_session_id,attribution) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, id, provenance, string(raw), note, sourceSessionID, attribution).Scan(&acceptanceID)
	return acceptanceID, err
}

func (r acceptances) SetCurrent(ctx context.Context, id, revision int64, acceptanceID *int64) error {
	if _, err := r.Exec(ctx, `UPDATE tasks SET acceptance_id=$2,acceptance_revision=$3,recorded_work=true,updated_at=`+nowSQL+` WHERE id=$1`, id, acceptanceID, revision); err != nil {
		return err
	}
	_, err := r.Exec(ctx, `UPDATE task_trees SET recorded_work=true WHERE id=(SELECT tree_id FROM tasks WHERE id=$1)`, id)
	return err
}

func (r acceptances) JournalEvent(ctx context.Context, id int64, event, detail string, createdBy int64, sourceMachine *int64, attribution store.Attribution) error {
	_, err := r.Exec(ctx, `INSERT INTO journal_entries(kind,task_id,tree_id,event_type,text,created_by_user_id,source_machine_id,attribution) SELECT 'event',id,tree_id,$2,$3,$4,$5,$6 FROM tasks WHERE id=$1`, id, event, detail, createdBy, sourceMachine, attribution)
	return err
}

func (r acceptances) ResetDependentsReady(ctx context.Context, id int64) error {
	_, err := r.Exec(ctx, `UPDATE tasks SET ready_at=NULL WHERE id IN (SELECT task_id FROM task_needs WHERE need_id=$1) AND state='backlog'`, id)
	return err
}

func (r acceptances) InferenceEligible(ctx context.Context, id int64, ref store.SessionRef, executionID string) (bool, error) {
	var eligible bool
	err := r.QueryRow(ctx, `SELECT t.state='done' AND t.acceptance_id IS NULL AND c.auto_complete
 AND NOT EXISTS(SELECT 1 FROM completion_inferences WHERE task_id=t.id AND execution_id=$4)
 FROM tasks t JOIN harness_connections c ON c.id=t.connection_id JOIN session_identities si ON si.id=t.session_id
 WHERE t.id=$1 AND si.connection_id=$2 AND si.external_id=$3`, id, ref.ConnectionID, ref.ExternalSessionID, executionID).Scan(&eligible)
	return eligible, notFound(err)
}

func (r acceptances) RecordInference(ctx context.Context, id int64, executionID, signal string, acceptanceID int64) error {
	_, err := r.Exec(ctx, `INSERT INTO completion_inferences(task_id,execution_id,signal,acceptance_id) VALUES($1,$2,$3,$4)`, id, executionID, signal, acceptanceID)
	return err
}

func (r acceptances) ClaimState(ctx context.Context, id int64, _ int) (store.ClaimState, error) {
	var c store.ClaimState
	err := r.QueryRow(ctx, `SELECT t.adopted,t.acceptance_id,t.tree_id,`+effectiveModeSQL+` FROM tasks t LEFT JOIN task_trees tr ON tr.id=t.tree_id WHERE t.id=$1`, id).Scan(&c.Adopted, &c.Accepted, &c.TreeID, &c.StartMode)
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
