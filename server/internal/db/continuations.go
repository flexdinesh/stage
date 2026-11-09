package db

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type ContinuationState string

const (
	ContinuationWaiting        ContinuationState = "waiting"
	ContinuationPaused         ContinuationState = "paused"
	ContinuationBlocked        ContinuationState = "blocked"
	ContinuationSending        ContinuationState = "sending"
	ContinuationRunning        ContinuationState = "running"
	ContinuationAwaitingSettle ContinuationState = "awaiting_settle"
	ContinuationSettled        ContinuationState = "settled"
	ContinuationCancelled      ContinuationState = "cancelled"
	ContinuationUncertain      ContinuationState = "uncertain"
)

type WaitReason string

const (
	WaitPredecessorUnsettled WaitReason = "predecessor_unsettled"
	WaitTargetRunning        WaitReason = "target_running"
	WaitTargetNeedsInput     WaitReason = "target_needs_input"
	WaitPredecessorFailed    WaitReason = "predecessor_failed"
	WaitTargetFailed         WaitReason = "target_failed"
	WaitUpstreamPending      WaitReason = "upstream_pending"
	WaitUpstreamCancelled    WaitReason = "upstream_cancelled"
	WaitThreadMissing        WaitReason = "thread_missing"
	WaitT3Offline            WaitReason = "t3_offline"
	WaitDeliveryUncertain    WaitReason = "delivery_uncertain"
	WaitLegacyConflict       WaitReason = "legacy_conflict"
)

type Continuation struct {
	OrderID                string            `json:"-"`
	ID                     int64             `json:"id"`
	AfterThreadID          string            `json:"afterThreadId"`
	TargetThreadID         string            `json:"targetThreadId"`
	PreviousContinuationID *int64            `json:"previousContinuationId"`
	Prompt                 string            `json:"prompt"`
	State                  ContinuationState `json:"state"`
	WaitReasons            []WaitReason      `json:"waitReasons"`
	LastError              *string           `json:"lastError"`
	RunID                  *string           `json:"runId"`
	MessageID              *string           `json:"messageId"`
	CreatedAt              time.Time         `json:"createdAt"`
	UpdatedAt              time.Time         `json:"updatedAt"`
	SentAt                 *time.Time        `json:"sentAt"`
	SettledAt              *time.Time        `json:"settledAt"`
	AfterThread            Thread            `json:"afterThread"`
	TargetThread           Thread            `json:"targetThread"`
	RequestID              *string           `json:"-"`
	DeliveryPrompt         *string           `json:"-"`
	ClaimExpiresAt         *time.Time        `json:"-"`
	RunCompleted           bool              `json:"-"`
}

const continuationSelect = `SELECT id, after_thread_id, target_thread_id, previous_continuation_id,
 prompt, state, wait_reasons, last_error, run_id, message_id, created_at, updated_at,
 sent_at, settled_at, request_id, delivery_prompt, claim_expires_at, run_completed, order_id::text FROM continuations`

func scanContinuation(row pgx.Row) (Continuation, error) {
	var c Continuation
	err := row.Scan(&c.ID, &c.AfterThreadID, &c.TargetThreadID, &c.PreviousContinuationID, &c.Prompt,
		&c.State, &c.WaitReasons, &c.LastError, &c.RunID, &c.MessageID, &c.CreatedAt, &c.UpdatedAt,
		&c.SentAt, &c.SettledAt, &c.RequestID, &c.DeliveryPrompt, &c.ClaimExpiresAt, &c.RunCompleted, &c.OrderID)
	if c.WaitReasons == nil {
		c.WaitReasons = []WaitReason{}
	}
	return c, notFound(err)
}

func (s *Store) hydrateContinuation(ctx context.Context, c Continuation) (Continuation, error) {
	var err error
	c.AfterThread, err = s.GetThread(ctx, c.AfterThreadID)
	if err != nil {
		return c, err
	}
	c.TargetThread, err = s.GetThread(ctx, c.TargetThreadID)
	return c, err
}

func (s *Store) GetContinuation(ctx context.Context, id int64) (Continuation, error) {
	c, err := scanContinuation(s.pool.QueryRow(ctx, continuationSelect+` WHERE id=$1`, id))
	if err != nil {
		return c, err
	}
	return s.hydrateContinuation(ctx, c)
}

