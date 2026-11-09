package sync

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/flexdinesh/t3-stage/server/internal/db"
	"github.com/flexdinesh/t3-stage/server/internal/t3"
)

// ContinuationEnvelope is shared with API preview data: preserve the saved
// prompt byte-for-byte, then append only the prerequisite reference.
func ContinuationEnvelope(prompt, title, id, link string) string {
	reference := fmt.Sprintf("\n\n---\nPrerequisite session\nTitle: %s\nThread ID: %s", title, id)
	if link != "" {
		reference += "\nT3: " + link
	}
	return prompt + reference
}

func sessionState(d t3.ThreadDetail) db.TaskState {
	// Unlike legacy task mapping, a stale settled flag must never hide a
	// restarted turn, pending input, failure, or interruption.
	if d.PendingRequestCount > 0 {
		return db.StateNeedsInput
	}
	if d.ActiveRunID != "" {
		return db.StateRunning
	}
	status := d.Status
	if status == t3.StatusIdle && len(d.RecentRuns) > 0 {
		status = d.RecentRuns[0].Status
	}
	if st, ok := runState(status); ok && st != db.StateDone {
		return st
	}
	if status != t3.StatusIdle && status != t3.StatusCompleted {
		return db.StateRunning
	}
	if d.Settled {
		return db.StateSettled
	}
	return db.StateDone
}

func failedSession(st db.TaskState) bool {
	return st == db.StateFailed || st == db.StateInterrupted || st == db.StateCancelled
}

func readiness(after, target t3.ThreadDetail) []db.WaitReason {
	reasons := []db.WaitReason{}
	if after.Archived || target.Archived || after.ParentThreadID != "" || target.ParentThreadID != "" {
		return []db.WaitReason{db.WaitThreadMissing}
	}
	a, b := sessionState(after), sessionState(target)
	if failedSession(a) {
		reasons = append(reasons, db.WaitPredecessorFailed)
	} else if !after.Settled || a != db.StateSettled {
		reasons = append(reasons, db.WaitPredecessorUnsettled)
	}
	switch {
	case b == db.StateNeedsInput:
		reasons = append(reasons, db.WaitTargetNeedsInput)
	case b == db.StateRunning:
		reasons = append(reasons, db.WaitTargetRunning)
	case failedSession(b):
		reasons = append(reasons, db.WaitTargetFailed)
	}
	return reasons
}

func blockedReasons(reasons []db.WaitReason) bool {
	for _, r := range reasons {
		switch r {
		case db.WaitPredecessorFailed, db.WaitTargetFailed, db.WaitUpstreamCancelled, db.WaitThreadMissing, db.WaitT3Offline, db.WaitDeliveryUncertain, db.WaitLegacyConflict:
			return true
		}
	}
	return false
}

func (s *Service) readSession(ctx context.Context, id string) (t3.ThreadDetail, error) {
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	d, err := s.t3.ReadThread(callCtx, id)
	if err != nil {
		return d, err
	}
	if d.ThreadID != id {
		return d, fmt.Errorf("T3 returned a different session for %s", id)
	}
	if err := s.store.UpsertThreadDetail(ctx, d); err != nil {
		return d, err
	}
	return d, nil
}

// RefreshSession reads without delivering or changing the legacy task.
func (s *Service) RefreshSession(ctx context.Context, id string) error {
	_, err := s.readSession(ctx, id)
	if errors.Is(err, t3.ErrNotFound) {
		return db.ErrNotFound
	}
	return t3Err(err)
}

// ReconcileContinuations refreshes every participant, including settled
// sessions, then performs a second fresh readiness read before each claim.
// No cached mirror row can authorize a send.
func (s *Service) ReconcileContinuations(ctx context.Context) error {
	if err := s.store.ExpireContinuationClaims(ctx); err != nil {
		return err
	}
	order, err := s.store.ListContinuations(ctx, "")
	if err != nil {
		return err
	}
	if s.t3.Status(ctx).Connected {
		seen := map[string]bool{}
		for _, c := range order {
			if c.State == db.ContinuationSettled || c.State == db.ContinuationCancelled {
				continue
			}
			for _, id := range []string{c.AfterThreadID, c.TargetThreadID} {
				if seen[id] {
					continue
				}
				seen[id] = true
				// Errors are classified during the readiness read below. Even if one
				// endpoint fails, read the remaining endpoints during this pass.
				if _, err := s.readSession(ctx, id); err != nil && ctx.Err() == nil {
					s.log.Debug("refresh continuation session", "thread", id, "err", err)
				}
			}
		}
	}
	for _, c := range order {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.reconcileContinuation(ctx, c.ID, true); err != nil {
			return err
		}
	}
	return nil
}

// RecheckContinuation is the mutation path. It observes wait conditions,
// never claims or sends, so Save and Resume leave all T3 runs untouched.
func (s *Service) RecheckContinuation(ctx context.Context, id int64) error {
	return s.reconcileContinuation(ctx, id, false)
}

