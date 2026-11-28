// Package steering delivers a steered tree's events to its steerer session
// as coalesced digests and finishes the steerer when the tree finishes
// (ADR 0013 "Delivery" and "Tree finish", docs/api.md "Steering delivery").
package steering

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/coordinator"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/workevent"
)

// Coordinator sends the steerer its digests and lists a member's requests
// (coordinator.Service).
type Coordinator interface {
	SendRecorded(ctx context.Context, id int64, m execution.Message, record func(context.Context) error) (db.Task, error)
	Requests(ctx context.Context, id int64) ([]execution.Request, error)
}

const (
	// Debounce: deliver once this long passed since the newest pending event.
	Debounce = 10 * time.Second
	// MaxWait: deliver once this long passed since the oldest pending event.
	MaxWait = 60 * time.Second
	// MaxDigest bounds a digest (bytes): a send's limit.
	MaxDigest = coordinator.MaxMessage
	// NotRunning is the binding's last error while its steerer can't take
	// updates (failed, interrupted, cancelled, backlog or launching).
	NotRunning = "steerer not running"

	firstBackoff = 10 * time.Second
	maxBackoff   = 5 * time.Minute
	eventPage    = 200
)

// Dispatcher delivers every active binding's pending events: one delivery
// in flight per tree (reconciles are serialized), at least once (a crash
// between the send and its record sends the same request id again).
type Dispatcher struct {
	store *db.Store
	coord Coordinator
	log   *slog.Logger

	// Every is the tick interval.
	Every time.Duration
	// Now is the clock (tests override it).
	Now func() time.Time
	// SendTimeout bounds one send.
	SendTimeout time.Duration

	// Record stores a delivered digest (store.RecordSteeringDelivery; tests
	// replace it to crash between the send and its record).
	Record func(ctx context.Context, id, through, finalRevision int64) (bool, error)

	mu       sync.Mutex
	kick     chan struct{}
	bindings map[int64]*binding
}

// binding is what the dispatcher remembers of one binding between passes.
// Coalescing times are when this dispatcher first saw the pending events:
// after a restart the windows start again.
type binding struct {
	seen           int64 // highest pending event id seen
	oldest, newest time.Time
	failures       int
	retryAt        time.Time
}

func New(store *db.Store, coord Coordinator, log *slog.Logger) *Dispatcher {
	if log == nil {
		log = slog.Default()
	}
	return &Dispatcher{
		store: store, coord: coord, log: log.With("component", "steering"),
		Every: 10 * time.Second, Now: time.Now, SendTimeout: 2 * time.Minute,
		Record:   store.RecordSteeringDelivery,
		kick:     make(chan struct{}, 1),
		bindings: map[int64]*binding{},
	}
}

// Kick schedules a pass (a steering event committed). Never blocks.
func (d *Dispatcher) Kick() {
	select {
	case d.kick <- struct{}{}:
	default:
	}
}

// OnTransition kicks on task transitions: a steerer that became deliverable
// or reached done after the final digest.
func (d *Dispatcher) OnTransition(context.Context, workevent.TaskChange) { d.Kick() }

// Run passes on start, on every kick, every Every and when a coalescing
// window closes, until ctx is done.
func (d *Dispatcher) Run(ctx context.Context) {
	t := time.NewTimer(0)
	defer t.Stop()
	for {
		next, err := d.reconcile(ctx)
		if err != nil && ctx.Err() == nil {
			d.log.Warn("deliver steering", "err", err)
		}
		wait := d.Every
		if !next.IsZero() {
			wait = min(wait, max(next.Sub(d.Now()), 50*time.Millisecond))
		}
		t.Reset(wait)
		select {
		case <-ctx.Done():
			return
		case <-d.kick:
		case <-t.C:
		}
	}
}

// Reconcile delivers every due digest, finishes bindings whose tree
// finished and decides awaiting auto-completes.
func (d *Dispatcher) Reconcile(ctx context.Context) error {
	_, err := d.reconcile(ctx)
	return err
}

