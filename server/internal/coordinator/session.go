package coordinator

import (
	"context"
	"errors"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func (s *Service) RefreshScopedSession(ctx context.Context, ref execution.SessionRef) error {
	o, err := s.ReadScopedSession(ctx, ref)
	if err != nil {
		return err
	}
	return s.store.PutObservation(ctx, o)
}

// ReadScopedSession verifies one named session without changing Stage state.
func (s *Service) ReadScopedSession(ctx context.Context, ref execution.SessionRef) (execution.Observation, error) {
	a, e := s.conns.Sessions(ref.ConnectionID)
	if e != nil {
		return execution.Observation{}, e
	}
	o, e := a.Observe(ctx, ref)
	if errors.Is(e, execution.ErrNotFound) {
		return execution.Observation{}, db.ErrNotFound
	}
	if e != nil {
		return execution.Observation{}, harnessErr(e)
	}
	if o.Session != ref {
		return execution.Observation{}, db.Conflict("Harness returned a different session. Check the configured connection.")
	}
	kind := s.store.Harness(ref.ConnectionID)
	if e := s.refusal(kind, o); e != nil {
		return execution.Observation{}, e
	}
	if e := o.Valid(); e != nil {
		return execution.Observation{}, harnessErr(e)
	}
	d, _ := s.conns.Descriptor(kind)
	o.Facts = d.DisplayFacts(o.Facts)
	return o, nil
}

// putObservation filters display metadata without interfering with execution state.
func (s *Service) putObservation(ctx context.Context, o execution.Observation) error {
	d, _ := s.conns.Descriptor(s.store.Harness(o.Session.ConnectionID))
	o.Facts = d.DisplayFacts(o.Facts)
	return s.store.PutObservation(ctx, o)
}
