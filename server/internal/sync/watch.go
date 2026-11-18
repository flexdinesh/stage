package sync

import (
	"context"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/workevent"
)

type ThreadChange = workevent.SessionChange

func Notifiable(s db.TaskState) bool { return workevent.Notifiable(s) }
func (s *Service) WatchThreads(ctx context.Context) error {
	for connection, a := range s.adapters {
		if !a.Health(ctx).Available {
			continue
		}
		refs, e := a.Attention(ctx)
		if e != nil {
			continue
		}
		stored, e := s.store.WatchedSessions(ctx, connection)
		if e != nil {
			return e
		}
		refs = append(refs, stored...)
		if connection == 1 {
			ids, e := s.store.WatchedThreadIDs(ctx)
			if e != nil {
				return e
			}
			for _, id := range ids {
				refs = append(refs, execution.SessionRef{ConnectionID: connection, ExternalSessionID: id})
			}
		}
		seen := map[string]bool{}
		for _, ref := range refs {
			if seen[ref.ExternalSessionID] {
				continue
			}
			seen[ref.ExternalSessionID] = true
			o, e := a.Observe(ctx, ref)
			if e != nil || o.Child || o.Session != ref {
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
			if connection == 1 {
				_ = s.store.SetObservedState(ctx, ref.ExternalSessionID, db.TaskState(o.State))
			}
			if !s.watchSeeded.Load() || previous.State == o.State || !Notifiable(db.TaskState(o.State)) {
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
	}
	s.watchSeeded.Store(true)
	return nil
}
