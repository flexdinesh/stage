package coordinator

import (
	"context"
	"errors"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

type LinkPreview struct {
	Attempt     int                   `json:"attempt"`
	Revision    int64                 `json:"revision"`
	Environment string                `json:"environment"`
	Observation execution.Observation `json:"observation"`
}

// PreviewExisting validates identity against the immutable launch destination.
// Reading a session never launches work or accepts task completion.
func (s *Service) PreviewExisting(ctx context.Context, id int64, link string) (LinkPreview, error) {
	unlock := s.lock(id)
	defer unlock()
	return s.previewExisting(ctx, id, link)
}
func (s *Service) previewExisting(ctx context.Context, id int64, link string) (LinkPreview, error) {
	var p LinkPreview
	t, err := s.store.GetTask(ctx, id)
	if err != nil {
		return p, err
	}
	if t.State != db.StateLaunching {
		return p, db.Conflict("This task has no unconfirmed launch.")
	}
	a, err := s.store.Attempt(ctx, id, t.LaunchAttempt)
	if err != nil {
		return p, err
	}
	adapter, err := s.conns.Adapter(t.ConnectionID)
	if err != nil {
		return p, err
	}
	resolver, ok := execution.As[execution.LinkResolver](adapter)
	if !ok {
		return p, db.Conflict("This harness cannot resolve session links.")
	}
	h := adapter.Health(ctx)
	if !h.Available {
		return p, db.Conflict("%s", h.Reason)
	}
	ref, err := resolver.ResolveLink(ctx, link)
	if err != nil {
		return p, db.Invalid("%s", err)
	}
	o, err := adapter.Observe(ctx, ref)
	if err != nil {
		return p, harnessErr(err)
	}
	if o.Session != ref {
		return p, db.Conflict("Harness returned a different session.")
	}
	if err = s.CheckObservation(t.Harness, o); err != nil {
		return p, err
	}
	dest, err := s.store.LaunchDestination(ctx, id, t.LaunchAttempt)
	if err != nil {
		return p, err
	}
	if o.Session.ConnectionID != dest.ConnectionID || o.Project != dest.ExternalProjectID {
		return p, db.Conflict("Session belongs to another launch destination.")
	}
	workspaces, err := adapter.Workspaces(ctx)
	if err != nil {
		return p, harnessErr(err)
	}
	verified := false
	for _, w := range workspaces {
		if w.ID == dest.ExternalProjectID && w.WorkspaceRoot == dest.Workspace && w.DeletedAt == nil {
			verified = true
		}
	}
	if !verified {
		return p, db.Conflict("The original workspace cannot be verified. Restore that exact workspace on the original connection before linking.")
	}
	if o.Child || o.Archived {
		return p, db.Conflict("Choose an active top-level session.")
	}
	owner, err := s.store.TaskBySession(ctx, ref)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return p, err
	}
	if err == nil && owner.ID != id {
		return p, db.Conflict("Session already belongs to task #%d.", owner.ID)
	}
	d, _ := s.conns.Descriptor(t.Harness)
	o.Facts = d.DisplayFacts(o.Facts)
	p = LinkPreview{Attempt: a.Number, Revision: a.Revision, Environment: h.Identity, Observation: o}
	return p, nil
}
func (s *Service) LinkExisting(ctx context.Context, id int64, link string, attempt int, revision int64, environment string) (db.Task, error) {
	unlock := s.lock(id)
	defer unlock()
	p, err := s.previewExisting(ctx, id, link)
	if err != nil {
		return db.Task{}, err
	}
	if p.Attempt != attempt || p.Revision != revision || p.Environment != environment {
		return db.Task{}, db.Conflict("Launch changed. Preview the session again.")
	}
	a, err := s.store.Attempt(ctx, id, attempt)
	if err != nil {
		return db.Task{}, err
	}
	if err = s.putObservation(ctx, p.Observation); err != nil {
		return db.Task{}, err
	}
	if err = s.store.FinishAttempt(ctx, a, p.Observation, true); err != nil {
		return db.Task{}, err
	}
	return s.store.GetTask(ctx, id)
}
