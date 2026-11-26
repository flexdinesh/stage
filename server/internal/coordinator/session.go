package coordinator

import (
	"context"
	"errors"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func (s *Service) RefreshScopedSession(ctx context.Context, ref execution.SessionRef) error {
	a, e := s.Adapter(ref.ConnectionID)
	if e != nil {
		return e
	}
	o, e := a.Observe(ctx, ref)
	if errors.Is(e, execution.ErrNotFound) {
		return db.ErrNotFound
	}
	if e != nil {
		return harnessErr(e)
	}
	if o.Session != ref {
		return db.Conflict("Harness returned a different session. Check the configured connection.")
	}
	if e := s.refusal(s.store.Harness(ref.ConnectionID), o); e != nil {
		return e
	}
	return s.putObservation(ctx, o)
}

// putObservation filters display metadata without interfering with execution state.
func (s *Service) putObservation(ctx context.Context, o execution.Observation) error {
	d, _ := s.descriptor(s.store.Harness(o.Session.ConnectionID))
	o.Facts = d.DisplayFacts(o.Facts)
	return s.store.PutObservation(ctx, o)
}
