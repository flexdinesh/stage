// Package release releases tree members whose needs have user acceptance
// (docs/api.md "Tree rules"): auto members launch; manual members with
// needs read ready once per episode and are reported to OnReady (the ready
// notification). A failed, interrupted or cancelled need keeps dependents
// blocked.
package release

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/workevent"
)

// Launcher starts a task in its harness (sync.Service.LaunchTask).
type Launcher interface {
	LaunchTask(ctx context.Context, id int64) (db.Task, error)
}

// Runner launches released auto members and marks manual ones ready.
// Reconciles are serialized.
type Runner struct {
	// ConnectionID is the connection this runner reconciles (zero: every
	// connection). The server runs one runner per active connection.
	ConnectionID int64
	store        *db.Store
	launcher     Launcher
	log          *slog.Logger

	// Every is the reconcile interval (catches changes missed while down).
	Every time.Duration
	// OnReady is called for each manual member that became ready (set
	// before Run). Must not block for long.
	OnReady func(ctx context.Context, taskID int64)

	mu   sync.Mutex
	kick chan struct{}
}

func NewRunner(store *db.Store, launcher Launcher, log *slog.Logger) *Runner {
	if log == nil {
		log = slog.Default()
	}
	return &Runner{
		store: store, launcher: launcher, log: log.With("component", "release"),
		Every: 10 * time.Second,
		kick:  make(chan struct{}, 1),
	}
}

// OnTransition reconciles when a launch ends a readiness episode.
func (r *Runner) OnTransition(_ context.Context, c workevent.TaskChange) {
	if c.From == db.StateBacklog {
		r.Kick()
	}
}

// Kick schedules a reconcile (tree changes: needs, start modes, splices).
// Never blocks.
func (r *Runner) Kick() {
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

// Run reconciles on start, on every kick and every Every until ctx is done.
func (r *Runner) Run(ctx context.Context) {
	t := time.NewTicker(r.Every)
	defer t.Stop()
	for {
		if err := r.Reconcile(ctx); err != nil && ctx.Err() == nil {
			r.log.Warn("reconcile trees", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-r.kick:
		case <-t.C:
		}
	}
}

// Reconcile launches every auto member whose needs all have user acceptance, then
// records readiness episodes of manual members and reports new ones to
// OnReady.
func (r *Runner) Reconcile(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids, err := r.store.LaunchableForConnection(ctx, r.ConnectionID)
	if err != nil {
		return err
	}
	var errs []error
	for _, id := range ids {
		if ctx.Err() != nil {
			return errors.Join(errs...) // shutting down: start no new launch (an in-flight one finishes)
		}
		r.log.Info("needs accepted; launching", "task", id)
		_, err := r.launcher.LaunchTask(ctx, id)
		var conflict *db.ConflictError
		if errors.As(err, &conflict) {
			continue // started or changed concurrently
		}
		if err != nil {
			errs = append(errs, err) // The task is failed with lastError.
		}
	}
	ready, err := r.store.MarkReady(ctx)
	if err != nil {
		errs = append(errs, err)
	}
	for _, id := range ready {
		r.log.Info("needs accepted; ready to start", "task", id)
		if r.OnReady != nil {
			r.OnReady(ctx, id)
		}
	}
	return errors.Join(errs...)
}
