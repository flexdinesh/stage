package db

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// AcceptedEvidence is copied once. No source-harness read is needed to use it.
type AcceptedEvidence struct {
	Session             *SessionRef          `json:"session"`
	TaskID              int64                `json:"taskId"`
	Title               string               `json:"title"`
	TreeID              *int64               `json:"treeId"`
	TreeTitle           *string              `json:"treeTitle"`
	ProjectID           int64                `json:"projectId"`
	ThreadID            *string              `json:"threadId"`
	ThreadLink          *string              `json:"threadLink"`
	Branch              *string              `json:"branch"`
	WorktreePath        *string              `json:"worktreePath"`
	ExecutionState      TaskState            `json:"executionState"`
	RunID               *string              `json:"runId"`
	ReplyRunID          *string              `json:"replyRunId"`
	ReplyItemID         *string              `json:"replyItemId"`
	Reply               *string              `json:"reply"`
	ReplyTruncated      bool                 `json:"replyTruncated"`
	ReplyAt             *time.Time           `json:"replyAt"`
	ObservedAt          *time.Time           `json:"observedAt"`
	HistoricalSettledAt *time.Time           `json:"historicalSettledAt"`
	Artifacts           []execution.Artifact `json:"artifacts"`
	JournalThroughID    int64                `json:"journalThroughId"`
}

type Acceptance struct {
	ID         int64            `json:"id"`
	TaskID     int64            `json:"taskId"`
	AcceptedAt time.Time        `json:"acceptedAt"`
	CapturedAt time.Time        `json:"capturedAt"`
	Provenance string           `json:"provenance"`
	Evidence   AcceptedEvidence `json:"evidence"`
	Note       string           `json:"note"`
}

const acceptanceColumns = `a.id,a.task_id,a.accepted_at,a.captured_at,a.provenance,a.evidence,a.note,(SELECT json_object('connectionId',connection_id,'externalSessionId',external_id) FROM session_identities WHERE id=a.source_session_id)`

func scanAcceptance(r rowScanner) (Acceptance, error) {
	var a Acceptance
	var session *SessionRef
	err := r.Scan(&a.ID, &a.TaskID, &a.AcceptedAt, &a.CapturedAt, &a.Provenance, &a.Evidence, &a.Note, &session)
	if a.Evidence.Session == nil {
		a.Evidence.Session = session
	}
	return a, notFound(err)
}
func (s *Store) Acceptances(ctx context.Context, id int64) ([]Acceptance, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+acceptanceColumns+` FROM task_acceptances a WHERE a.task_id=$1 ORDER BY a.id DESC`, id)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, scanAcceptance)
}

type AcceptanceImpact struct {
	TaskID    int64      `json:"taskId"`
	Title     string     `json:"title"`
	Claimed   bool       `json:"claimed"`
	StartMode *StartMode `json:"startMode"`
}
type CompletionPreview struct {
	Revision        int64              `json:"revision"`
	EvidenceVersion string             `json:"evidenceVersion"`
	Evidence        AcceptedEvidence   `json:"evidence"`
	BlockedReason   string             `json:"blockedReason"`
	Dependents      []AcceptanceImpact `json:"dependents"`
}

