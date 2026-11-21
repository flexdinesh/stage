package sync

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
	return s.store.PutObservation(ctx, o)
}
