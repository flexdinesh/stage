package observe

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

const readTimeout = 20 * time.Second

type RunnerOptions struct {
	PollEvery      time.Duration
	StalePollEvery time.Duration
	Log            *slog.Logger
}

// Runner samples a watch beside the adapters. The sink accepts records from
// both polls and streams and must honor cancellation.
type Runner struct {
	adapter func(int64) (execution.Adapter, error)
	emit    func(context.Context, Record) error
	options RunnerOptions

	mu        sync.Mutex
	ctx       context.Context
	watch     *Watch
	paused    bool
	workers   map[int64]*watchWorker
	attention map[int64]*attentionWatch
}

type watchWorker struct {
	entry     WatchConnection
	cancel    context.CancelFunc
	attention *attentionWatch
}

type attentionWatch struct {
	seeded atomic.Bool
	mu     sync.Mutex
	active map[string]bool
}

func NewRunner(adapter func(int64) (execution.Adapter, error), emit func(context.Context, Record) error, options RunnerOptions) *Runner {
	if options.PollEvery <= 0 {
		options.PollEvery = 5 * time.Second
	}
	if options.StalePollEvery <= 0 {
		options.StalePollEvery = time.Minute
	}
	if options.Log == nil {
		options.Log = slog.Default()
	}
	return &Runner{adapter: adapter, emit: emit, options: options,
		workers: make(map[int64]*watchWorker), attention: make(map[int64]*attentionWatch)}
}

// Run starts the latest watch and waits for cancellation. At most one call to
// Run may drive a Runner at a time.
func (r *Runner) Run(ctx context.Context) {
	r.mu.Lock()
	if r.ctx != nil {
		r.mu.Unlock()
		return
	}
	r.ctx = ctx
	if r.watch != nil && !r.paused {
		r.reconcile(r.watch.Connections, false, false)
	}
	r.mu.Unlock()

	<-ctx.Done()
	r.mu.Lock()
	r.reconcile(nil, false, false)
	r.ctx = nil
	r.mu.Unlock()
}

func sameWatchConnection(a, b WatchConnection) bool {
	return a.ID == b.ID && a.Attention == b.Attention && slices.Equal(a.Sessions, b.Sessions)
}

// Replace accepts a newer generation or an exact replay. Callers select the
// authoritative epoch. A new epoch resets the watch even at a lower generation.
func (r *Runner) Replace(watch Watch) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	newEpoch := r.watch == nil || r.watch.Epoch != watch.Epoch
	if !newEpoch && watch.Generation <= r.watch.Generation {
		return watch.Generation == r.watch.Generation && r.watch.Resample == watch.Resample &&
			slices.EqualFunc(r.watch.Connections, watch.Connections, sameWatchConnection)
	}
	watch.Connections = slices.Clone(watch.Connections)
	for i := range watch.Connections {
		watch.Connections[i].Sessions = slices.Clone(watch.Connections[i].Sessions)
	}
	r.watch = &watch
	keepAttention := make(map[int64]bool, len(watch.Connections))
	for _, conn := range watch.Connections {
		keepAttention[conn.ID] = conn.Attention
	}
	for id, attn := range r.attention {
		if !keepAttention[id] {
			delete(r.attention, id)
		} else if newEpoch {
			attn.seeded.Store(false)
		}
	}
	if r.ctx != nil && !r.paused {
		r.reconcile(watch.Connections, newEpoch, watch.Resample)
	}
	return true
}

// RetainAttention restores still-active discovery hints from the worker outbox.
func (r *Runner) RetainAttention(refs []execution.SessionRef) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.watch == nil {
		return
	}
	for _, ref := range refs {
		if slices.ContainsFunc(r.watch.Connections, func(c WatchConnection) bool { return c.ID == ref.ConnectionID && c.Attention }) {
			r.attentionFor(ref.ConnectionID).retain(ref.ExternalSessionID, true)
		}
	}
}

// Pause stops sampling while the adapter set is being reconciled.
func (r *Runner) Pause() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paused = true
	r.reconcile(nil, false, false)
}

