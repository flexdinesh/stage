// Package links runs runs-after links: a backlog task linked to run after
// another launches once that task is settled (or cancelled) in T3
// (docs/api.md "Link rules").
package links

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/flexdinesh/t3-stage/server/internal/db"
	t3sync "github.com/flexdinesh/t3-stage/server/internal/sync"
)

// Launcher starts a task in T3 (sync.Service.LaunchTask).
type Launcher interface {
	LaunchTask(ctx context.Context, id int64) (db.Task, error)
}

// Passed: the predecessor no longer blocks its follower.
func Passed(s db.TaskState) bool { return s == db.StateSettled || s == db.StateCancelled }

// Runner launches ready followers. Launches are serialized.
type Runner struct {
	store    *db.Store
	launcher Launcher
	log      *slog.Logger

	// Every is the reconcile interval (catches transitions missed while down).
	Every time.Duration

	mu   sync.Mutex
	kick chan struct{}
}

func NewRunner(store *db.Store, launcher Launcher, log *slog.Logger) *Runner {
	if log == nil {
		log = slog.Default()
	}
	return &Runner{
		store: store, launcher: launcher, log: log.With("component", "links"),
		Every: 10 * time.Second,
		kick:  make(chan struct{}, 1),
	}
}

// OnTransition is the sync.Service hook: a task passing (settled or
// cancelled) schedules a reconcile that launches its follower.
func (r *Runner) OnTransition(_ context.Context, c t3sync.Change) {
	if !Passed(c.To) {
		return
	}
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
			r.log.Warn("reconcile links", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-r.kick:
		case <-t.C:
		}
	}
}

// Reconcile launches every backlog task whose predecessor has passed.
func (r *Runner) Reconcile(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids, err := r.store.ReadyFollowers(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, id := range ids {
		r.log.Info("predecessor passed; launching follower", "task", id)
		_, err := r.launcher.LaunchTask(ctx, id)
		var conflict *db.ConflictError
		if errors.As(err, &conflict) {
			continue // started or changed concurrently
		}
		if err != nil {
			errs = append(errs, err) // T3 failure: the task is failed with lastError
		}
	}
	return errors.Join(errs...)
}
