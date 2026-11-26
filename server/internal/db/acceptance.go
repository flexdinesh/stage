package db

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

type (
	// AcceptedEvidence is copied once.
	AcceptedEvidence = store.AcceptedEvidence
	Acceptance       = store.Acceptance
	AcceptanceImpact = store.AcceptanceImpact
	AcceptanceResult = store.AcceptanceResult
)

func (s *Store) Acceptances(ctx context.Context, id int64) ([]Acceptance, error) {
	return read(ctx, s, func(tx store.Tx) ([]Acceptance, error) { return tx.Acceptances().List(ctx, id) })
}

type CompletionPreview struct {
	Revision        int64              `json:"revision"`
	EvidenceVersion string             `json:"evidenceVersion"`
	Evidence        AcceptedEvidence   `json:"evidence"`
	BlockedReason   string             `json:"blockedReason"`
	Dependents      []AcceptanceImpact `json:"dependents"`
}

func completionPreview(ctx context.Context, tx store.Tx, id int64) (CompletionPreview, error) {
	var p CompletionPreview
	task, err := tx.Tasks().Get(ctx, id)
	if err != nil {
		return p, err
	}
	p.Revision = task.AcceptanceRevision
	e := &p.Evidence
	e.Session = task.Session
	e.TaskID, e.Title, e.TreeID, e.TreeTitle, e.ProjectID = id, task.Title, task.TreeID, task.TreeTitle, task.ProjectID
	e.ThreadID, e.ExecutionState = task.T3ThreadID, task.State
	e.Artifacts = []execution.Artifact{}
	if task.State.Active() {
		p.BlockedReason = "Stop execution in its harness before completing this task."
	}
	if task.AcceptanceID != nil {
		p.BlockedReason = "This task is already completed. Change its state back to accept another result."
	}
	if task.Session != nil {
		var observed execution.Observation
		if task.SessionIdentityID != nil {
			observed, err = tx.Observations().Get(ctx, *task.SessionIdentityID)
		} else {
			err = ErrNotFound
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
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
	if e.JournalThroughID, err = tx.Acceptances().JournalThrough(ctx, id, task.TreeID); err != nil {
		return p, err
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return p, err
	}
	p.EvidenceVersion = fmt.Sprintf("%x", sha256.Sum256(raw))
	p.Dependents = []AcceptanceImpact{}
	p.Dependents, err = tx.Acceptances().Dependents(ctx, id)
	return p, err
}
func (s *Store) CompletionPreview(ctx context.Context, id int64) (CompletionPreview, error) {
	var p CompletionPreview
	err := s.e.Write(ctx, func(tx store.Tx) error {
		// Write may rerun fn (Postgres retry): start each run from scratch.
		p = CompletionPreview{}
		var err error
		p, err = completionPreview(ctx, tx, id)
		return err
	})
	return p, err
}

type AcceptanceRequest struct {
	RequestID       string `json:"requestId"`
	Revision        int64  `json:"revision"`
	EvidenceVersion string `json:"evidenceVersion"`
	Note            string `json:"note"`
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
	err := s.e.Write(ctx, func(tx store.Tx) error {
		// Write may rerun fn (Postgres retry): start each run from scratch.
		result = AcceptanceResult{}
		// One revision sequence per task; requestId replays see the first.
		if err := tx.Tasks().Lock(ctx, id); err != nil {
			return err
		}
		ac := tx.Acceptances()
		previous, recorded, err := ac.Request(ctx, id, in.RequestID)
		if err == nil {
			if previous != string(raw) {
				return Conflict("requestId already used with different content")
			}
			result = recorded
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		p, err := completionPreview(ctx, tx, id)
		if err != nil {
			return err
		}
		if p.Revision != in.Revision {
			return Conflict("Acceptance changed. Refresh and review it again.")
		}
		current, err := ac.Current(ctx, id)
		if err != nil {
			return err
		}
		event, detail := "task_reopened", "Reopened by user; unclaimed dependents wait for acceptance again."
		if reopen {
			if current == nil {
				return Conflict("Task is already open. Refresh its acceptance.")
			}
			// A session still closed in its harness must not re-complete this execution.
			if err = ac.MarkReopened(ctx, id); err != nil {
				return err
			}
		} else {
			if p.BlockedReason != "" {
				return Conflict("%s", p.BlockedReason)
			}
			if p.EvidenceVersion != in.EvidenceVersion {
				return Conflict("Evidence changed. Refresh and review it again.")
			}
			acceptanceID, err := insertAcceptance(ctx, tx, id, "user", p.Evidence, in.Note)
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
		return ac.SaveRequest(ctx, id, in.RequestID, string(raw), result)
	})
	return result, err
}

// recordAcceptance sets (or, with a nil acceptance, clears) the task's current
// acceptance at revision, journals it and refreshes dependents' readiness.
func recordAcceptance(ctx context.Context, tx store.Tx, id, revision int64, acceptanceID *int64, event, detail string) error {
	ac := tx.Acceptances()
	if err := ac.SetCurrent(ctx, id, revision, acceptanceID); err != nil {
		return err
	}
	if err := ac.JournalEvent(ctx, id, event, detail, ActingUser(ctx), ActingMachine(ctx)); err != nil {
		return err
	}
	return ac.ResetDependentsReady(ctx, id)
}

// InferCompletion completes a done task whose session was closed in its
// harness, when the task's connection allows it. closure is the harness
// descriptor's entry for o's signal (provenance and event text). It decides
// once per execution: an earlier inference or a user reopen for
// o.ExecutionID suppresses it. Reports whether it completed the task.
func (s *Store) InferCompletion(ctx context.Context, id int64, o execution.Observation, closure harness.Closure) (bool, error) {
	if o.Closed == nil || o.Active || o.ExecutionID == "" || closure.Signal != o.Closed.Signal || closure.Provenance == "" {
		return false, nil
	}
	provenance, detail := closure.Provenance, closure.Event
	inferred := false
	err := s.e.Write(ctx, func(tx store.Tx) error {
		// Write may rerun fn (Postgres retry): start each run from scratch.
		inferred = false
		ac := tx.Acceptances()
		eligible, err := ac.InferenceEligible(ctx, id, o.Session, o.ExecutionID)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil || !eligible {
			return err
		}
		p, err := completionPreview(ctx, tx, id)
		if err != nil || p.BlockedReason != "" {
			return err
		}
		acceptanceID, err := insertAcceptance(ctx, tx, id, provenance, p.Evidence, "")
		if err != nil {
			return err
		}
		if err = recordAcceptance(ctx, tx, id, p.Revision+1, &acceptanceID, "task_completed", detail); err != nil {
			return err
		}
		if err = ac.RecordInference(ctx, id, o.ExecutionID, o.Closed.Signal, acceptanceID); err != nil {
			return err
		}
		inferred = true
		return nil
	})
	return inferred, err
}

func (s *Store) AcceptedLaunchInputs(ctx context.Context, id int64, attempt int) ([]Acceptance, error) {
	return read(ctx, s, func(tx store.Tx) ([]Acceptance, error) { return tx.Acceptances().LaunchInputs(ctx, id, attempt) })
}

func checkLaunchClaim(ctx context.Context, tx store.Tx, t Transition, attempt int) error {
	ac := tx.Acceptances()
	c, err := ac.ClaimState(ctx, t.TaskID, attempt)
	if err != nil {
		return err
	}
	if c.Adopted {
		return Conflict("Adopted tasks continue in T3.")
	}
	if c.Accepted != nil {
		return Conflict("Task is completed. Reopen it before starting work.")
	}
	if c.TreeID != nil {
		if err := treeOpen(ctx, tx, *c.TreeID); err != nil {
			return err
		}
	}
	needs, err := ac.UnacceptedNeeds(ctx, t.TaskID)
	if err != nil {
		return err
	}
	if len(needs) > 0 {
		return Conflict("Prerequisites %v need user completion in Stage. Complete them or edit dependencies before starting.", needs)
	}
	if t.RequireAuto {
		eligible, err := ac.Released(ctx, t.TaskID)
		if err != nil {
			return err
		}
		if !eligible || c.StartMode == nil || *c.StartMode != StartAuto {
			return Conflict("Task no longer qualifies for automatic release. Refresh its dependencies and start policy.")
		}
	}
	return nil
}
