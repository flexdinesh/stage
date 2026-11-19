// Package schedule runs one-shot starts and recurring schedules
// (docs/api.md "Schedule rules").
package schedule

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
)

// Launcher starts a task in T3 with a launching event detail
// (sync.Service.LaunchScheduled).
type Launcher interface {
	LaunchScheduled(ctx context.Context, id int64, detail string) (db.Task, error)
}

// Grace: a recurring fire later than this (server was down) is missed.
const Grace = 2 * time.Minute

// lateAfter: a one-shot start later than this notes how late it was.
const lateAfter = time.Minute

// Runner fires due schedules, then launches due one-shot tasks. Serialized.
type Runner struct {
	ConnectionID int64 // zero reconciles every connection
	// Harness, when set, reconciles that harness's active connection (it
	// changes when the instance is replaced) and wins over ConnectionID.
	Harness  string
	store    *db.Store
	launcher Launcher
	log      *slog.Logger

	// Every is the tick interval.
	Every time.Duration
	// Now is the clock (tests override it).
	Now func() time.Time

	mu sync.Mutex
}

func NewRunner(store *db.Store, launcher Launcher, log *slog.Logger) *Runner {
	if log == nil {
		log = slog.Default()
	}
	return &Runner{
		store: store, launcher: launcher, log: log.With("component", "schedule"),
		Every: 15 * time.Second, Now: time.Now,
	}
}

// Run reconciles on start and every Every until ctx is done.
func (r *Runner) Run(ctx context.Context) {
	t := time.NewTicker(r.Every)
	defer t.Stop()
	for {
		if err := r.Reconcile(ctx); err != nil && ctx.Err() == nil {
			r.log.Warn("reconcile schedules", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Reconcile fires every due schedule (spawning backlog tasks with a start
// time), then launches every backlog task whose start time has come.
func (r *Runner) Reconcile(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.Now()
	var errs []error
	due, err := r.store.DueSchedules(ctx, now, r.connection())
	if err != nil {
		return err
	}
	for _, id := range due {
		res, err := r.store.FireSchedule(ctx, id, now, Grace)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if res.Outcome != "" {
			r.log.Info("schedule fired", "schedule", id, "outcome", res.Outcome)
		}
	}
	tasks, err := r.store.DueForConnection(ctx, now, r.connection())
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, t := range tasks {
		if ctx.Err() != nil {
			break // shutting down: start no new launch (an in-flight one finishes)
		}
		detail := "scheduled start"
		if late := now.Sub(t.StartAt); late > lateAfter {
			detail += ", late by " + compact(late)
		}
		r.log.Info("start time reached; launching", "task", t.ID, "detail", detail)
		_, err := r.launcher.LaunchScheduled(ctx, t.ID, detail)
		var conflict *db.ConflictError
		if errors.As(err, &conflict) {
			if conflict.Code == "binding_required" || conflict.Code == "connection_unavailable" {
				if holdErr := r.store.HoldScheduled(ctx, t.ID, conflict.Error()); holdErr != nil {
					errs = append(errs, holdErr)
				}
			}
			continue // started or changed concurrently
		}
		if err != nil {
			errs = append(errs, err) // T3 failure: the task is failed with lastError
		}
	}
	return errors.Join(errs...)
}

// compact formats a duration as 12m, 3h, 3h5m or 2d4h.
func compact(d time.Duration) string {
	m := int(d.Round(time.Minute) / time.Minute)
	switch {
	case m < 60:
		return fmt.Sprintf("%dm", m)
	case m < 24*60:
		if m%60 == 0 {
			return fmt.Sprintf("%dh", m/60)
		}
		return fmt.Sprintf("%dh%dm", m/60, m%60)
	default:
		h := m / 60
		if h%24 == 0 {
			return fmt.Sprintf("%dd", h/24)
		}
		return fmt.Sprintf("%dd%dh", h/24, h%24)
	}
}

// connection is the connection this runner reconciles (0: all).
func (r *Runner) connection() int64 {
	if r.Harness != "" {
		if id := r.store.ActiveID(r.Harness); id != 0 {
			return id
		}
		return -1 // no connection yet: nothing to reconcile
	}
	return r.ConnectionID
}
