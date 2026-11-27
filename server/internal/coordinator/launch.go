package coordinator

import (
	"context"
	"errors"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/workevent"
)

func (s *Service) StartTask(ctx context.Context, id int64) (db.Task, error) {
	return s.launch(ctx, id, "launch requested", false)
}
func (s *Service) LaunchTask(ctx context.Context, id int64) (db.Task, error) {
	return s.launch(ctx, id, "launch requested", true)
}
func (s *Service) LaunchScheduled(ctx context.Context, id int64, detail string) (db.Task, error) {
	return s.launch(db.WithWorkCause(ctx, db.Cause{Kind: "scheduled_start"}), id, detail, false)
}
func (s *Service) launch(ctx context.Context, id int64, detail string, automatic bool) (db.Task, error) {
	unlock := s.lock(id)
	defer unlock()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
	defer cancel()
	t, e := s.store.GetTask(ctx, id)
	if e != nil {
		return t, e
	}
	if t.State == db.StateLaunching {
		a, e := s.store.Attempt(ctx, id, t.LaunchAttempt)
		if e != nil {
			return t, e
		}
		return s.resume(ctx, t, a)
	}
	if t.Adopted {
		return t, db.Conflict("An adopted session cannot be launched. Open it in its harness.")
	}
	a, e := s.conns.Adapter(t.ConnectionID)
	if e != nil {
		return t, e
	}
	if h := a.Health(ctx); !h.Available {
		return t, &db.ConflictError{Code: "connection_unavailable", Msg: h.Reason}
	}
	features, e := a.Features(ctx)
	if e != nil {
		return t, harnessErr(e)
	}
	if op := features.Operations[execution.OpLaunch]; !op.Available {
		return t, &db.ConflictError{Code: "operation_unavailable", Msg: "Launch unavailable: " + op.Reason}
	}
	d, _ := s.conns.Descriptor(t.Harness)
	correlated := d.Launch == harness.LaunchCorrelated
	if _, ok := execution.As[execution.Correlator](a); correlated && !ok {
		return t, db.Conflict("Launch unavailable: this harness cannot reconcile a launch it does not reserve.")
	}
	if _, _, e = s.workspaces.RefreshInventory(ctx, t.ConnectionID); e != nil {
		var failed *db.HarnessError
		if errors.As(e, &failed) {
			return t, &db.ConflictError{Code: "connection_unavailable", Msg: "Cannot verify the workspace. Check its connection in Settings."}
		}
		return t, e
	}
	config := t.ExecutionConfig
	if config == nil {
		return t, db.Invalid("executionConfig is required for this harness")
	}
	claim, e := s.transition(ctx, db.Transition{TaskID: id, From: []db.TaskState{db.StateBacklog, db.StateFailed, db.StateInterrupted, db.StateCancelled}, To: db.StateLaunching, Detail: detail, BeginAttempt: true, RequireAuto: automatic})
	if e != nil {
		return t, e
	}
	if !claim.Changed {
		return t, db.Conflict("task is %s; cannot start", claim.From)
	}
	t, e = s.store.GetTask(ctx, id)
	if e != nil {
		return t, e
	}
	// Until the immutable intent is saved, no adapter effect is allowed.
	prepared := false
	defer func() {
		if !prepared {
			if att, err := s.store.Attempt(ctx, id, t.LaunchAttempt); err == nil && att.Intent == nil && att.Phase == "prepared" {
				_ = s.store.RejectAttempt(ctx, att, "Launch preparation failed before dispatch. Review the error and start again.")
			}
		}
	}()
	dest, e := s.store.LaunchDestination(ctx, id, t.LaunchAttempt)
	if e != nil {
		return t, e
	}
	handoff, e := s.prepareHandoff(ctx, t)
	if e != nil {
		return t, e
	}
	journal, e := s.store.JournalLaunchContext(ctx, t, journalInstructions(t, s.attribution(t.Harness), s.httpAPI(t.ConnectionID), s.CLI))
	if e != nil {
		return t, e
	}
	prompt := t.Prompt
	if handoff != "" {
		prompt += "\n\n" + handoff
	}
	prompt += "\n\n" + journal
	images, e := s.store.TaskAttachments(ctx, id)
	if e != nil {
		return t, e
	}
	in := execution.Intent{RequestID: claim.RequestID, ConnectionID: t.ConnectionID, Project: dest.ExternalProjectID, Directory: dest.Workspace, Title: t.Title, Prompt: prompt, Config: *config, Images: []execution.Image{}}
	if correlated {
		in.Marker = execution.LaunchMarker(claim.RequestID)
		in.Prompt += "\n\n" + in.Marker
	}
	for _, img := range images {
		in.Images = append(in.Images, execution.Image{Name: img.Name, MimeType: img.MimeType, Data: img.Data})
	}
	if e = s.store.PrepareAttempt(ctx, id, t.LaunchAttempt, in); e != nil {
		return t, e
	}
	prepared = true
	attempt, e := s.store.Attempt(ctx, id, t.LaunchAttempt)
	if e != nil {
		return t, e
	}
	return s.resume(ctx, t, attempt)
}