// ListContinuations returns connected orders, predecessor first, retaining history.
func (s *Store) ListContinuations(ctx context.Context, thread string) ([]Continuation, error) {
	rows, err := s.pool.Query(ctx, continuationSelect+` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	all, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Continuation, error) { return scanContinuation(row) })
	if err != nil {
		return nil, err
	}
	keep := map[string]bool{}
	for _, c := range all {
		if c.AfterThreadID == thread || c.TargetThreadID == thread {
			keep[c.OrderID] = true
		}
	}
	byID := map[int64]Continuation{}
	for _, c := range all {
		byID[c.ID] = c
	}
	depth := func(c Continuation) int {
		d := 0
		for c.PreviousContinuationID != nil && d < len(all) {
			c = byID[*c.PreviousContinuationID]
			d++
		}
		return d
	}
	root := func(c Continuation) int64 {
		for n := 0; c.PreviousContinuationID != nil && n < len(all); n++ {
			c = byID[*c.PreviousContinuationID]
		}
		return c.ID
	}
	sort.SliceStable(all, func(i, j int) bool {
		if root(all[i]) != root(all[j]) {
			return root(all[i]) < root(all[j])
		}
		return depth(all[i]) < depth(all[j])
	})
	out := []Continuation{}
	for _, c := range all {
		if thread != "" && !keep[c.OrderID] {
			continue
		}
		c, err = s.hydrateContinuation(ctx, c)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func legacyConflict(ctx context.Context, tx pgx.Tx, ids []string) (bool, error) {
	var found bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks t WHERE t.t3_thread_id=ANY($1)
  AND (t.after_task_id IS NOT NULL OR EXISTS(SELECT 1 FROM tasks f WHERE f.after_task_id=t.id)))`, ids).Scan(&found)
	return found, err
}

func (s *Store) HasLegacyConflict(ctx context.Context, ids ...string) (bool, error) {
	var found bool
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error { var err error; found, err = legacyConflict(ctx, tx, ids); return err })
	return found, err
}

// continuationConflict is also used by legacy setAfter under the same lock.
func continuationConflict(ctx context.Context, tx pgx.Tx, taskIDs []int64) error {
	var found bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM continuations c JOIN tasks t
 ON t.t3_thread_id IN (c.after_thread_id,c.target_thread_id)
 WHERE t.id=ANY($1) AND EXISTS (SELECT 1 FROM continuations live
 WHERE live.order_id=c.order_id AND live.state NOT IN ('settled','cancelled')))`, taskIDs).Scan(&found)
	if err != nil {
		return err
	}
	if found {
		return Conflict("session belongs to a queued continuation order")
	}
	return nil
}

func (s *Store) CreateContinuation(ctx context.Context, after, target, prompt string) (Continuation, error) {
	if strings.TrimSpace(after) == "" || strings.TrimSpace(target) == "" || strings.TrimSpace(prompt) == "" {
		return Continuation{}, Invalid("afterThreadId, targetThreadId and prompt are required")
	}
	if after == target {
		return Continuation{}, Conflict("a session cannot run after itself")
	}
	var id int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockLinks(ctx, tx); err != nil {
			return err
		}
		for _, thread := range []string{after, target} {
			var parent *string
			var archived bool
			if err := tx.QueryRow(ctx, `SELECT parent_thread_id,archived FROM threads WHERE t3_thread_id=$1`, thread).Scan(&parent, &archived); err != nil {
				return notFound(err)
			}
			if parent != nil || archived {
				return Conflict("only unarchived top-level sessions can join an order")
			}
		}
		conflict, err := legacyConflict(ctx, tx, []string{after, target})
		if err != nil {
			return err
		}
		if conflict {
			return Conflict("unlink legacy task order before adding a continuation")
		}

		// Reserve sessions for the lifetime of their execution order, including
		// cancelled ancestors with live descendants. Completed orders release
		// membership; their history never becomes a prerequisite of a new run.
		active := []string{}
		for _, thread := range []string{after, target} {
			var order *string
			if err := tx.QueryRow(ctx, `SELECT (SELECT DISTINCT c.order_id::text FROM continuations c
		 WHERE $1 IN (c.after_thread_id,c.target_thread_id) AND EXISTS(
		 SELECT 1 FROM continuations live WHERE live.order_id=c.order_id AND live.state NOT IN ('settled','cancelled')))`, thread).Scan(&order); err != nil {
				return err
			}
			if order != nil && !slices.Contains(active, *order) {
				active = append(active, *order)
			}
		}
		var order string
		if len(active) > 0 {
			order = active[0]
		} else {
			if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&order); err != nil {
				return err
			}
		}
		var occupied bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM continuations WHERE order_id::text=ANY($3)
		 AND (after_thread_id=$1 OR target_thread_id=$2))`, after, target, active).Scan(&occupied); err != nil {
			return err
		}
		if occupied {
			return Conflict("session already has a predecessor or successor in its live order")
		}
		var cycle bool
		if err := tx.QueryRow(ctx, `WITH RECURSIVE down AS (
		 SELECT target_thread_id FROM continuations WHERE after_thread_id=$1 AND order_id::text=ANY($3)
		 UNION SELECT c.target_thread_id FROM continuations c JOIN down d ON c.after_thread_id=d.target_thread_id WHERE c.order_id::text=ANY($3))
		 SELECT EXISTS(SELECT 1 FROM down WHERE target_thread_id=$2)`, target, after, active).Scan(&cycle); err != nil {
			return err
		}
		if cycle {
			return Conflict("continuation order cannot loop")
		}
		var previous *int64
		if err := tx.QueryRow(ctx, `SELECT (SELECT id FROM continuations WHERE target_thread_id=$1 AND order_id::text=ANY($2))`, after, active).Scan(&previous); err != nil {
			return err
		}
		var successor *int64
		var immutable bool
		if err := tx.QueryRow(ctx, `SELECT (SELECT id FROM continuations WHERE after_thread_id=$1 AND order_id::text=ANY($2)),
		 EXISTS(SELECT 1 FROM continuations WHERE after_thread_id=$1 AND order_id::text=ANY($2) AND (request_id IS NOT NULL OR state='cancelled'))`, target, active).Scan(&successor, &immutable); err != nil {
			return err
		}
		if immutable {
			return Conflict("cannot prepend to a delivered or cancelled continuation")
		}
		if len(active) > 1 {
			if _, err := tx.Exec(ctx, `UPDATE continuations SET order_id=$1::uuid,updated_at=now() WHERE order_id::text=ANY($2)`, order, active); err != nil {
				return err
			}
		}
		err = tx.QueryRow(ctx, `INSERT INTO continuations(order_id,after_thread_id,target_thread_id,previous_continuation_id,prompt,wait_reasons)
		 VALUES($1::uuid,$2,$3,$4,$5,ARRAY['predecessor_unsettled']) RETURNING id`, order, after, target, previous, prompt).Scan(&id)
		if err != nil {
			return err
		}

		if successor != nil {
			if _, err := tx.Exec(ctx, `UPDATE continuations SET previous_continuation_id=$2,state=CASE WHEN state='paused' THEN state ELSE 'waiting' END,
    wait_reasons=ARRAY['upstream_pending'],updated_at=now() WHERE id=$1`, *successor, id); err != nil {
				return err
			}
			if err := continuationEvent(ctx, tx, *successor, "waiting", "upstream continuation attached"); err != nil {
				return err
			}
		}
		return continuationEvent(ctx, tx, id, "waiting", "created")
	})
	if err != nil {
		return Continuation{}, err
	}
	return s.GetContinuation(ctx, id)
}

