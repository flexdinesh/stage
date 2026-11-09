package sync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/flexdinesh/t3-stage/server/internal/db"
	"github.com/flexdinesh/t3-stage/server/internal/t3"
)

// ThreadChange is an external thread's observed state change (watcher).
// From nil = first observation.
type ThreadChange struct {
	ThreadID  string
	ProjectID string // T3 project id
	Title     string
	From      *db.TaskState
	To        db.TaskState
}

// watchStatuses: thread statuses the watcher lists from T3 (active runs).
// subagent is T3 relationshipToParent of a subagent thread (never watched).
const subagent = "subagent"

var watchStatuses = []t3.ThreadStatus{
	t3.StatusPreparing, t3.StatusQueued, t3.StatusStarting, t3.StatusRunning, t3.StatusWaiting,
}

// Notifiable: entering these states notifies (docs/api.md "Notification rules").
func Notifiable(s db.TaskState) bool {
	return s == db.StateNeedsInput || s == db.StateDone || s == db.StateFailed
}

// WatchThreads reads every active external thread (unsettled root threads in
// an active status, plus those last observed running|needs_input), stores
// the mapped state as observed_state and reports changes into a notifiable
// state via OnThreadChange. External = not owned by a task and not a
// subagent (forks included). The first pass after
// start records silently; afterwards a never-observed thread reports only
// when created after start. A never-observed thread maps from running (rule
// 9 keeps it running). A thread T3 no longer knows becomes cancelled silently.
func (s *Service) WatchThreads(ctx context.Context) error {
	if !s.t3.Status(ctx).Connected {
		return nil
	}
	var ids []string
	seen := map[string]bool{}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	settled := false
	var cursor *int
	for page := 0; page < 10000; page++ {
		res, err := s.t3.ListThreads(ctx, t3.ListThreadsInput{
			AllProjects: true, Statuses: watchStatuses, Settled: &settled, Limit: 100, Cursor: cursor,
		})
		if err != nil {
			return t3Err(fmt.Errorf("list threads: %w", err))
		}
		for _, t := range res.Threads {
			if t.RelationshipToParent != subagent {
				add(t.ThreadID)
			}
		}
		if res.NextCursor == nil || len(res.Threads) == 0 || (cursor != nil && *res.NextCursor <= *cursor) {
			break
		}
		cursor = res.NextCursor
	}
	watched, err := s.store.WatchedThreadIDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range watched {
		add(id)
	}
	obs, err := s.store.ThreadObservations(ctx, ids)
	if err != nil {
		return err
	}
	silent := !s.watchSeeded.Load()
	for _, id := range ids {
		if ctx.Err() != nil {
			return nil
		}
		o := obs[id]
		if o.TaskOwned {
			// Legacy task state can be frozen after a follower is released.
			// Its session mirror must still reflect external restarts/input.
			if _, err := s.readSession(ctx, id); err != nil && !errors.Is(err, t3.ErrNotFound) {
				s.log.Warn("refresh task session", "thread", id, "err", err)
			}
			continue
		}
		if err := s.watchThread(ctx, id, o.ObservedState, silent); err != nil {
			if errors.Is(err, t3.ErrNotConnected) {
				return err
			}
			s.log.Warn("watch thread", "thread", id, "err", err)
		}
	}
	s.watchSeeded.Store(true)
	return nil
}

func (s *Service) watchThread(ctx context.Context, id string, prev *db.TaskState, silent bool) error {
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	d, err := s.t3.ReadThread(callCtx, id)
	cancel()
	if errors.Is(err, t3.ErrNotFound) {
		return s.store.SetObservedState(ctx, id, db.StateCancelled)
	}
	if err != nil {
		return t3Err(err)
	}
	if d.RelationshipToParent == subagent {
		return nil
	}
	if err := s.store.UpsertThreadDetail(ctx, d); err != nil {
		return fmt.Errorf("upsert thread: %w", err)
	}
	cur := db.StateRunning
	if prev != nil {
		cur = *prev
	}
	next := mapDetail(cur, d)
	if prev != nil && *prev == next {
		return nil
	}
	if err := s.store.SetObservedState(ctx, id, next); err != nil {
		return err
	}
	if silent || !Notifiable(next) || (prev == nil && !d.CreatedAt.After(s.started)) {
		return nil
	}
	s.log.Info("thread change", "thread", id, "to", next)
	if s.OnThreadChange != nil {
		s.OnThreadChange(ctx, ThreadChange{ThreadID: id, ProjectID: d.ProjectID, Title: d.Title, From: prev, To: next})
	}
	return nil
}
