package chains

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/flexdinesh/t3-stage/server/internal/db"
	t3sync "github.com/flexdinesh/t3-stage/server/internal/sync"
	"github.com/flexdinesh/t3-stage/server/internal/t3"
)

// SkipDetail is the event detail of a skipped step.
const SkipDetail = "skipped"

// Launcher starts a task in T3 (sync.Service.LaunchTask).
type Launcher interface {
	LaunchTask(ctx context.Context, id int64) (db.Task, error)
}

// Runner applies chain rules. Evaluation of chains is serialized.
type Runner struct {
	store    *db.Store
	launcher Launcher
	t3       t3.Client
	log      *slog.Logger

	// Every is the reconcile interval for running chains.
	Every time.Duration

	mu   sync.Mutex
	kick chan int64
}

func NewRunner(store *db.Store, launcher Launcher, client t3.Client, log *slog.Logger) *Runner {
	if log == nil {
		log = slog.Default()
	}
	return &Runner{
		store: store, launcher: launcher, t3: client, log: log.With("component", "chains"),
		Every: 10 * time.Second,
		kick:  make(chan int64, 256),
	}
}

// OnTransition is the sync.Service hook. A step entering
// failed/interrupted/cancelled (not via skip) pauses its running chain;
// every chain step change schedules an evaluation.
func (r *Runner) OnTransition(ctx context.Context, c t3sync.Change) {
	if c.ChainID == nil {
		return
	}
	switch c.To {
	case db.StateFailed, db.StateInterrupted, db.StateCancelled:
		if c.Detail != SkipDetail {
			changed, _, err := r.store.SetChainState(ctx, *c.ChainID, []db.ChainState{db.ChainRunning}, db.ChainPaused)
			if err != nil {
				r.log.Warn("pause chain", "chain", *c.ChainID, "err", err)
			} else if changed {
				r.log.Info("chain paused", "chain", *c.ChainID, "task", c.TaskID, "state", c.To)
			}
		}
	}
	select {
	case r.kick <- *c.ChainID:
	default:
	}
}

// Run evaluates kicked chains and reconciles running chains periodically
// until ctx is done.
func (r *Runner) Run(ctx context.Context) {
	t := time.NewTicker(r.Every)
	defer t.Stop()
	if err := r.EvaluateAll(ctx); err != nil && ctx.Err() == nil {
		r.log.Warn("evaluate chains", "err", err)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-r.kick:
			if err := r.Evaluate(ctx, id); err != nil && ctx.Err() == nil {
				r.log.Warn("evaluate chain", "chain", id, "err", err)
			}
		case <-t.C:
			if err := r.EvaluateAll(ctx); err != nil && ctx.Err() == nil {
				r.log.Warn("evaluate chains", "err", err)
			}
		}
	}
}

