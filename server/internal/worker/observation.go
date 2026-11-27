package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/observe"
	"github.com/flexdinesh/stage/server/internal/observe/outbox"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

const (
	outboxDir   = "outbox"
	watchFile   = "watch.json"
	watchSchema = 1

	observationWaitingSetup = "waiting_for_setup"
	observationWaitingWatch = "waiting_for_watch"
	observationRecovery     = "recovery_needed"
	observationSampling     = "sampling"
	observationDegraded     = "degraded"
)

// observationWorker runs beside the local adapters. A disconnect only detaches
// the sender. A new attachment pauses sampling until setup and watch agree again.
type observationWorker struct {
	worker   *runner
	identity string
	work     *observe.Runner
	wake     chan struct{}

	mu              sync.Mutex
	queue           *outbox.Queue
	watch           *observe.Watch
	revision        uint64
	canResume       bool
	lost, recovered uint64
	attached        *observationAttachment
}

type observationAttachment struct {
	channel      *channel
	ready        bool
	installed    bool
	acknowledged uint64
}

func newObservationWorker(w *runner, local *machine.Local, identity string) *observationWorker {
	o := &observationWorker{worker: w, identity: identity, wake: make(chan struct{}, 1)}
	o.work = observe.NewRunner(func(id int64) (execution.Adapter, error) {
		a, ok := local.Adapter(id)
		if !ok {
			return nil, fmt.Errorf("no local adapter for connection %d", id)
		}
		return a, nil
	}, o.enqueue, observe.RunnerOptions{Log: w.log})
	o.work.Pause()
	local.AddChange(func(old, next int64) {
		if old != next {
			o.work.Reset(old)
		}
		o.work.Reset(next)
	})
	return o
}

func (o *observationWorker) restore(settingsRestored bool, settingsErr error, previous *State) {
	watch, watchErr := o.readWatch()
	if watchErr == nil {
		o.work.Replace(watch)
		o.watch, o.revision = &watch, 1
	} else if errors.Is(watchErr, os.ErrNotExist) {
		watchErr = nil
	} else {
		watchErr = fmt.Errorf("restore observation watch: %w", watchErr)
	}
	outboxErr := o.openOutbox(previous)
	if err := errors.Join(settingsErr, watchErr, outboxErr); err != nil {
		o.report(observationRecovery, err)
		return
	}
	switch {
	case !settingsRestored:
		o.report(observationWaitingSetup, nil)
	case o.watch == nil:
		o.report(observationWaitingWatch, nil)
	default:
		o.work.RetainAttention(o.queue.ActiveAttention())
		o.canResume = true
		o.resume()
	}
}

func (o *observationWorker) openOutbox(previous *State) error {
	dir := filepath.Join(o.worker.dir, outboxDir)
	q, err := outbox.Open(dir, o.identity, outbox.Limits{})
	var recovery error
	if errors.Is(err, outbox.ErrCorrupt) {
		recovery = err
		// Drop only the corrupt file. A new producer and fresh watch let the
		// main server resample without conflating old and new cursors.
		if removeErr := os.Remove(filepath.Join(dir, "state.json")); removeErr != nil {
			return errors.Join(err, removeErr)
		}
		q, err = outbox.Open(dir, o.identity, outbox.Limits{})
	}
	if err != nil {
		return fmt.Errorf("open observation outbox: %w", errors.Join(recovery, err))
	}
	o.queue, o.lost = q, q.Lost()
	if previous != nil && previous.ObservationProducer == q.Producer() {
		o.recovered = min(previous.ObservationRecovered, o.lost)
	}
	if recovery != nil {
		return fmt.Errorf("started a new observation producer: %w", recovery)
	}
	return nil
}

func (o *observationWorker) report(status string, err error) {
	if err != nil {
		o.worker.log.Warn("worker observation needs recovery", "err", err)
	}
	o.worker.updateState(func(s State) State {
		if s.State == "" {
			s.State = StateConnecting
		}
		if status != "" {
			s.Observation, s.ObservationError = status, ""
			if err != nil {
				s.ObservationError = err.Error()
			}
		}
		if s.Observation == observationSampling || s.Observation == observationDegraded {
			s.Observation = observationSampling
			if o.lost > o.recovered {
				s.Observation = observationDegraded
			}
		}
		if o.queue != nil {
			s.ObservationProducer = o.queue.Producer()
			s.ObservationLost, s.ObservationRecovered = o.lost, o.recovered
		}
		return s
	})
}

func (o *observationWorker) pause() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.work.Pause()
}

func (o *observationWorker) attach(c *channel) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.queue == nil {
		if err := o.openOutbox(nil); err != nil {
			o.report(observationRecovery, err)
		}
	}
	if o.queue != nil && slices.Contains(c.serverKinds, observe.KindBatch) {
		o.attached = &observationAttachment{channel: c}
	}
}