func completionPreview(ctx context.Context, tx *Tx, id int64) (CompletionPreview, error) {
	var p CompletionPreview
	task, err := scanTask(tx.QueryRow(ctx, taskSelect+` WHERE t.id=$1`, id))
	if err != nil {
		return p, err
	}
	p.Revision = task.AcceptanceRevision
	e := &p.Evidence
	e.Session = task.Session
	e.TaskID, e.Title, e.TreeID, e.TreeTitle, e.ProjectID = id, task.Title, task.TreeID, task.TreeTitle, task.ProjectID
	e.ThreadID, e.ThreadLink, e.ExecutionState = task.T3ThreadID, task.ThreadLink, task.State
	e.Artifacts = []execution.Artifact{}
	var reuse bool
	if err = tx.QueryRow(ctx, `SELECT launch_reuse FROM tasks WHERE id=$1`, id).Scan(&reuse); err != nil {
		return p, err
	}
	if task.State.Active() {
		p.BlockedReason = "Stop execution in its harness before completing this task."
	}
	if reuse {
		p.BlockedReason = "Launch unconfirmed. Wait for reconciliation or link the existing session before completing."
	}
	if task.AcceptanceID != nil {
		p.BlockedReason = "This task is already completed. Reopen it to accept another result."
	}
	if task.T3ThreadID != nil {
		var status string
		var pending int
		var active *string
		err = tx.QueryRow(ctx, `SELECT branch,worktree_path,latest_run_id,last_word_run_id,last_word_item_id,last_word_text,last_word_truncated,last_word_at,updated_at,pull_requests,COALESCE(execution_state,'done'),pending_request_count,active_run_id FROM threads WHERE session_id=$1`, task.SessionIdentityID).Scan(&e.Branch, &e.WorktreePath, &e.RunID, &e.ReplyRunID, &e.ReplyItemID, &e.Reply, &e.ReplyTruncated, &e.ReplyAt, &e.ObservedAt, &e.Artifacts, &status, &pending, &active)
		if err != nil && !errors.Is(notFound(err), ErrNotFound) {
			return p, err
		}
		if pending > 0 || active != nil && *active != "" || status == "running" || status == "needs_input" {
			p.BlockedReason = "Stop execution in its harness before completing this task."
		}
	}
	if task.Session != nil {
		var observed execution.Observation
		err = tx.QueryRow(ctx, "SELECT observation FROM execution_observations WHERE session_id=$1", task.SessionIdentityID).Scan(&observed)
		if err != nil && !errors.Is(notFound(err), ErrNotFound) {
			return p, err
		}
		if err == nil {
			e.Branch = nullIfEmpty(observed.Branch)
			e.WorktreePath = nullIfEmpty(observed.Directory)
			e.RunID = nullIfEmpty(observed.ExecutionID)
			e.ObservedAt = &observed.UpdatedAt
			if observed.Links.App != "" {
				e.ThreadLink = &observed.Links.App
			} else if observed.Links.Web != "" {
				e.ThreadLink = &observed.Links.Web
			}
			if len(observed.Artifacts) > 0 {
				e.Artifacts = observed.Artifacts
			}
			if observed.Reply != nil {
				r := observed.Reply
				e.Reply = &r.Text
				e.ReplyRunID = nullIfEmpty(r.RunID)
				e.ReplyItemID = nullIfEmpty(r.ItemID)
				e.ReplyTruncated = r.Truncated
				e.ReplyAt = &r.UpdatedAt
			}
			if observed.Active {
				p.BlockedReason = "Stop execution in its harness before completing this task."
			}
		}
	}
	if e.Reply != nil {
		chars := []rune(*e.Reply)
		if len(chars) > 4000 {
			excerpt := string(chars[:4000])
			e.Reply, e.ReplyTruncated = &excerpt, true
		}
	}
	if err = tx.QueryRow(ctx, `SELECT COALESCE(max(id),0) FROM journal_entries WHERE task_id=$1 OR tree_id=$2`, id, task.TreeID).Scan(&e.JournalThroughID); err != nil {
		return p, err
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return p, err
	}
	p.EvidenceVersion = fmt.Sprintf("%x", sha256.Sum256(raw))
	p.Dependents = []AcceptanceImpact{}
	rows, err := tx.Query(ctx, `SELECT t.id,t.title,t.launch_attempt>0 OR t.t3_thread_id IS NOT NULL,`+effectiveModeSQL+` FROM task_needs n JOIN tasks t ON t.id=n.task_id LEFT JOIN task_trees tr ON tr.id=t.tree_id WHERE n.need_id=$1 ORDER BY t.id`, id)
	if err != nil {
		return p, err
	}
	p.Dependents, err = collectRows(rows, func(r rowScanner) (AcceptanceImpact, error) {
		var d AcceptanceImpact
		err := r.Scan(&d.TaskID, &d.Title, &d.Claimed, &d.StartMode)
		return d, err
	})
	return p, err
}
func (s *Store) CompletionPreview(ctx context.Context, id int64) (CompletionPreview, error) {
	var p CompletionPreview
	err := s.inTx(ctx, func(tx *Tx) error { var err error; p, err = completionPreview(ctx, tx, id); return err })
	return p, err
}

type AcceptanceRequest struct {
	RequestID       string `json:"requestId"`
	Revision        int64  `json:"revision"`
	EvidenceVersion string `json:"evidenceVersion"`
	Note            string `json:"note"`
}
type AcceptanceResult struct {
	Revision     int64  `json:"revision"`
	AcceptanceID *int64 `json:"acceptanceId"`
}

