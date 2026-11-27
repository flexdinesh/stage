package observe

import (
	"context"
	"time"
)

// WatchAttention polls sessions that need attention across active connections.
func (s *Service) WatchAttention(ctx context.Context) error {
	for id := range s.adapters.All() {
		if err := s.watchAttention(ctx, id); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) watchAttention(ctx context.Context, connection int64) error {
	a, err := s.adapters.Adapter(connection)
	if err != nil {
		return err
	}
	_, seeded := s.watchSeeded.Load(connection)
	if !a.Health(ctx).Available {
		return nil
	}
	refs, e := a.Attention(ctx)
	if e != nil {
		return nil
	}
	stored, e := s.store.WatchedSessions(ctx, connection)
	if e != nil {
		return e
	}
	refs = append(refs, stored...)
	seen := map[string]bool{}
	for _, ref := range refs {
		if seen[ref.ExternalSessionID] {
			continue
		}
		seen[ref.ExternalSessionID] = true
		o, e := a.Observe(ctx, ref)
		if e != nil || ref.ConnectionID != connection || o.Child || o.Session != ref {
			continue
		}
		if err := s.emitLocal(ctx, Record{ConnectionID: connection, ExternalSessionID: ref.ExternalSessionID,
			ObservedAt: time.Now().UTC(), Observation: &o, Attention: true, Quiet: !seeded}); err != nil {
			return err
		}
	}
	s.watchSeeded.Store(connection, true)
	return nil
}
