package notify

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/workevent"
)

// Holds releases held needs_input notifications (ADR 0013, docs/api.md
// "Notification rules"): at expiry when still unanswered, at once when the
// steerer can no longer answer, never when answered first. Holds are
// durable: Run re-evaluates them on start.
type Holds struct {
	n   *Notifier
	log *slog.Logger

	// CanAnswer reports whether a connection offers respond or send (nil:
	// every connection does). An error keeps the hold until the next check.
	CanAnswer func(ctx context.Context, connectionID int64) (bool, error)
	// Every is the tick interval.
	Every time.Duration
	// Now is the clock (tests override it).
	Now func() time.Time
	// AnswerTTL caches CanAnswer per connection.
	AnswerTTL time.Duration

	kick    chan struct{}
	mu      sync.Mutex
	answers map[int64]answerCheck
}

type answerCheck struct {
	ok bool
	at time.Time
}

// NewHolds returns the release loop of n's held notifications; set it as
// n.Holds so new needs_input notifications are held.
func NewHolds(n *Notifier) *Holds {
	return &Holds{
		n: n, log: n.log.With("component", "notification-holds"),
		Every: 5 * time.Second, Now: time.Now, AnswerTTL: 30 * time.Second,
		kick: make(chan struct{}, 1), answers: map[int64]answerCheck{},
	}
}

// Kick schedules a reconcile (a new hold, a transition, a steering change).
func (h *Holds) Kick() {
	select {
	case h.kick <- struct{}{}:
	default:
	}
}

// Run reconciles on start, on every kick and every Every until ctx is done.
func (h *Holds) Run(ctx context.Context) {
	t := time.NewTicker(h.Every)
	defer t.Stop()
	for {
		if err := h.Reconcile(ctx); err != nil && ctx.Err() == nil {
			h.log.Warn("reconcile notification holds", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-h.kick:
		}
	}
}

// Reconcile ends every held notification whose wait is over.
func (h *Holds) Reconcile(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	held, err := h.n.store.HeldHolds(ctx)
	if err != nil {
		return err
	}
	now := h.Now()
	for _, x := range held {
		outcome, reason, note, act := h.decide(ctx, x, now)
		if !act {
			continue
		}
		r, err := h.n.store.ReleaseHold(ctx, x.ID, outcome, reason, note, h.n.heldRecord)
		if err != nil {
			if ctx.Err() != nil {
				return err
			}
			h.log.Warn("release notification hold", "hold", x.ID, "task", x.TaskID, "err", err)
			continue
		}
		h.n.publishHeld(r)
	}
	return nil
}

// decide is how held x ends now (act false: it keeps waiting). A wait that
// ended is cancelled by ReleaseHold's own recheck.
func (h *Holds) decide(ctx context.Context, x db.HeldHold, now time.Time) (db.HoldState, *string, *string, bool) {
	released := func(reason string) (db.HoldState, *string, *string, bool) {
		return db.HoldReleased, &reason, nil, true
	}
	switch {
	case x.TaskState != db.StateNeedsInput || x.TaskRevision != x.StateRevision ||
		x.AnsweredRevision != nil && *x.AnsweredRevision == x.StateRevision:
		return db.HoldCancelled, nil, nil, true
	case x.SteererEnded || x.SteererTaskID == nil:
		return released(db.HoldReasonSteererEnded)
	case x.SteererState == nil || !db.SteererCanAnswer(*x.SteererState):
		return released(db.HoldReasonSteererInactive)
	case x.TaskTreeID == nil || *x.TaskTreeID != x.SteererTreeID:
		return released(db.HoldReasonLeftTree)
	case !h.canAnswer(ctx, x.ConnectionID, now):
		return released(db.HoldReasonUnanswerable)
	case !now.Before(x.Until):
		note := db.HoldNoteTimeout
		return db.HoldEscalated, nil, &note, true
	}
	return "", nil, nil, false
}

// canAnswer is CanAnswer for connection id, cached AnswerTTL; a failed
// check answers true (keep the hold) and is not cached.
func (h *Holds) canAnswer(ctx context.Context, id int64, now time.Time) bool {
	if h.CanAnswer == nil {
		return true
	}
	if c, ok := h.answers[id]; ok && now.Sub(c.at) < h.AnswerTTL {
		return c.ok
	}
	ok, err := h.CanAnswer(ctx, id)
	if err != nil {
		if ctx.Err() == nil {
			h.log.Warn("check whether a connection can answer; keeping its holds", "connection", id, "err", err)
		}
		return true
	}
	h.answers[id] = answerCheck{ok: ok, at: now}
	return ok
}

// Escalate is escalate_to_user from steerer task callerTask on task id
// (db.EscalateTask), then signals the inbox and pushes.
func (h *Holds) Escalate(ctx context.Context, id, callerTask int64, reason string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, err := h.n.store.EscalateTask(ctx, id, callerTask, reason, h.n.heldRecord)
	if err != nil {
		return err
	}
	h.n.publishHeld(r)
	return nil
}

// hold holds task change c's needs_input notification on store (the root
// Store, or one joining the observation transaction) and reports whether it
// is held.
func (h *Holds) hold(ctx context.Context, store *db.Store, c *workevent.TaskChange) (bool, error) {
	if c == nil || c.To != db.StateNeedsInput {
		return false, nil
	}
	return store.HoldNeedsInput(ctx, c.TaskID, h.Now())
}

// heldRecord is a released hold's notification, under the filter handle
// applies: personal mode records only enabled events (a failed read
// notifies); shared access records every event.
func (n *Notifier) heldRecord(ctx context.Context, tx *db.Store, t db.Task) (*db.NewNotification, error) {
	if !n.SharedAccess {
		p, err := tx.GetPreferences(db.WithUser(ctx, db.DefaultUser))
		if err != nil && ctx.Err() == nil {
			n.log.Warn("read notification filter; notifying", "err", err)
		}
		if err == nil && !slices.Contains(p.NotifyEvents, db.StateNeedsInput) {
			return nil, nil
		}
	}
	rec, err := notificationRecord(ctx, tx, event{task: &workevent.TaskChange{TaskID: t.ID, To: db.StateNeedsInput}})
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// publishHeld signals the inbox and queues Web Push for a released hold's
// committed notification (none: nothing).
func (n *Notifier) publishHeld(r db.HoldRelease) {
	if r.Notification == nil {
		return
	}
	n.Inbox.Signal()
	n.enqueue(event{saved: r.Notification, savedID: r.NotificationID})
}