func continuationEvent(ctx context.Context, tx pgx.Tx, id int64, state ContinuationState, detail string) error {
	_, err := tx.Exec(ctx, `INSERT INTO continuation_events(continuation_id,state,detail) VALUES($1,$2,$3)`, id, state, detail)
	return err
}

func (s *Store) MutateContinuation(ctx context.Context, id int64, action string, prompt *string) (Continuation, error) {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockLinks(ctx, tx); err != nil {
			return err
		}
		c, err := scanContinuation(tx.QueryRow(ctx, continuationSelect+` WHERE id=$1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		if c.RequestID != nil || c.State == ContinuationCancelled || c.State == ContinuationSettled || c.State == ContinuationUncertain {
			return Conflict("continuation is %s; delivery is already claimed or final", c.State)
		}
		state := c.State
		reasons := c.WaitReasons
		switch action {
		case "edit":
			if prompt == nil || strings.TrimSpace(*prompt) == "" {
				return Invalid("prompt is required")
			}
		case "pause":
			state = ContinuationPaused
		case "resume":
			state = ContinuationWaiting
			reasons = []WaitReason{WaitPredecessorUnsettled}
		case "cancel":
			state = ContinuationCancelled
			reasons = []WaitReason{}
		default:
			return Invalid("invalid continuation action")
		}
		if _, err := tx.Exec(ctx, `UPDATE continuations SET prompt=COALESCE($2,prompt),state=$3,wait_reasons=$4,last_error=NULL,updated_at=now() WHERE id=$1`, id, prompt, state, reasons); err != nil {
			return err
		}
		if err := continuationEvent(ctx, tx, id, state, action); err != nil {
			return err
		}
		if action == "cancel" {
			rows, err := tx.Query(ctx, `WITH RECURSIVE down AS (
    SELECT id FROM continuations WHERE previous_continuation_id=$1
    UNION SELECT c.id FROM continuations c JOIN down d ON c.previous_continuation_id=d.id)
    UPDATE continuations SET state='blocked',wait_reasons=ARRAY['upstream_cancelled'],
      last_error='upstream continuation cancelled',updated_at=now()
    WHERE id IN (SELECT id FROM down) AND request_id IS NULL AND state<>'cancelled' RETURNING id`, id)
			if err != nil {
				return err
			}
			ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
			if err != nil {
				return err
			}
			for _, child := range ids {
				if err := continuationEvent(ctx, tx, child, ContinuationBlocked, "upstream continuation cancelled"); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return Continuation{}, err
	}
	return s.GetContinuation(ctx, id)
}

// ObserveContinuation uses the loaded revision, so it cannot overwrite an
// edit, cancellation, pause, new prerequisite, or another worker's claim.
func (s *Store) ObserveContinuation(ctx context.Context, c Continuation, state ContinuationState, reasons []WaitReason, lastError *string, completed bool) error {
	if reasons == nil {
		reasons = []WaitReason{}
	}
	sameError := (c.LastError == nil && lastError == nil) || (c.LastError != nil && lastError != nil && *c.LastError == *lastError)
	if c.State == state && slices.Equal(c.WaitReasons, reasons) && sameError && c.RunCompleted == completed {
		return nil
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE continuations SET state=$3,wait_reasons=$4,last_error=$5,run_completed=$6,
    settled_at=CASE WHEN $3='settled' THEN now() ELSE settled_at END,updated_at=now()
    WHERE id=$1 AND updated_at=$2`, c.ID, c.UpdatedAt, state, reasons, lastError, completed)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		return continuationEvent(ctx, tx, c.ID, state, fmt.Sprint(reasons))
	})
}