// reconcile is Reconcile, also returning the earliest time a window closes
// or a backoff ends (zero: none).
func (d *Dispatcher) reconcile(ctx context.Context) (time.Time, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var next time.Time
	soonest := func(t time.Time) {
		if !t.IsZero() && (next.IsZero() || t.Before(next)) {
			next = t
		}
	}
	states, err := d.store.ActiveSteerers(ctx)
	if err != nil {
		return next, err
	}
	var errs []error
	live := map[int64]bool{}
	for _, st := range states {
		if ctx.Err() != nil {
			break
		}
		live[st.ID] = true
		at, err := d.steer(ctx, st)
		if err != nil {
			errs = append(errs, fmt.Errorf("binding %d (tree %d): %w", st.ID, st.TreeID, err))
		}
		soonest(at)
	}
	for id := range d.bindings {
		if !live[id] {
			delete(d.bindings, id)
		}
	}
	awaiting, err := d.store.SteerersAwaitingCompletion(ctx)
	if err != nil {
		return next, errors.Join(append(errs, err)...)
	}
	for _, c := range awaiting {
		done, err := d.store.AutoCompleteSteerer(ctx, c)
		if err != nil {
			errs = append(errs, fmt.Errorf("auto-complete steerer #%d: %w", c.TaskID, err))
			continue
		}
		if done {
			d.log.Info("steerer completed: tree finished", "task", c.TaskID, "tree", c.TreeID)
		}
	}
	return next, errors.Join(errs...)
}

// deliverable: the steerer can take an update (ADR 0013: running, done or
// waiting on the user).
func deliverable(t db.Task) bool {
	switch t.State {
	case db.StateRunning, db.StateDone, db.StateNeedsInput:
		return t.Session != nil
	}
	return false
}

// steer delivers st's pending events when due, returning when to look again.
func (d *Dispatcher) steer(ctx context.Context, st db.SteererState) (time.Time, error) {
	now := d.Now()
	b := d.bindings[st.ID]
	if b == nil {
		b = &binding{}
		d.bindings[st.ID] = b
	}
	final := st.FinishEventID != nil
	task, err := d.store.GetTask(ctx, st.TaskID)
	if err != nil {
		return time.Time{}, err
	}
	if !deliverable(task) {
		if final {
			d.log.Info("tree finished; steerer not running: steering ends", "tree", st.TreeID, "task", st.TaskID)
			_, err := d.store.FinishSteering(ctx, st.ID)
			return time.Time{}, err
		}
		if st.LastError == nil || *st.LastError != NotRunning {
			msg := NotRunning
			return time.Time{}, d.store.SetSteeringError(ctx, st.ID, &msg)
		}
		return time.Time{}, nil
	}
	if st.LastError != nil && *st.LastError == NotRunning {
		if err := d.store.SetSteeringError(ctx, st.ID, nil); err != nil {
			return time.Time{}, err
		}
	}
	pending, err := d.store.SteeringPending(ctx, st.ID, st.DeliveredThrough)
	if err != nil || pending.Count == 0 {
		return time.Time{}, err
	}
	if b.oldest.IsZero() {
		b.oldest = now
	}
	if pending.LastID > b.seen {
		b.seen, b.newest = pending.LastID, now
	}
	if now.Before(b.retryAt) {
		return b.retryAt, nil
	}
	if !final {
		due := b.newest.Add(Debounce)
		if latest := b.oldest.Add(MaxWait); latest.Before(due) {
			due = latest
		}
		if now.Before(due) {
			return due, nil
		}
	} else if task.State == db.StateRunning {
		// The final update waits for the steerer's turn to end, so the
		// done that follows is the turn that read it (auto-complete).
		return time.Time{}, nil
	}
	text, err := d.compose(ctx, st, task, pending)
	if err != nil {
		return time.Time{}, err
	}
	through := pending.LastID
	m := execution.Message{Text: text, RequestID: fmt.Sprintf("steering-%d-%d", st.ID, through)}
	sendCtx, cancel := context.WithTimeout(ctx, d.SendTimeout)
	defer cancel()
	_, err = d.coord.SendRecorded(sendCtx, st.TaskID, m, func(record context.Context) error {
		_, err := d.Record(record, st.ID, through, task.StateRevision)
		return err
	})
	var conflict *db.ConflictError
	if errors.As(err, &conflict) && conflict.Code == "session_busy" {
		return time.Time{}, nil // retry on the next kick or tick
	}
	if err != nil {
		b.failures++
		b.retryAt = now.Add(backoff(b.failures))
		msg := err.Error()
		if setErr := d.store.SetSteeringError(ctx, st.ID, &msg); setErr != nil {
			return b.retryAt, errors.Join(err, setErr)
		}
		return b.retryAt, err
	}
	d.log.Info("steering update sent", "tree", st.TreeID, "task", st.TaskID, "through", through, "final", final)
	delete(d.bindings, st.ID)
	return time.Time{}, nil
}