// EvaluateAll evaluates every running chain.
func (r *Runner) EvaluateAll(ctx context.Context) error {
	chains, err := r.store.ListChains(ctx, db.ChainRunning)
	if err != nil {
		return err
	}
	var errs []error
	for _, c := range chains {
		if err := r.Evaluate(ctx, c.ID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Evaluate applies one Advance action to the chain.
func (r *Runner) Evaluate(ctx context.Context, chainID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.evaluateLocked(ctx, chainID)
}

func (r *Runner) evaluateLocked(ctx context.Context, chainID int64) error {
	chain, err := r.store.GetChain(ctx, chainID)
	if errors.Is(err, db.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	act := Advance(chain.State, StepsOf(chain.Steps))
	switch act.Kind {
	case ActionLaunch:
		r.log.Info("chain launching step", "chain", chainID, "task", act.TaskID)
		_, err := r.launcher.LaunchTask(ctx, act.TaskID)
		var conflict *db.ConflictError
		if errors.As(err, &conflict) {
			return nil // step changed state concurrently
		}
		return err // T3 failure: task is failed and the hook paused the chain
	case ActionDone:
		done, err := r.store.CompleteChain(ctx, chainID)
		if err != nil {
			return err
		}
		if done {
			r.log.Info("chain done", "chain", chainID)
		}
	case ActionPause:
		if _, _, err := r.store.SetChainState(ctx, chainID, []db.ChainState{db.ChainRunning}, db.ChainPaused); err != nil {
			return err
		}
		r.log.Info("chain paused", "chain", chainID, "task", act.TaskID)
	}
	return nil
}

// Start: idle → running, then launch the current step.
func (r *Runner) Start(ctx context.Context, id int64) (db.Chain, error) {
	chain, err := r.store.GetChain(ctx, id)
	if err != nil {
		return db.Chain{}, err
	}
	if len(chain.Steps) == 0 {
		return db.Chain{}, db.Conflict("chain has no steps")
	}
	changed, cur, err := r.store.SetChainState(ctx, id, []db.ChainState{db.ChainIdle}, db.ChainRunning)
	if err != nil {
		return db.Chain{}, err
	}
	if !changed {
		return db.Chain{}, db.Conflict("chain is %s; start requires idle", cur)
	}
	return r.evaluateAndGet(ctx, id)
}

// Pause: running → paused (active thread keeps running).
func (r *Runner) Pause(ctx context.Context, id int64) (db.Chain, error) {
	changed, cur, err := r.store.SetChainState(ctx, id, []db.ChainState{db.ChainRunning}, db.ChainPaused)
	if err != nil {
		return db.Chain{}, err
	}
	if !changed {
		return db.Chain{}, db.Conflict("chain is %s; pause requires running", cur)
	}
	return r.store.GetChain(ctx, id)
}

// Resume: paused → running unless the current step is failed/interrupted.
func (r *Runner) Resume(ctx context.Context, id int64) (db.Chain, error) {
	r.mu.Lock()
	chain, err := r.store.GetChain(ctx, id)
	if err != nil {
		r.mu.Unlock()
		return db.Chain{}, err
	}
	if chain.State != db.ChainPaused {
		r.mu.Unlock()
		return db.Chain{}, db.Conflict("chain is %s; resume requires paused", chain.State)
	}
	if cur, ok := Current(StepsOf(chain.Steps)); ok && (cur.State == db.StateFailed || cur.State == db.StateInterrupted) {
		r.mu.Unlock()
		return db.Chain{}, db.Conflict("current step %d is %s: restart it or skip it first", cur.TaskID, cur.State)
	}
	changed, cur, err := r.store.SetChainState(ctx, id, []db.ChainState{db.ChainPaused}, db.ChainRunning)
	r.mu.Unlock()
	if err != nil {
		return db.Chain{}, err
	}
	if !changed {
		return db.Chain{}, db.Conflict("chain is %s; resume requires paused", cur)
	}
	return r.evaluateAndGet(ctx, id)
}

// Skip cancels the current step with detail "skipped", then interrupts its
// thread when it was active (best effort: a failed interrupt is logged; the
// step stays cancelled). Cancelling first keeps the poller from mapping the
// interrupt to interrupted (which would pause the chain). Chain state is
// unchanged; a running chain advances.
func (r *Runner) Skip(ctx context.Context, id int64) (db.Chain, error) {
	r.mu.Lock()
	chain, err := r.store.GetChain(ctx, id)
	if err != nil {
		r.mu.Unlock()
		return db.Chain{}, err
	}
	cur, ok := Current(StepsOf(chain.Steps))
	if !ok {
		r.mu.Unlock()
		return db.Chain{}, db.Conflict("chain has no current step")
	}
	var task db.Task
	for _, t := range chain.Steps {
		if t.ID == cur.TaskID {
			task = t
		}
	}
	res, err := r.store.TransitionTask(ctx, db.Transition{
		TaskID: task.ID, From: []db.TaskState{task.State}, To: db.StateCancelled, Detail: SkipDetail,
	})
	r.mu.Unlock()
	if err != nil {
		return db.Chain{}, err
	}
	if !res.Changed {
		return db.Chain{}, db.Conflict("step %d changed state (now %s); retry", task.ID, res.From)
	}
	r.log.Info("chain step skipped", "chain", id, "task", task.ID)
	if task.State.Active() && task.T3ThreadID != nil {
		if err := r.t3.Interrupt(ctx, *task.T3ThreadID); err != nil {
			r.log.Warn("interrupt skipped step", "chain", id, "task", task.ID, "thread", *task.T3ThreadID, "err", err)
		}
	}
	return r.evaluateAndGet(ctx, id)
}

func (r *Runner) evaluateAndGet(ctx context.Context, id int64) (db.Chain, error) {
	if err := r.Evaluate(ctx, id); err != nil {
		r.log.Warn("evaluate chain", "chain", id, "err", err)
	}
	return r.store.GetChain(ctx, id)
}