func (s *Service) reconcileContinuation(ctx context.Context, id int64, allowSend bool) error {
	c, err := s.store.GetContinuation(ctx, id)
	if err != nil {
		return err
	}
	switch c.State {
	case db.ContinuationPaused, db.ContinuationCancelled, db.ContinuationSettled, db.ContinuationUncertain, db.ContinuationSending:
		return nil
	}
	observe := func(state db.ContinuationState, reasons []db.WaitReason, lastError *string, completed bool) error {
		return s.store.ObserveContinuation(ctx, c, state, reasons, lastError, completed)
	}
	if !s.t3.Status(ctx).Connected {
		return observe(db.ContinuationBlocked, []db.WaitReason{db.WaitT3Offline}, nil, c.RunCompleted)
	}
	// Always read both endpoints. A partial successful read never authorizes
	// delivery, and no readiness result survives a later reconcile pass.
	after, aerr := s.readSession(ctx, c.AfterThreadID)
	target, terr := s.readSession(ctx, c.TargetThreadID)
	if aerr != nil || terr != nil {
		reason := db.WaitT3Offline
		if errors.Is(aerr, t3.ErrNotFound) || errors.Is(terr, t3.ErrNotFound) {
			reason = db.WaitThreadMissing
		}
		msg := fmt.Sprintf("session refresh failed: predecessor=%v, target=%v", aerr, terr)
		return observe(db.ContinuationBlocked, []db.WaitReason{reason}, &msg, c.RunCompleted)
	}
	if c.RequestID != nil {
		return s.observeDelivered(ctx, c, target)
	}
	reasons := readiness(after, target)
	if c.PreviousContinuationID != nil {
		previous, err := s.store.GetContinuation(ctx, *c.PreviousContinuationID)
		if err != nil {
			return err
		}
		if previous.State == db.ContinuationCancelled || slices.Contains(previous.WaitReasons, db.WaitUpstreamCancelled) {
			reasons = append(reasons, db.WaitUpstreamCancelled)
		} else if previous.State != db.ContinuationSettled {
			reasons = append(reasons, db.WaitUpstreamPending)
		}
	}
	conflict, err := s.store.HasLegacyConflict(ctx, c.AfterThreadID, c.TargetThreadID)
	if err != nil {
		return err
	}
	if conflict {
		reasons = append(reasons, db.WaitLegacyConflict)
	}
	if !s.t3.Status(ctx).Connected {
		reasons = append(reasons, db.WaitT3Offline)
	}
	if len(reasons) > 0 || !allowSend {
		state := db.ContinuationWaiting
		if blockedReasons(reasons) {
			state = db.ContinuationBlocked
		}
		return observe(state, reasons, nil, false)
	}
	// The only send path follows the immediately preceding fresh reads and
	// a database claim. Queue mode remains mandatory even if T3 starts again
	// in the unavoidable gap between observation and delivery.
	link := t3.WebThreadURL(s.publicURL, s.t3.Status(ctx).EnvironmentID, after.ThreadID)
	if link == "" {
		link = after.Link
	}
	if link == "" {
		link = t3.AppThreadURL(s.t3.Status(ctx).EnvironmentID, after.ThreadID)
	}
	envelope := ContinuationEnvelope(c.Prompt, after.Title, after.ThreadID, link)
	c, claimed, err := s.store.ClaimContinuation(ctx, c, envelope)
	if err != nil || !claimed {
		return err
	}
	if c.RequestID == nil {
		return errors.New("claimed continuation has no request id")
	}
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	result, sendErr := s.t3.QueueContinuation(sendCtx, c.TargetThreadID, envelope, *c.RequestID)
	cancel()
	recordCtx, recordCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer recordCancel()
	return s.store.RecordContinuationDelivery(recordCtx, c, result.RunID, result.MessageID, sendErr)
}

func (s *Service) observeDelivered(ctx context.Context, c db.Continuation, d t3.ThreadDetail) error {
	state := db.ContinuationRunning
	reasons := []db.WaitReason{}
	completed := c.RunCompleted
	found := false
	if c.RunID != nil {
		for _, run := range d.RecentRuns {
			if run.RunID != *c.RunID {
				continue
			}
			found = true
			if st, ok := runState(run.Status); ok && failedSession(st) {
				return s.store.ObserveContinuation(ctx, c, db.ContinuationBlocked, []db.WaitReason{db.WaitTargetFailed}, nil, false)
			}
			completed = run.Status == t3.StatusCompleted
			break
		}
	}
	if !found && !completed {
		// Recent runs are bounded by T3. Absence proves neither completion nor
		// failure. Retain the receipt and require review instead of guessing.
		msg := "delivered run absent from T3 recent runs; review in T3"
		return s.store.ObserveContinuation(ctx, c, db.ContinuationBlocked, []db.WaitReason{db.WaitDeliveryUncertain}, &msg, false)
	}
	current := sessionState(d)
	switch {
	case d.Archived || d.ParentThreadID != "":
		state = db.ContinuationBlocked
		reasons = append(reasons, db.WaitThreadMissing)
	case failedSession(current):
		state = db.ContinuationBlocked
		reasons = append(reasons, db.WaitTargetFailed)
	case current == db.StateNeedsInput:
		reasons = append(reasons, db.WaitTargetNeedsInput)
	case current == db.StateRunning:
		reasons = append(reasons, db.WaitTargetRunning)
	case completed:
		state = db.ContinuationAwaitingSettle
		reasons = append(reasons, db.WaitPredecessorUnsettled)
		if d.Settled && d.SettledAt != nil && c.SentAt != nil && !d.SettledAt.Before(*c.SentAt) {
			state = db.ContinuationSettled
			reasons = []db.WaitReason{}
		}
	}
	return s.store.ObserveContinuation(ctx, c, state, reasons, nil, completed)
}
