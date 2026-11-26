package observe

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/workevent"
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
	// A silent harness's unowned sessions are current state only (the
	// waiting row and needs-input count): no inbox event, no push.
	kind := s.store.Harness(connection)
	d, _ := s.adapters.Descriptor(kind)
	registry := s.adapters.Harnesses()
	silent := d.Attention == harness.AttentionSilent
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
		// A session another harness wraps is that harness's to surface.
		if e != nil || o.Child || o.Session != ref || registry != nil && registry.Refusal(kind, o.Origin) != nil {
			continue
		}
		previous, e := s.store.Observation(ctx, ref)
		descriptor, _ := s.adapters.Descriptor(s.store.Harness(o.Session.ConnectionID))
		o.Facts = descriptor.DisplayFacts(o.Facts)
		if e = s.store.PutObservation(ctx, o); e != nil {
			return e
		}
		o, e = s.store.Observation(ctx, ref)
		if e != nil {
			return e
		}
		if _, err := s.store.TaskBySession(ctx, ref); err == nil {
			continue
		}
		if silent || !seeded || previous.State == o.State || !workevent.Notifiable(db.TaskState(o.State)) {
			continue
		}
		if previous.State == "" && !o.CreatedAt.After(s.started) {
			continue
		}
		prev := db.TaskState(previous.State)
		if s.OnSessionChange != nil {
			s.OnSessionChange(ctx, workevent.SessionChange{Session: &ref, ProjectID: o.Project, Title: o.Title, From: &prev, To: db.TaskState(o.State)})
		}
	}
	s.watchSeeded.Store(connection, true)
	return nil
}