// ReconcileLaunch resumes a launching task when its retry is due.
func (s *Service) ReconcileLaunch(ctx context.Context, task db.Task) error {
	unlock := s.lock(task.ID)
	a, err := s.store.Attempt(ctx, task.ID, task.LaunchAttempt)
	if err == nil && (a.RetryAt == nil || !time.Now().Before(*a.RetryAt)) {
		_, err = s.resume(ctx, task, a)
	}
	unlock()
	return err
}

func (s *Service) resume(ctx context.Context, t db.Task, attempt db.Attempt) (db.Task, error) {
	if attempt.Phase == "confirmed" || attempt.Phase == "rejected" {
		return s.store.GetTask(ctx, t.ID)
	}
	if attempt.Intent == nil && attempt.Phase == "prepared" {
		if e := s.store.RejectAttempt(ctx, attempt, "Launch preparation was interrupted before dispatch. Review the task and start again."); e != nil {
			return t, e
		}
		return s.store.GetTask(ctx, t.ID)
	}
	if attempt.Intent == nil {
		return t, db.Conflict("Launch identity is incomplete. Link the existing session.")
	}
	a, e := s.conns.Adapter(attempt.Intent.ConnectionID)
	if e != nil {
		return t, e
	}
	call, cancel := context.WithTimeout(ctx, s.LaunchAttemptTimeout)
	defer cancel()
	workspaces, e := a.Workspaces(call)
	if e != nil {
		return t, harnessErr(e)
	}
	verified := false
	for _, w := range workspaces {
		if w.ID == attempt.Intent.Project && w.WorkspaceRoot == attempt.Intent.Directory && w.DeletedAt == nil {
			verified = true
		}
	}
	if !verified {
		return t, db.Conflict("The original launch workspace changed. Restore it before reconciling this attempt.")
	}
	save := func(ctx context.Context, cp execution.Checkpoint) error {
		return s.store.SaveCheckpoint(ctx, &attempt, cp)
	}
	var o execution.Observation
	if attempt.Intent.Marker != "" {
		o, e = s.correlated(call, a, &attempt, save)
	} else {
		for retry := 0; retry < 2; retry++ {
			o, e = a.Launch(call, *attempt.Intent, attempt.Checkpoint, save)
			var rejected *execution.Rejected
			if e == nil || errors.As(e, &rejected) || call.Err() != nil {
				break
			}
		}
	}
	// Record the outcome even after the external timeout.
	record, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer done()
	if e == nil {
		e = s.CheckObservation(t.Harness, o)
	}
	if e == nil && (o.Session.ConnectionID != attempt.Intent.ConnectionID || o.Project != attempt.Intent.Project) {
		e = errors.New("launch returned a different destination")
	}
	if e != nil {
		var rejection *execution.Rejected
		if errors.As(e, &rejection) {
			if err := s.store.RejectAttempt(record, attempt, e.Error()); err != nil {
				return t, err
			}
		} else {
			if err := s.store.Unconfirmed(record, attempt, e, nil); err != nil {
				return t, err
			}
		}
		t, _ = s.store.GetTask(record, t.ID)
		return t, harnessErr(e)
	}
	// The harness returned a session; a failure to record it keeps the
	// attempt unconfirmed with the cause, a backoff and the session seen.
	if e = s.putObservation(record, o); e == nil {
		e = s.store.FinishAttempt(record, attempt, o, false)
	}
	if e != nil {
		if err := s.store.Unconfirmed(record, attempt, e, db.Observed(o)); err != nil {
			return t, errors.Join(e, err)
		}
		return t, e
	}
	if s.OnTransition != nil {
		s.OnTransition(record, workevent.TaskChange{TaskID: t.ID, From: db.StateLaunching, To: db.TaskState(o.State),
			Detail: "execution confirmed", Attribution: db.WorkAttribution(db.AttemptOutcome(record, attempt, &o.Session))})
	}
	return s.store.GetTask(record, t.ID)
}

// correlated dispatches a correlated launch once: the dispatch is recorded
// (the attempt leaves prepared) before the harness creates anything. Once
// dispatched, the outcome is found by the intent's marker and never launched
// again; not found keeps the attempt unconfirmed until the user links it.
func (s *Service) correlated(ctx context.Context, a execution.Launcher, attempt *db.Attempt, save execution.SaveCheckpoint) (execution.Observation, error) {
	if attempt.Phase == "prepared" {
		if err := save(ctx, attempt.Checkpoint); err != nil {
			return execution.Observation{}, err
		}
		return a.Launch(ctx, *attempt.Intent, attempt.Checkpoint, save)
	}
	c, ok := execution.As[execution.Correlator](a)
	if !ok {
		return execution.Observation{}, errors.New("this harness cannot find a correlated launch; link the session")
	}
	o, err := c.Correlate(ctx, *attempt.Intent)
	if errors.Is(err, execution.ErrNotFound) {
		return o, errors.New("no session carries this launch's marker; if it started, link the session")
	}
	return o, err
}
