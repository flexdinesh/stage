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

	"github.com/flexdinesh/stage/server/internal/t3"
)

// AcceptedEvidence is copied once. No source-harness read is needed to use it.
type AcceptedEvidence struct {
	TaskID              int64            `json:"taskId"`
	Title               string           `json:"title"`
	TreeID              *int64           `json:"treeId"`
	TreeTitle           *string          `json:"treeTitle"`
	ProjectID           int64            `json:"projectId"`
	ThreadID            *string          `json:"threadId"`
	ThreadLink          *string          `json:"threadLink"`
	Branch              *string          `json:"branch"`
	WorktreePath        *string          `json:"worktreePath"`
	ExecutionState      TaskState        `json:"executionState"`
	RunID               *string          `json:"runId"`
	ReplyRunID          *string          `json:"replyRunId"`
	ReplyItemID         *string          `json:"replyItemId"`
	Reply               *string          `json:"reply"`
	ReplyTruncated      bool             `json:"replyTruncated"`
	ReplyAt             *time.Time       `json:"replyAt"`
	ObservedAt          *time.Time       `json:"observedAt"`
	HistoricalSettledAt *time.Time       `json:"historicalSettledAt"`
	Artifacts           []t3.PullRequest `json:"artifacts"`
	JournalThroughID    int64            `json:"journalThroughId"`
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

const acceptanceColumns = `a.id,a.task_id,a.accepted_at,a.captured_at,a.provenance,a.evidence,a.note`

func scanAcceptance(r rowScanner) (Acceptance, error) {
	var a Acceptance
	err := r.Scan(&a.ID, &a.TaskID, &a.AcceptedAt, &a.CapturedAt, &a.Provenance, &a.Evidence, &a.Note)
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
	e.TaskID, e.Title, e.TreeID, e.TreeTitle, e.ProjectID = id, task.Title, task.TreeID, task.TreeTitle, task.ProjectID
	e.ThreadID, e.ThreadLink, e.ExecutionState = task.T3ThreadID, task.ThreadLink, task.State
	e.Artifacts = []t3.PullRequest{}
	var reuse bool
	if err = tx.QueryRow(ctx, `SELECT launch_reuse FROM tasks WHERE id=$1`, id).Scan(&reuse); err != nil {
		return p, err
	}
	if task.State.Active() {
		p.BlockedReason = "Stop execution in T3 before completing this task."
	}
	if reuse {
		p.BlockedReason = "Launch outcome is unknown. Retry the existing launch to reconcile it before completing."
	}
	if task.AcceptanceID != nil {
		p.BlockedReason = "This task is already completed. Reopen it to accept another result."
	}
	if task.T3ThreadID != nil {
		var status string
		var pending int
		var active *string
		err = tx.QueryRow(ctx, `SELECT branch,worktree_path,latest_run_id,last_word_run_id,last_word_item_id,last_word_text,last_word_truncated,last_word_at,updated_at,pull_requests,status,pending_request_count,active_run_id FROM threads WHERE t3_thread_id=$1`, task.T3ThreadID).Scan(&e.Branch, &e.WorktreePath, &e.RunID, &e.ReplyRunID, &e.ReplyItemID, &e.Reply, &e.ReplyTruncated, &e.ReplyAt, &e.ObservedAt, &e.Artifacts, &status, &pending, &active)
		if err != nil && !errors.Is(notFound(err), ErrNotFound) {
			return p, err
		}
		if pending > 0 || active != nil && *active != "" || status == "running" || status == "preparing" || status == "queued" || status == "starting" || status == "waiting" {
			p.BlockedReason = "Stop execution in T3 before completing this task."
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
		} else {
			if p.BlockedReason != "" {
				return Conflict("%s", p.BlockedReason)
			}
			if p.EvidenceVersion != in.EvidenceVersion {
				return Conflict("Evidence changed. Refresh and review it again.")
			}
			a, err := scanAcceptance(tx.QueryRow(ctx, `INSERT INTO task_acceptances(task_id,provenance,evidence,note) VALUES($1,'user',$2,$3) RETURNING id,task_id,accepted_at,captured_at,provenance,evidence,note`, id, p.Evidence, in.Note))
			if err != nil {
				return err
			}
			result.AcceptanceID = &a.ID
			event, detail = "task_completed", "Completed by user; accepted evidence saved in Stage."
			if strings.TrimSpace(in.Note) != "" {
				if _, err = appendNote(ctx, tx, JournalOwner{TaskID: &id}, AppendNoteInput{Text: in.Note, Category: "outcome", RequestID: "acceptance:" + in.RequestID}); err != nil {
					return err
				}
			}
		}
		result.Revision = p.Revision + 1
		if _, err = tx.Exec(ctx, `UPDATE tasks SET acceptance_id=$2,acceptance_revision=$3,recorded_work=1,updated_at=stage_now() WHERE id=$1`, id, result.AcceptanceID, result.Revision); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE task_trees SET recorded_work=1 WHERE id=(SELECT tree_id FROM tasks WHERE id=$1)`, id); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO journal_entries(kind,task_id,tree_id,event_type,text) SELECT 'event',id,tree_id,$2,$3 FROM tasks WHERE id=$1`, id, event, detail); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE tasks SET ready_at=NULL WHERE id IN (SELECT task_id FROM task_needs WHERE need_id=$1) AND state='backlog'`, id); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO acceptance_requests(task_id,request_id,input,result) VALUES($1,$2,$3,$4)`, id, in.RequestID, string(raw), result)
		return err
	})
	return result, err
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