// Resume starts the latest accepted watch after reconciliation.
func (r *Runner) Resume() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.paused && r.ctx != nil && r.watch != nil {
		r.reconcile(r.watch.Connections, false, false)
	}
	r.paused = false
}

// Reset restarts the watched connection after its adapter has been replaced.
func (r *Runner) Reset(connectionID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if worker := r.workers[connectionID]; worker != nil {
		worker.cancel()
		r.start(worker.entry)
	}
}

// reconcile and start are called with mu held.
func (r *Runner) reconcile(entries []WatchConnection, newEpoch, resample bool) {
	desired := make(map[int64]WatchConnection, len(entries))
	for _, entry := range entries {
		desired[entry.ID] = entry
	}
	for id, worker := range r.workers {
		entry, keep := desired[id]
		if keep && !newEpoch && !resample && sameWatchConnection(worker.entry, entry) {
			delete(desired, id)
			continue
		}
		worker.cancel()
		if !keep {
			delete(r.workers, id)
		}
	}
	for _, entry := range desired {
		r.start(entry)
	}
}

func (r *Runner) attentionFor(id int64) *attentionWatch {
	a := r.attention[id]
	if a == nil {
		a = &attentionWatch{active: make(map[string]bool)}
		r.attention[id] = a
	}
	return a
}

func (r *Runner) start(entry WatchConnection) {
	ctx, cancel := context.WithCancel(r.ctx)
	worker := &watchWorker{entry: entry, cancel: cancel}
	if entry.Attention {
		worker.attention = r.attentionFor(entry.ID)
	}
	r.workers[entry.ID] = worker
	go r.poll(ctx, worker)
	go r.stream(ctx, entry)
}

func (a *attentionWatch) retain(id string, active bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if active {
		a.active[id] = true
	} else {
		delete(a.active, id)
	}
}

