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
	"github.com/flexdinesh/stage/server/internal/workevent"
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
	if e := s.CheckObservation(task.Harness, o); e != nil {
		return e
	}
	var change *workevent.TaskChange
	var inferred bool
	err := s.store.WriteObservation(ctx, func(tx *db.Store) error {
		if err := tx.LockObservationConnection(ctx, o.Session.ConnectionID); err != nil {
			return err
		}
		current, err := tx.GetTask(ctx, task.ID)
		if err != nil {
			return err
		}
		change, inferred, err = s.ApplyObservation(ctx, tx, current, o, time.Now().UTC())
		return err
	})
	if err == nil {
		s.PublishObservation(ctx, change, inferred, false)
	}
	return err
}

// ApplyObservation reconciles a validated observation within the caller's
// unit of work. The caller publishes its effects after commit.
func (s *Service) ApplyObservation(ctx context.Context, tx *db.Store, task db.Task, o execution.Observation, at time.Time) (*workevent.TaskChange, bool, error) {
	if task.Session == nil || o.Session != *task.Session {
		return nil, false, errors.New("observation returned a different session")
	}
	previous, err := tx.Observation(ctx, o.Session)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return nil, false, err
	}
	if previous.UpdatedAt.After(o.UpdatedAt) {
		return nil, false, nil
	}
	d, _ := s.conns.Descriptor(task.Harness)
	o.Facts = d.DisplayFacts(o.Facts)
	if err := tx.PutObservation(ctx, o); err != nil {
		return nil, false, err
	}
	o, err = tx.Observation(ctx, o.Session)
	if err != nil {
		return nil, false, err
	}
	if task.State == db.StateBacklog && !task.Adopted {
		return nil, false, nil
	}
	// Idle never overwrites a known state, but it may carry a closure (a
	// Claude session removed with its state): inference reads the stored
	// task state, so only a task Stage recorded done can complete.
	var transition *workevent.TaskChange
	if o.State != "idle" && task.State != db.StateLaunching {
		detail := "execution observed " + o.State
		if o.StateReason != "" {
			detail += " (" + o.StateReason + ")"
		}
		change := db.Transition{TaskID: task.ID, From: []db.TaskState{task.State}, To: db.TaskState(o.State), Detail: detail, ObservedAt: &at}
		if task.Adopted && task.LaunchedAt == nil {
			change.ObservedStart = o.StartedAt
		}
		res, err := tx.TransitionTask(ctx, change)
		if err != nil {
			return nil, false, err
		}
		if res.Changed {
			transition = &workevent.TaskChange{TaskID: task.ID, From: res.From, To: change.To, Detail: detail}
		}
	}
	closure, ok := s.closure(task.Harness, o)
	if !ok {
		return transition, false, nil
	}
	inferred, err := tx.InferCompletion(ctx, task.ID, o, closure)
	return transition, inferred, err
}

func (s *Service) PublishObservation(ctx context.Context, change *workevent.TaskChange, inferred, inboxRecorded bool) {
	if change != nil {
		hook := s.OnTransition
		if inboxRecorded && s.OnObservedTransition != nil {
			hook = s.OnObservedTransition
		}
		if hook != nil {
			hook(ctx, *change)
		}
	}
	if inferred && s.OnAccepted != nil {
		s.OnAccepted()
	}
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