// backoff is the wait after n consecutive failures: 10s doubling, at most
// 5 minutes.
func backoff(n int) time.Duration {
	wait := firstBackoff
	for i := 1; i < n && wait < maxBackoff; i++ {
		wait *= 2
	}
	return min(wait, maxBackoff)
}

// compose is st's digest of pending events.
func (d *Dispatcher) compose(ctx context.Context, st db.SteererState, steerer db.Task, pending db.SteeringPending) (string, error) {
	dg := digest{
		Tree: st.TreeID, Through: pending.LastID, Steerer: st.TaskID,
		Pending: pending.Count, FirstID: pending.FirstID, LastID: pending.LastID,
		Final: st.FinishEventID != nil, Waiting: steerer.State == db.StateNeedsInput,
	}
	summaries := map[int64]string{}
	size := 0
	for after := st.DeliveredThrough; after < pending.LastID && size < MaxDigest; {
		events, _, err := d.store.SteeringEvents(ctx, st.TreeID, after, eventPage)
		if err != nil {
			return "", err
		}
		if len(events) == 0 {
			break
		}
		for _, e := range events {
			after = e.ID
			if e.ID > pending.LastID {
				break
			}
			if e.Self || e.SteererID != st.ID {
				continue
			}
			summary := ""
			if e.Kind == db.SteerMemberNeedsInput && e.TaskID != nil {
				summary = d.requests(ctx, *e.TaskID, e, summaries)
			}
			line := eventLine{ID: e.ID, Text: eventText(e, summary)}
			size += len(line.Text) + 1
			dg.Lines = append(dg.Lines, line)
		}
	}
	return dg.render(MaxDigest), nil
}

// requests summarizes what member task waits on for its needs_input event
// e: the coordinator's request listing while the member still needs input
// at e's revision, once per task and revision.
func (d *Dispatcher) requests(ctx context.Context, task int64, e db.SteeringEvent, seen map[int64]string) string {
	rev := eventRevision(e)
	t, err := d.store.GetTask(ctx, task)
	if err != nil {
		return "member unavailable: " + oneLine(err.Error(), 200)
	}
	if t.State != db.StateNeedsInput || t.StateRevision != rev {
		return fmt.Sprintf("moved on since (now %s at stateRevision %d)", t.State, t.StateRevision)
	}
	if t.AnsweredRevision != nil && *t.AnsweredRevision == rev {
		return "already answered at this revision"
	}
	if s, ok := seen[task]; ok {
		return s
	}
	rs, err := d.coord.Requests(ctx, task)
	s := noRequests
	if err == nil && len(rs) > 0 {
		parts := make([]string, 0, len(rs))
		for _, r := range rs {
			parts = append(parts, fmt.Sprintf("%s %s %q", r.ID, r.Kind, oneLine(r.Title, 160)))
		}
		s = "requests: " + strings.Join(parts, "; ")
	}
	seen[task] = s
	return s
}
