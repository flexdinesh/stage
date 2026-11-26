package sync

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/workevent"
)

type ThreadChange = workevent.SessionChange

func Notifiable(s db.TaskState) bool { return workevent.Notifiable(s) }
func (s *Service) WatchThreads(ctx context.Context) error {
	for id := range s.connections() {
		if err := s.watchConnection(ctx, id); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) watchConnection(ctx context.Context, connection int64) error {
	a, err := s.Adapter(connection)
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
	d, _ := s.descriptor(kind)
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
	// The legacy session mirror holds only sessions of harnesses that keep
	// one; for the others these are no-ops.
	ids, e := s.store.WatchedThreadIDs(ctx, connection)
	if e != nil {
		return e
	}
	for _, id := range ids {
		refs = append(refs, execution.SessionRef{ConnectionID: connection, ExternalSessionID: id})
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		if seen[ref.ExternalSessionID] {
			continue
		}
		seen[ref.ExternalSessionID] = true
		o, e := a.Observe(ctx, ref)
		// A session another harness wraps is that harness's to surface.
		if e != nil || o.Child || o.Session != ref || s.refusal(kind, o) != nil {
			continue
		}
		previous, e := s.store.Observation(ctx, ref)
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
		_ = s.store.SetObservedState(ctx, ref, db.TaskState(o.State))
		if silent || !seeded || previous.State == o.State || !Notifiable(db.TaskState(o.State)) {
			continue
		}
		if previous.State == "" && !o.CreatedAt.After(s.started) {
			continue
		}
		prev := db.TaskState(previous.State)
		if s.OnThreadChange != nil {
			s.OnThreadChange(ctx, ThreadChange{Session: &ref, ThreadID: ref.ExternalSessionID, ProjectID: o.Project, Title: o.Title, From: &prev, To: db.TaskState(o.State)})
		}
	}
	s.watchSeeded.Store(connection, true)
	return nil
}