func (o *observationWorker) reconcile() {
	o.pause()
	o.worker.updateState(func(s State) State {
		if s.Observation == observationSampling {
			s.Observation = observationWaitingWatch
		}
		return s
	})
}

func (o *observationWorker) detach(c *channel, reconnect bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.attached != nil && o.attached.channel == c {
		o.attached = nil
		o.signal()
	}
	if reconnect && o.canResume {
		o.resume()
	}
}

func (o *observationWorker) capabilities(c *channel, snapshot *workerchan.Ready) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.queue != nil {
		snapshot.UpKinds = append(snapshot.UpKinds, observe.KindBatch)
	}
	if o.attached != nil && o.attached.channel == c {
		snapshot.ChannelKinds = append(snapshot.ChannelKinds, observe.KindWatch)
	}
}

type watchReceipt struct {
	ack      observe.WatchAck
	revision uint64
}

func (o *observationWorker) accept(c *channel, ctx context.Context, env workerchan.Envelope) (watchReceipt, error) {
	var watch observe.Watch
	if err := json.Unmarshal(env.Body, &watch); err != nil {
		return watchReceipt{}, fmt.Errorf("decode observation watch: %w", err)
	}
	if err := validateWatch(watch); err != nil {
		return watchReceipt{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	a := o.attached
	if a == nil || a.channel != c {
		return watchReceipt{}, &workerchan.RemoteError{Code: workerchan.CodeOperationUnavailable, Message: "observation watch is unavailable on this attachment"}
	}
	if err := ctx.Err(); err != nil {
		return watchReceipt{}, err
	}
	released, err := o.queue.ReleasedCursor()
	if err != nil {
		o.report(observationRecovery, err)
		return watchReceipt{}, err
	}
	if o.watch != nil && o.watch.Epoch == watch.Epoch && watch.Generation <= o.watch.Generation {
		if !o.work.Replace(watch) {
			return watchReceipt{}, errors.New("stale or conflicting observation watch")
		}
	} else {
		newEpoch := o.watch != nil && o.watch.Epoch != watch.Epoch
		if err := o.saveWatch(watch); err != nil {
			o.report(observationRecovery, err)
			return watchReceipt{}, err
		}
		o.work.Replace(watch)
		o.watch = &watch
		o.revision++
		// A full watch in a new epoch also resamples after a main restart,
		// including when the main had already acknowledged a gap.
		if (watch.Resample || newEpoch) && o.recovered < o.lost {
			o.recovered = o.lost
			o.report("", nil)
		}
	}
	o.canResume = true
	a.installed = true
	o.activate(a)
	return watchReceipt{ack: observe.WatchAck{Producer: o.queue.Producer(), ReleasedCursor: released}, revision: o.revision}, nil
}

func (o *observationWorker) ready(c *channel) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if a := o.attached; a != nil && a.channel == c {
		a.ready = true
		if o.activate(a) {
			return
		}
	}
	o.worker.waitForObservationWatch()
}

func (o *observationWorker) acknowledged(c *channel, receipt watchReceipt) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if a := o.attached; a != nil && a.channel == c {
		a.acknowledged = max(a.acknowledged, receipt.revision)
		o.signal()
	}
}

// The server can send a watch before ready returns. Once both are installed,
// sampling can continue if the channel drops before the watch reply is sent.
// Delivery also waits for that reply.
func (o *observationWorker) activate(a *observationAttachment) bool {
	if a.ready && a.installed {
		o.resume()
		o.signal()
		return true
	}
	return false
}

func (o *observationWorker) resume() {
	o.work.Resume()
	o.report(observationSampling, nil)
}

func (o *observationWorker) enqueue(ctx context.Context, record observe.Record) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := o.queue.Enqueue(record); err != nil {
		return err
	}
	if lost := o.queue.Lost(); lost > o.lost {
		o.lost = lost
		o.report("", nil)
	}
	o.signal()
	return nil
}

