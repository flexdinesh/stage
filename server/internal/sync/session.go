package sync

import (
	"context"
	"errors"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func (s *Service) RefreshSession(ctx context.Context, id string) error {
	return s.RefreshScopedSession(ctx, execution.SessionRef{ConnectionID: 1, ExternalSessionID: id})
}
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
	return s.store.PutObservation(ctx, o)
}
