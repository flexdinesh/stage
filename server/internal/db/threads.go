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
func RunSpan(source execution.Source) (*time.Time, *time.Time) {
	o := source.Observation()
	return o.StartedAt, o.EndedAt
}

// NeedsInputCount: tasks in needs_input plus top-level sessions no task owns
// observed needing input.
func (s *Store) NeedsInputCount(ctx context.Context) (int, error) {
	return read(ctx, s, func(tx store.Tx) (int, error) { return tx.Observations().NeedsInputCount(ctx) })
}