func (a *attentionWatch) sessions() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	ids := make([]string, 0, len(a.active))
	for id := range a.active {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func (r *Runner) send(ctx context.Context, record Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	record.ObservedAt = time.Now().UTC()
	err := r.emit(ctx, record)
	if err != nil && ctx.Err() == nil {
		r.options.Log.Warn("emit observation", "connection", record.ConnectionID, "session", record.ExternalSessionID, "err", err)
	}
	return err
}

func (r *Runner) poll(ctx context.Context, worker *watchWorker) {
	var nextActive, nextStale time.Time
	for ctx.Err() == nil {
		now := time.Now()
		active, stale := !now.Before(nextActive), !now.Before(nextStale)
		if active {
			nextActive = now.Add(r.options.PollEvery)
		}
		if stale {
			nextStale = now.Add(r.options.StalePollEvery)
		}
		if active || stale {
			r.pollConnection(ctx, worker, active, stale)
		}
		wake := nextActive
		if nextStale.Before(wake) {
			wake = nextStale
		}
		timer := time.NewTimer(time.Until(wake))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (r *Runner) pollConnection(ctx context.Context, worker *watchWorker, active, stale bool) {
	entry := worker.entry
	a, err := r.adapter(entry.ID)
	if err != nil {
		return
	}
	seen := make(map[string]bool, len(entry.Sessions))
	owned := make(map[string]bool, len(entry.Sessions))
	read := func(ref execution.SessionRef, attention, quiet bool) error {
		if seen[ref.ExternalSessionID] || ctx.Err() != nil {
			return nil
		}
		seen[ref.ExternalSessionID] = true
		o, err := r.read(ctx, a, ref, attention, quiet)
		if attention && o != nil && err == nil && ctx.Err() == nil {
			worker.attention.retain(ref.ExternalSessionID, o.Active)
		}
		return err
	}
	for _, session := range entry.Sessions {
		if session.Attention {
			continue
		}
		owned[session.ID] = true
		if (session.Stale && !stale) || (!session.Stale && !active) {
			continue
		}
		_ = read(execution.SessionRef{ConnectionID: entry.ID, ExternalSessionID: session.ID}, false, false)
	}
	if !active || !entry.Attention || ctx.Err() != nil {
		return
	}

	call, cancel := context.WithTimeout(ctx, readTimeout)
	health := a.Health(call)
	err = call.Err()
	cancel()
	if err != nil || !health.Available {
		return
	}
	call, cancel = context.WithTimeout(ctx, readTimeout)
	refs, err := a.Attention(call)
	if err == nil {
		err = call.Err()
	}
	cancel()
	if err != nil {
		return
	}
	quiet := !worker.attention.seeded.Load()
	succeeded := true
	for _, ref := range refs {
		if ref.ConnectionID == entry.ID && ref.ExternalSessionID != "" && !owned[ref.ExternalSessionID] && read(ref, true, quiet) != nil {
			succeeded = false
		}
	}
	for _, session := range entry.Sessions {
		if session.Attention && read(execution.SessionRef{ConnectionID: entry.ID, ExternalSessionID: session.ID}, true, quiet) != nil {
			succeeded = false
		}
	}
	for _, id := range worker.attention.sessions() {
		if owned[id] {
			worker.attention.retain(id, false)
		} else if read(execution.SessionRef{ConnectionID: entry.ID, ExternalSessionID: id}, true, quiet) != nil {
			succeeded = false
		}
	}
	if succeeded && ctx.Err() == nil {
		worker.attention.seeded.Store(true)
	}
}

func (r *Runner) read(ctx context.Context, a execution.Adapter, ref execution.SessionRef, attention, quiet bool) (*execution.Observation, error) {
	call, cancel := context.WithTimeout(ctx, readTimeout)
	o, err := a.Observe(call, ref)
	if err == nil {
		err = call.Err()
	}
	cancel()
	record := Record{ConnectionID: ref.ConnectionID, ExternalSessionID: ref.ExternalSessionID, Attention: attention, Quiet: quiet}
	if err != nil {
		if attention {
			return nil, nil
		}
		record.Unavailable = true
	} else if o.Session != ref || (attention && o.Child) {
		return nil, nil
	} else {
		record.Observation = &o
	}
	return record.Observation, r.send(ctx, record)
}

func (r *Runner) stream(ctx context.Context, entry WatchConnection) {
	id := entry.ID
	var mu sync.Mutex
	var last *string
	report := func(ctx context.Context, message string) {
		mu.Lock()
		defer mu.Unlock()
		if last != nil && *last == message {
			return
		}
		if r.send(ctx, Record{ConnectionID: id, StreamError: &message}) == nil {
			last = &message
		}
	}
	for ctx.Err() == nil {
		a, err := r.adapter(id)
		if err == nil {
			if src, ok := execution.As[execution.EventSource](a); ok {
				call, cancel := context.WithCancel(ctx)
				sink := func(callback context.Context, o execution.Observation) error {
					if err := call.Err(); err != nil {
						return err
					}
					if err := callback.Err(); err != nil {
						return err
					}
					if o.Session.ConnectionID != id || o.Session.ExternalSessionID == "" {
						return errors.New("pushed observation names a different or missing session")
					}
					err := r.send(call, Record{ConnectionID: id, ExternalSessionID: o.Session.ExternalSessionID, Observation: &o})
					if err == nil {
						report(call, "")
					}
					return err
				}
				if tracked, ok := execution.As[execution.TrackedEventSource](a); ok {
					var refs []execution.SessionRef
					for _, session := range entry.Sessions {
						if !session.Attention {
							refs = append(refs, execution.SessionRef{ConnectionID: id, ExternalSessionID: session.ID})
						}
					}
					err = tracked.TrackedEvents(call, refs, sink)
				} else {
					err = src.Events(call, sink)
				}
				cancel()
			}
			if ctx.Err() == nil && err != nil {
				report(ctx, err.Error())
			} else if ctx.Err() == nil {
				report(ctx, "")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(r.options.PollEvery):
		}
	}
}