func (s *Store) ChangeAcceptance(ctx context.Context, id int64, in AcceptanceRequest, reopen bool) (AcceptanceResult, error) {
	var result AcceptanceResult
	if strings.TrimSpace(in.RequestID) == "" {
		return result, Invalid("requestId is required")
	}
	if !utf8.ValidString(in.Note) || len(in.Note) > 16*1024 {
		return result, Invalid("note must be UTF-8, at most 16 KiB")
	}
	if reopen && (in.Note != "" || in.EvidenceVersion != "") {
		return result, Invalid("reopen accepts requestId and revision only")
	}
	raw, _ := json.Marshal(struct {
		AcceptanceRequest
		Reopen bool
	}{in, reopen})
	err := s.inTx(ctx, func(tx *Tx) error {
		var previous string
		err := tx.QueryRow(ctx, `SELECT input,result FROM acceptance_requests WHERE task_id=$1 AND request_id=$2`, id, in.RequestID).Scan(&previous, &result)
		if err == nil {
			if previous != string(raw) {
				return Conflict("requestId already used with different content")
			}
			return nil
		}
		if !errors.Is(notFound(err), ErrNotFound) {
			return err
		}
		p, err := completionPreview(ctx, tx, id)
		if err != nil {
			return err
		}
		if p.Revision != in.Revision {
			return Conflict("Acceptance changed. Refresh and review it again.")
		}
		var current *int64
		if err = tx.QueryRow(ctx, `SELECT acceptance_id FROM tasks WHERE id=$1`, id).Scan(&current); err != nil {
			return err
		}
		event, detail := "task_reopened", "Reopened by user; unclaimed dependents wait for acceptance again."
		if reopen {
			if current == nil {
				return Conflict("Task is already open. Refresh its acceptance.")
			}
			// A session still closed in its harness must not re-complete this execution.
			if _, err = tx.Exec(ctx, `INSERT INTO completion_inferences(task_id,execution_id,signal)
 SELECT t.id,json_extract(o.observation,'$.executionId'),'reopened' FROM tasks t JOIN execution_observations o ON o.session_id=t.session_id
 WHERE t.id=$1 AND COALESCE(json_extract(o.observation,'$.executionId'),'')<>''
 ON CONFLICT(task_id,execution_id) DO UPDATE SET signal='reopened',at=stage_now()`, id); err != nil {
				return err
			}
		} else {
			if p.BlockedReason != "" {
				return Conflict("%s", p.BlockedReason)
			}
			if p.EvidenceVersion != in.EvidenceVersion {
				return Conflict("Evidence changed. Refresh and review it again.")
			}
			var acceptanceID int64
			err := tx.QueryRow(ctx, `INSERT INTO task_acceptances(task_id,provenance,evidence,note) VALUES($1,'user',$2,$3) RETURNING id`, id, p.Evidence, in.Note).Scan(&acceptanceID)
			if err != nil {
				return err
			}
			result.AcceptanceID = &acceptanceID
			event, detail = "task_completed", "Completed by user; accepted evidence saved in Stage."
			if strings.TrimSpace(in.Note) != "" {
				if _, err = appendNote(ctx, tx, JournalOwner{TaskID: &id}, AppendNoteInput{Text: in.Note, Category: "outcome", RequestID: "acceptance:" + in.RequestID}); err != nil {
					return err
				}
			}
		}
		result.Revision = p.Revision + 1
		if err = recordAcceptance(ctx, tx, id, result.Revision, result.AcceptanceID, event, detail); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO acceptance_requests(task_id,request_id,input,result) VALUES($1,$2,$3,$4)`, id, in.RequestID, string(raw), result)
		return err
	})
	return result, err
}

// recordAcceptance sets (or, with a nil acceptance, clears) the task's current
// acceptance at revision, journals it and refreshes dependents' readiness.
func recordAcceptance(ctx context.Context, tx *Tx, id, revision int64, acceptanceID *int64, event, detail string) error {
	if _, err := tx.Exec(ctx, `UPDATE tasks SET acceptance_id=$2,acceptance_revision=$3,recorded_work=1,updated_at=stage_now() WHERE id=$1`, id, acceptanceID, revision); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE task_trees SET recorded_work=1 WHERE id=(SELECT tree_id FROM tasks WHERE id=$1)`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO journal_entries(kind,task_id,tree_id,event_type,text) SELECT 'event',id,tree_id,$2,$3 FROM tasks WHERE id=$1`, id, event, detail); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE tasks SET ready_at=NULL WHERE id IN (SELECT task_id FROM task_needs WHERE need_id=$1) AND state='backlog'`, id)
	return err
}

// InferCompletion completes a done task whose session was closed in its
// harness (T3 settle, OpenCode archive), when the task's connection allows
// it. It decides once per execution: an earlier inference or a user reopen
// for o.ExecutionID suppresses it. Reports whether it completed the task.
func (s *Store) InferCompletion(ctx context.Context, id int64, o execution.Observation) (bool, error) {
	if o.Closed == nil || o.Active || o.ExecutionID == "" {
		return false, nil
	}
	var provenance, detail string
	switch o.Closed.Signal {
	case execution.ClosedSettled:
		provenance, detail = "harness_settled", "Completed from T3 settle; accepted evidence saved in Stage."
	case execution.ClosedArchived:
		provenance, detail = "harness_archived", "Completed from OpenCode archive; accepted evidence saved in Stage."
	default:
		return false, nil
	}
	inferred := false
	err := s.inTx(ctx, func(tx *Tx) error {
		var eligible bool
		err := tx.QueryRow(ctx, `SELECT t.state='done' AND t.acceptance_id IS NULL AND c.auto_complete=1
 AND NOT EXISTS(SELECT 1 FROM completion_inferences WHERE task_id=t.id AND execution_id=$4)
 FROM tasks t JOIN harness_connections c ON c.id=t.connection_id JOIN session_identities si ON si.id=t.session_id
 WHERE t.id=$1 AND si.connection_id=$2 AND si.external_id=$3`, id, o.Session.ConnectionID, o.Session.ExternalSessionID, o.ExecutionID).Scan(&eligible)
		if errors.Is(notFound(err), ErrNotFound) {
			return nil
		}
		if err != nil || !eligible {
			return err
		}
		p, err := completionPreview(ctx, tx, id)
		if err != nil || p.BlockedReason != "" {
			return err
		}
		var acceptanceID int64
		if err = tx.QueryRow(ctx, `INSERT INTO task_acceptances(task_id,provenance,evidence,note) VALUES($1,$2,$3,'') RETURNING id`, id, provenance, p.Evidence).Scan(&acceptanceID); err != nil {
			return err
		}
		if err = recordAcceptance(ctx, tx, id, p.Revision+1, &acceptanceID, "task_completed", detail); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO completion_inferences(task_id,execution_id,signal,acceptance_id) VALUES($1,$2,$3,$4)`, id, o.ExecutionID, o.Closed.Signal, acceptanceID); err != nil {
			return err
		}
		inferred = true
		return nil
	})
	return inferred, err
}

func (s *Store) AcceptedLaunchInputs(ctx context.Context, id int64, attempt int) ([]Acceptance, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+acceptanceColumns+` FROM task_launch_inputs i JOIN task_acceptances a ON a.id=i.acceptance_id WHERE i.task_id=$1 AND i.attempt=$2 ORDER BY i.source_task_id`, id, attempt)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, scanAcceptance)
}

func checkLaunchClaim(ctx context.Context, tx *Tx, t Transition, attempt int) error {
	var reuse, claimed, adopted bool
	var accepted *int64
	var tree *int64
	var mode *StartMode
	err := tx.QueryRow(ctx, `SELECT t.launch_reuse,EXISTS(SELECT 1 FROM task_launch_claims c WHERE c.task_id=t.id AND c.attempt=$2),t.adopted,t.acceptance_id,t.tree_id,`+effectiveModeSQL+` FROM tasks t LEFT JOIN task_trees tr ON tr.id=t.tree_id WHERE t.id=$1`, t.TaskID, attempt).Scan(&reuse, &claimed, &adopted, &accepted, &tree, &mode)
	if err != nil {
		return err
	}
	if adopted {
		return Conflict("Adopted tasks continue in T3.")
	}
	if accepted != nil {
		return Conflict("Task is completed. Reopen it before starting work.")
	}
	// Unknown transport outcomes must reconcile the exact original attempt.
	if reuse && claimed {
		return nil
	}
	if tree != nil {
		if err := treeOpen(ctx, tx, *tree); err != nil {
			return err
		}
	}
	var needs []int64
	rows, err := tx.Query(ctx, `SELECT d.id FROM task_needs n JOIN tasks d ON d.id=n.need_id WHERE n.task_id=$1 AND d.acceptance_id IS NULL ORDER BY d.id`, t.TaskID)
	if err != nil {
		return err
	}
	needs, err = collectRows(rows, rowTo[int64])
	if err != nil {
		return err
	}
	if len(needs) > 0 {
		return Conflict("Prerequisites %v need user completion in Stage. Complete them or edit dependencies before starting.", needs)
	}
	if t.RequireAuto {
		var eligible bool
		if err := tx.QueryRow(ctx, `SELECT `+releasedSQL+` FROM tasks t LEFT JOIN task_trees tr ON tr.id=t.tree_id WHERE t.id=$1`, t.TaskID).Scan(&eligible); err != nil {
			return err
		}
		if !eligible || mode == nil || *mode != StartAuto {
			return Conflict("Task no longer qualifies for automatic release. Refresh its dependencies and start policy.")
		}
	}
	return nil
}