// ClaimContinuation persists the exact envelope and request id before the
// network call. Claims never expire back into the sendable state.
func (s *Store) ClaimContinuation(ctx context.Context, c Continuation, envelope string) (Continuation, bool, error) {
	claimed := false
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockLinks(ctx, tx); err != nil {
			return err
		}
		conflict, err := legacyConflict(ctx, tx, []string{c.AfterThreadID, c.TargetThreadID})
		if err != nil {
			return err
		}
		if conflict {
			return nil
		}
		tag, err := tx.Exec(ctx, `UPDATE continuations c SET state='sending',wait_reasons='{}',last_error=NULL,
    request_id='continuation-'||id||'-'||gen_random_uuid(),delivery_prompt=$3,
    claim_expires_at=now()+interval '2 minutes',updated_at=now()
   WHERE id=$1 AND updated_at=$2 AND state IN ('waiting','blocked') AND request_id IS NULL
    AND (previous_continuation_id IS NULL OR EXISTS(
      SELECT 1 FROM continuations p WHERE p.id=c.previous_continuation_id AND p.state='settled'))`, c.ID, c.UpdatedAt, envelope)
		if err != nil {
			return err
		}
		claimed = tag.RowsAffected() == 1
		if claimed {
			return continuationEvent(ctx, tx, c.ID, ContinuationSending, "delivery claimed")
		}
		return nil
	})
	if err != nil || !claimed {
		return c, claimed, err
	}
	c, err = s.GetContinuation(ctx, c.ID)
	return c, true, err
}

func (s *Store) RecordContinuationDelivery(ctx context.Context, c Continuation, runID, messageID string, sendErr error) error {
	state := ContinuationRunning
	reasons := []WaitReason{}
	var lastError *string
	if sendErr != nil || runID == "" || messageID == "" {
		state = ContinuationUncertain
		reasons = []WaitReason{WaitDeliveryUncertain}
		msg := "T3 accepted delivery without usable run and message IDs; review in T3"
		if sendErr != nil {
			msg = sendErr.Error() + "; delivery may have occurred; review in T3"
		}
		lastError = &msg
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE continuations SET state=$3,run_id=$4,message_id=$5,last_error=$6,wait_reasons=$7,
    sent_at=CASE WHEN $8 THEN updated_at ELSE sent_at END,claim_expires_at=NULL,updated_at=now()
    WHERE id=$1 AND request_id=$2 AND state='sending'`, c.ID, c.RequestID, state, nullIfEmpty(runID), nullIfEmpty(messageID), lastError, reasons, sendErr == nil)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		return continuationEvent(ctx, tx, c.ID, state, "delivery result recorded")
	})
}

func (s *Store) ExpireContinuationClaims(ctx context.Context) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `UPDATE continuations SET state='uncertain',wait_reasons=ARRAY['delivery_uncertain'],
    last_error='delivery claim expired; delivery may have occurred; review in T3',updated_at=now()
    WHERE state='sending' AND claim_expires_at<now() RETURNING id`)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err := continuationEvent(ctx, tx, id, ContinuationUncertain, "delivery claim expired"); err != nil {
				return err
			}
		}
		return nil
	})
}
