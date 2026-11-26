package coordinator

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

func (s *Service) PollTask(ctx context.Context, task db.Task) error {
	if task.Session == nil || task.State == db.StateLaunching {
		return nil
	}
	a, e := s.conns.Sessions(task.ConnectionID)
	if e != nil {
		return e
	}
	call, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	o, e := a.Observe(call, *task.Session)
	if e != nil {
		_ = s.store.ObservationUnavailable(ctx, *task.Session, e.Error())
		return harnessErr(e)
	}
	return s.Apply(ctx, task, o)
}

// Apply records an observation of task's session, polled or pushed, and
// moves the task to its state.
func (s *Service) Apply(ctx context.Context, task db.Task, o execution.Observation) error {
	if o.Session != *task.Session {
		return errors.New("observation returned a different session")
	}
	if e := s.CheckObservation(task.Harness, o); e != nil {
		return e
	}
	if e := s.putObservation(ctx, o); e != nil {
		return e
	}
	o, e := s.store.Observation(ctx, *task.Session)
	if e != nil {
		return e
	}
	if task.State == db.StateBacklog && !task.Adopted {
		return nil
	}
	// Idle never overwrites a known state, but it may carry a closure (a
	// Claude session removed with its state): inference reads the stored
	// task state, so only a task Stage recorded done can complete.
	if o.State != "idle" {
		detail := "execution observed " + o.State
		if o.StateReason != "" {
			detail += " (" + o.StateReason + ")"
		}
		change := db.Transition{TaskID: task.ID, From: []db.TaskState{task.State}, To: db.TaskState(o.State), Detail: detail}
		if task.Adopted && task.LaunchedAt == nil {
			change.ObservedStart = o.StartedAt
		}
		if _, e = s.transition(ctx, change); e != nil {
			return e
		}
	}
	closure, ok := s.closure(task.Harness, o)
	if !ok {
		return nil
	}
	inferred, e := s.store.InferCompletion(ctx, task.ID, o, closure)
	if e != nil || !inferred {
		return e
	}
	s.log.Info("completion inferred from harness closure", "task", task.ID, "signal", o.Closed.Signal, "execution", o.ExecutionID)
	if s.OnAccepted != nil {
		s.OnAccepted()
	}
	return nil
}

// refusal refuses o, a session of kind that another registered harness
// wraps (its origin among that harness's Wraps): the wrapper tracks it.
// Nil: Stage may track it.
func (s *Service) refusal(kind string, o execution.Observation) error {
	if s.conns.Harnesses() == nil {
		return nil
	}
	if r := s.conns.Harnesses().Refusal(kind, o.Origin); r != nil {
		return harnessErr(r)
	}
	return nil
}

// CheckObservation validates o and checks that kind declares its state reason
// before any observation changes are applied.
func (s *Service) CheckObservation(kind string, o execution.Observation) error {
	if err := o.Valid(); err != nil {
		return err
	}
	d, _ := s.conns.Descriptor(kind)
	if o.StateReason != "" && !slices.Contains(d.StateReasons, o.StateReason) {
		return fmt.Errorf("malformed observation: harness %q did not declare state reason %q", kind, o.StateReason)
	}
	return s.refusal(kind, o)
}

// closure is the harness's declared closure for o's signal.
func (s *Service) closure(kind string, o execution.Observation) (harness.Closure, bool) {
	d, ok := s.conns.Descriptor(kind)
	if o.Closed == nil || !ok {
		return harness.Closure{}, false
	}
	return d.Closure(o.Closed.Signal)
}