func (o *observationWorker) signal() {
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

func (o *observationWorker) deliveryTarget() (*channel, *outbox.Queue) {
	o.mu.Lock()
	defer o.mu.Unlock()
	a := o.attached
	if a == nil || !a.ready || a.acknowledged == 0 || a.acknowledged != o.revision || a.channel.ctx.Err() != nil {
		return nil, nil
	}
	return a.channel, o.queue
}

// deliver is the sole sender across reconnects. Failed posts leave the sealed
// batch intact, including when the main server committed but its reply was lost.
func (o *observationWorker) deliver(ctx context.Context) {
	const minDelay, maxDelay = time.Second, 30 * time.Second
	backoff := minDelay
	for ctx.Err() == nil {
		c, q := o.deliveryTarget()
		if c != nil {
			sent, err := o.sendObservations(c, q)
			if err != nil {
				if c.ctx.Err() == nil {
					o.worker.log.Warn("send observation batch", "err", err, "retryIn", backoff)
					timer := time.NewTimer(backoff)
					select {
					case <-ctx.Done():
					case <-c.ctx.Done():
					case <-timer.C:
					}
					timer.Stop()
					backoff = min(backoff*2, maxDelay)
				}
				continue
			}
			if sent {
				backoff = minDelay
				continue
			}
		}
		backoff = minDelay
		select {
		case <-ctx.Done():
		case <-o.wake:
		}
	}
}

func (o *observationWorker) sendObservations(c *channel, q *outbox.Queue) (bool, error) {
	batch, err := q.Seal()
	if err != nil || len(batch.Records) == 0 {
		return false, err
	}
	ctx, cancel := context.WithTimeout(c.ctx, workerchan.DefaultTimeout)
	defer cancel()
	raw, err := c.upcall(workerchan.Envelope{})(ctx, observe.KindBatch, batch)
	if err != nil {
		return false, err
	}
	var ack observe.BatchAck
	if err := json.Unmarshal(raw, &ack); err != nil {
		return false, fmt.Errorf("decode observation acknowledgement: %w", err)
	}
	if ack.Cursor > batch.Cursor {
		return false, fmt.Errorf("observation acknowledgement %d exceeds sent cursor %d", ack.Cursor, batch.Cursor)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	// Serialize releasing records with the replacement attachment's watch
	// reply, which reports the exact durable prefix no longer replayable.
	if o.attached == nil || o.attached.channel != c || c.ctx.Err() != nil {
		return false, workerchan.ErrReplaced
	}
	if err := q.Ack(ack.Cursor); err != nil {
		return false, err
	}
	if ack.Cursor < batch.Cursor {
		return false, fmt.Errorf("observations acknowledged through %d of %d", ack.Cursor, batch.Cursor)
	}
	// The server requests resampling in a fresh watch, which can arrive
	// before this acknowledgement. The runner handles it at installation.
	return true, nil
}

type savedWatch struct {
	Schema int           `json:"schema"`
	Join   string        `json:"join"`
	Watch  observe.Watch `json:"watch"`
}

func (o *observationWorker) readWatch() (observe.Watch, error) {
	path := filepath.Join(o.worker.dir, watchFile)
	info, err := os.Lstat(path)
	if err != nil {
		return observe.Watch{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > workerchan.MaxPostBytes {
		return observe.Watch{}, errors.New("observation watch must be a private regular file within the channel size limit")
	}
	file, err := os.Open(path)
	if err != nil {
		return observe.Watch{}, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, workerchan.MaxPostBytes+1))
	if err != nil {
		return observe.Watch{}, err
	}
	var saved savedWatch
	if len(raw) > workerchan.MaxPostBytes || json.Unmarshal(raw, &saved) != nil {
		return observe.Watch{}, errors.New("malformed observation watch")
	}
	if saved.Schema != watchSchema || saved.Join != o.identity {
		return observe.Watch{}, errors.New("observation watch has an unsupported schema or belongs to another join")
	}
	return saved.Watch, validateWatch(saved.Watch)
}

func (o *observationWorker) saveWatch(watch observe.Watch) error {
	raw, err := json.Marshal(savedWatch{Schema: watchSchema, Join: o.identity, Watch: watch})
	if err != nil {
		return err
	}
	if len(raw) > workerchan.MaxPostBytes {
		return errors.New("observation watch exceeds the channel size limit")
	}
	if err := writeFileAtomic(filepath.Join(o.worker.dir, watchFile), raw, 0o600); err != nil {
		return fmt.Errorf("persist observation watch: %w", err)
	}
	dir, err := os.Open(o.worker.dir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func validateWatch(watch observe.Watch) error {
	if watch.Epoch == "" || watch.Generation <= 0 {
		return errors.New("observation watch requires an epoch and positive generation")
	}
	connections := make(map[int64]bool, len(watch.Connections))
	for _, c := range watch.Connections {
		if c.ID <= 0 || connections[c.ID] {
			return errors.New("observation watch contains a missing or duplicate connection ID")
		}
		connections[c.ID] = true
		sessions := make(map[string]bool, len(c.Sessions))
		for _, s := range c.Sessions {
			if s.ID == "" || sessions[s.ID] {
				return errors.New("observation watch contains a missing or duplicate session ID")
			}
			sessions[s.ID] = true
		}
	}
	return nil
}
