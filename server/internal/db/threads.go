package db

import (
	"context"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// UpsertThreadDetail stores Stage-owned observations. Legacy run/PR history remains intact.
func (s *Store) UpsertThreadDetail(ctx context.Context, source execution.Source) error {
	return s.PutObservation(ctx, source.Observation())
}
func (s *Store) UpsertThreads(ctx context.Context, observations []execution.Observation) error {
	for _, o := range observations {
		if old, e := s.Observation(ctx, o.Session); e == nil {
			o.Branch = old.Branch
			o.Directory = old.Directory
			o.StartedAt = old.StartedAt
			o.EndedAt = old.EndedAt
		}
		if err := s.PutObservation(ctx, o); err != nil {
			return err
		}
	}
	return nil
}
func nonNilPRs(prs []execution.Artifact) []execution.Artifact {
	if prs == nil {
		return []execution.Artifact{}
	}
	return prs
}
func RunSpan(source execution.Source) (*time.Time, *time.Time) {
	o := source.Observation()
	return o.StartedAt, o.EndedAt
}

type (
	// ThreadFilter selects mirrored threads (ListThreads).
	ThreadFilter = store.ThreadFilter
	// ThreadObservation is the watcher's stored view of a thread.
	ThreadObservation = store.ThreadObservation
)

// ThreadRuns returns a thread's mirrored recent runs (newest first, at most
// 50); [] when unread. ErrNotFound for an unknown thread.
func (s *Store) ThreadRuns(ctx context.Context, id string) ([]ThreadRun, error) {
	runs, err := read(ctx, s, func(tx store.Tx) ([]ThreadRun, error) { return tx.Threads().Runs(ctx, id) })
	if err != nil {
		return nil, err
	}
	if runs == nil {
		runs = []ThreadRun{}
	}
	return runs, nil
}

func (s *Store) GetThread(ctx context.Context, id string) (Thread, error) {
	return read(ctx, s, func(tx store.Tx) (Thread, error) { return tx.Threads().Get(ctx, id) })
}

// ListThreads returns a page of mirrored threads (createdAt desc) and the
// total match count.
func (s *Store) ListThreads(ctx context.Context, f ThreadFilter) (threads []Thread, total int, err error) {
	err = s.e.Read(ctx, func(tx store.Tx) error {
		var err error
		threads, total, err = tx.Threads().List(ctx, f)
		return err
	})
	return threads, total, err
}

// SetPendingQuestionCount stores how many of a thread's pending requests are
// answerable questions (t3_pending_request list). Reads clamp it to
// pending_request_count, and a detail upsert with no pending requests resets it.
func (s *Store) SetPendingQuestionCount(ctx context.Context, threadID string, n int) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Threads().SetPendingQuestionCount(ctx, threadID, n) })
}

// NeedsInputCount: tasks in needs_input + external threads needing input.
func (s *Store) NeedsInputCount(ctx context.Context) (int, error) {
	return read(ctx, s, func(tx store.Tx) (int, error) { return tx.Threads().NeedsInputCount(ctx) })
}

// WatchedThreadIDs: external non-subagent threads the watcher last saw running,
// needing input or done (re-read until they leave those states; done until
// observed again, including when work resumes). Scoped to the connection: a
// replaced T3's threads are never watched against the T3 that replaced it.
func (s *Store) WatchedThreadIDs(ctx context.Context, connection int64) ([]string, error) {
	return read(ctx, s, func(tx store.Tx) ([]string, error) { return tx.Threads().WatchedIDs(ctx, connection) })
}

// ThreadObservations returns an entry for every id.
func (s *Store) ThreadObservations(ctx context.Context, ids []string) (map[string]ThreadObservation, error) {
	return read(ctx, s, func(tx store.Tx) (map[string]ThreadObservation, error) { return tx.Threads().Observations(ctx, ids) })
}

// SetObservedState records the watcher's mapped state for a mirrored thread
// of ref's connection (scoped like WatchedThreadIDs: another T3 connection's
// thread is never touched).
func (s *Store) SetObservedState(ctx context.Context, ref SessionRef, st TaskState) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Threads().SetObservedState(ctx, ref, st) })
}
