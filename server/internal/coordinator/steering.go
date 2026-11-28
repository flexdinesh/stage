package coordinator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// Stop asks the task's session to stop and records it. A harness declaring
// stop unavailable is 409 operation_unavailable; a catalog outage does not
// prevent the owning adapter from stopping a running session.
func (s *Service) Stop(ctx context.Context, task db.Task) error {
	if task.Session == nil {
		return db.Conflict("task has no execution session")
	}
	adapter, err := s.conns.Adapter(task.ConnectionID)
	if err != nil {
		return err
	}
	caps, catalogErr := adapter.Features(ctx)
	if op, declared := caps.Operations[execution.OpStop]; !op.Available && (catalogErr == nil || declared) {
		reason := strings.TrimSpace(op.Reason)
		if reason == "" {
			reason = "This connection does not support stopping tasks."
		}
		return &db.ConflictError{Code: "operation_unavailable", Msg: reason}
	}
	if err := adapter.Stop(ctx, *task.Session); err != nil {
		return &db.HarnessError{Err: err}
	}
	record, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return s.store.RecordTaskOperation(record, task.ID, *task.Session, "task_stopped", "Stop requested from Stage")
}

// SteeringAvailable: a connection hosts a steerer only when its send
// operation is available (ADR 0013, P2); otherwise 409
// operation_unavailable with the harness's reason.
func (s *Service) SteeringAvailable(ctx context.Context, connectionID int64) error {
	a, err := s.conns.Catalog(connectionID)
	if err != nil {
		return err
	}
	features, err := a.Features(ctx)
	if err != nil {
		return harnessErr(err)
	}
	if avail := features.Operations[execution.OpSend]; !avail.Available {
		reason := strings.TrimSpace(avail.Reason)
		if reason == "" {
			reason = "the harness reports send unavailable"
		}
		return &db.ConflictError{Code: "operation_unavailable", Msg: "Steering needs operation send: " + reason}
	}
	return nil
}

// SetSteerer creates and binds tree's steerer (db.SetTreeSteerer), stops a
// replaced steerer's session, then launches the new one. Stop and launch
// failures leave the binding as written and come back as warnings.
func (s *Service) SetSteerer(ctx context.Context, tree int64, in db.SteererInput) (db.SteererResult, []string, error) {
	if err := s.SteeringAvailable(ctx, in.Task.ConnectionID); err != nil {
		return db.SteererResult{}, nil, err
	}
	r, err := s.store.SetTreeSteerer(ctx, tree, in)
	if err != nil || r.Replayed {
		return r, nil, err
	}
	warnings := []string{}
	if r.Replaced != nil {
		warnings = append(warnings, s.stopSteerer(ctx, *r.Replaced)...)
	}
	if _, err := s.StartTask(ctx, r.TaskID); err != nil {
		s.log.Warn("start steerer", "task", r.TaskID, "tree", tree, "err", err)
		warnings = append(warnings, fmt.Sprintf("Steerer #%d is bound but did not start: %v", r.TaskID, err))
		if noteErr := s.store.NoteBlockedLaunch(ctx, r.TaskID, err.Error()); noteErr != nil {
			return r, warnings, noteErr
		}
	}
	return r, warnings, nil
}

// EndSteering ends tree's binding (stopped) and stops the steerer's session;
// a stop failure still ends the binding and comes back as a warning.
func (s *Service) EndSteering(ctx context.Context, tree int64) (int64, []string, error) {
	task, err := s.store.EndTreeSteering(ctx, tree, db.SteeringStopped)
	if err != nil {
		return 0, nil, err
	}
	return task, s.stopSteerer(ctx, task), nil
}

// stopSteerer stops the former steerer's session when it is active and
// returns why that failed (none: nothing to stop or stopped).
func (s *Service) stopSteerer(ctx context.Context, taskID int64) []string {
	t, err := s.store.GetTask(ctx, taskID)
	if err == nil && (t.Session == nil || !t.State.Active()) {
		return []string{}
	}
	if err == nil {
		err = s.Stop(ctx, t)
	}
	if err == nil {
		return []string{}
	}
	s.log.Warn("stop former steerer", "task", taskID, "err", err)
	return []string{fmt.Sprintf("Steering ended, but stopping steerer #%d failed: %v", taskID, err)}
}
