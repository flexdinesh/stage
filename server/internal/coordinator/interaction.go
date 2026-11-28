package coordinator

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// interactive is the task's session and its adapter's Interactive when the
// harness offers op; otherwise an operation_unavailable conflict naming why.
func (s *Service) interactive(ctx context.Context, id int64, op execution.Op) (db.Task, execution.Interactive, error) {
	t, a, err := s.sessionOperation(ctx, id, op)
	if err != nil {
		return t, nil, err
	}
	in, ok := execution.As[execution.Interactive](a)
	if !ok {
		return t, nil, unavailable(op, "this connection does not provide session interaction")
	}
	return t, in, nil
}

// sessionOperation resolves the task's current session and checks its
// connection's declaration. The caller checks the corresponding trait.
func (s *Service) sessionOperation(ctx context.Context, id int64, op execution.Op) (db.Task, execution.Catalog, error) {
	t, err := s.store.GetTask(ctx, id)
	if err != nil {
		return t, nil, err
	}
	if t.Session == nil {
		return t, nil, db.Conflict("task has no execution session")
	}
	a, err := s.connectionOperation(ctx, t.Session.ConnectionID, op)
	return t, a, err
}

func (s *Service) connectionOperation(ctx context.Context, connectionID int64, op execution.Op) (execution.Catalog, error) {
	a, err := s.conns.Catalog(connectionID)
	if err != nil {
		return nil, err
	}
	features, err := a.Features(ctx)
	if err != nil {
		return nil, harnessErr(err)
	}
	if avail := features.Operations[op]; !avail.Available {
		return nil, unavailable(op, avail.Reason)
	}
	return a, nil
}

// CanAnswer reports whether connection id offers respond or send, so a
// steerer can answer its tasks' waits (ADR 0013 notification hold).
func (s *Service) CanAnswer(ctx context.Context, id int64) (bool, error) {
	a, err := s.conns.Catalog(id)
	if err != nil {
		return false, err
	}
	features, err := a.Features(ctx)
	if err != nil {
		return false, harnessErr(err)
	}
	return features.Operations[execution.OpRespond].Available || features.Operations[execution.OpSend].Available, nil
}

func unavailable(op execution.Op, reason string) error {
	if strings.TrimSpace(reason) == "" {
		reason = "the harness reports it unavailable"
	}
	return &db.ConflictError{Code: "operation_unavailable", Msg: "Operation " + string(op) + " unavailable: " + reason}
}

// Requests lists what the task's session waits on (operation respond).
func (s *Service) Requests(ctx context.Context, id int64) ([]execution.Request, error) {
	t, in, err := s.interactive(ctx, id, execution.OpRespond)
	if err != nil {
		return nil, err
	}
	rs, err := in.Requests(ctx, *t.Session)
	if err != nil {
		return nil, harnessErr(err)
	}
	if rs == nil {
		rs = []execution.Request{}
	}
	return rs, nil
}

// Activity reads an attempt's recent activity (OpActivity). A nil attempt
// selects the task's current session.
func (s *Service) Activity(ctx context.Context, id int64, attempt *int, q execution.ActivityQuery) (execution.Activity, error) {
	var ref execution.SessionRef
	if attempt == nil {
		t, err := s.store.GetTask(ctx, id)
		if err != nil {
			return execution.Activity{}, err
		}
		if t.Session == nil {
			return execution.Activity{}, db.Conflict("task has no execution session")
		}
		ref = *t.Session
	} else {
		historical, err := s.store.TaskSession(ctx, id, *attempt)
		if err != nil {
			return execution.Activity{}, err
		}
		ref = historical.Session
	}
	a, err := s.connectionOperation(ctx, ref.ConnectionID, execution.OpActivity)
	if err != nil {
		return execution.Activity{}, err
	}
	reader, ok := execution.As[execution.ActivityReader](a)
	if !ok {
		return execution.Activity{}, unavailable(execution.OpActivity, "this connection does not provide session activity")
	}
	q = q.Bounded()
	out, err := reader.Activity(ctx, ref, q)
	if err != nil {
		return execution.Activity{}, harnessErr(err)
	}
	out, err = out.Bounded(q)
	return out, harnessErr(err)
}

// Respond answers one pending request, then observes the session once.
// expected (expectedStateRevision) guards it: first answer wins (answer).
func (s *Service) Respond(ctx context.Context, id int64, requestID string, r execution.Response, expected *int64) (db.Task, error) {
	if strings.TrimSpace(requestID) == "" {
		return db.Task{}, db.Invalid("request id is required")
	}
	if r.Option == "" && strings.TrimSpace(r.Text) == "" && len(r.Answers) == 0 {
		return db.Task{}, db.Invalid("respond with an option, text or answers")
	}
	t, err := s.answer(ctx, id, execution.OpRespond, expected, func(t db.Task, in execution.Interactive) error {
		var invalid *execution.Invalid
		if err := in.Respond(ctx, *t.Session, requestID, r); errors.Is(err, execution.ErrNotFound) {
			return fmt.Errorf("%w: request %q is no longer pending", db.ErrNotFound, requestID)
		} else if errors.As(err, &invalid) {
			return db.Invalid("%s", invalid.Reason)
		} else if err != nil {
			return harnessErr(err)
		}
		record, cancel := recordContext(ctx, db.Cause{Kind: "session_request", TaskID: id, Session: db.CallerRef(ctx), RequestID: requestID})
		defer cancel()
		return s.store.RecordTaskAnswer(record, id, *t.Session, "session_response", "Answered a session request", expected)
	})
	if err != nil {
		return t, err
	}
	return s.observeAfter(ctx, t)
}

// MaxMessage bounds a message's text (bytes): a harness may pass it as one
// command-line argument.
const MaxMessage = 32 << 10

// Delivery events record the action without the message text.
const (
	FollowUpSent  = "Follow-up sent from Stage"
	SteerSent     = "Steer sent from Stage"
	MessageQueued = "Message queued from Stage"
)

// Send sends the task's session a message, records it on the task's
// timeline, then observes the session once. *execution.Busy (the session
// can't take it now) is 409 session_busy. expected (expectedStateRevision)
// guards it: first answer wins (answer).
func (s *Service) Send(ctx context.Context, id int64, m execution.Message, expected *int64) (db.Task, error) {
	return s.send(ctx, id, m, expected, nil)
}

// SendRecorded delivers a steering digest as a new turn. Use the native
// queue when a turn is active and the connection declares it. Older
// adapters can still implement queueing on Send. The caller records
// delivery in place of the ordinary message event.
func (s *Service) SendRecorded(ctx context.Context, id int64, m execution.Message, record func(context.Context) error) (db.Task, error) {
	t, err := s.store.GetTask(ctx, id)
	if err != nil {
		return t, err
	}
	active := t.State == db.StateRunning
	if t.State == db.StateNeedsInput && t.Session != nil {
		obs, err := s.store.Observation(ctx, *t.Session)
		if err != nil && !errors.Is(err, db.ErrNotFound) {
			return t, err
		}
		active = obs.Active
	}
	if active && t.Session != nil {
		a, err := s.conns.Catalog(t.Session.ConnectionID)
		if err != nil {
			return t, err
		}
		features, err := a.Features(ctx)
		if err != nil {
			return t, harnessErr(err)
		}
		if features.Operations[execution.OpQueue].Available {
			m.Delivery = execution.DeliveryQueue
		}
	}
	return s.send(ctx, id, m, nil, record)
}

func (s *Service) send(ctx context.Context, id int64, m execution.Message, expected *int64, recorder func(context.Context) error) (db.Task, error) {
	if strings.TrimSpace(m.Text) == "" {
		return db.Task{}, db.Invalid("message text is required")
	}
	if len(m.Text) > MaxMessage {
		return db.Task{}, db.Invalid("message text is longer than %d KiB", MaxMessage>>10)
	}
	op, err := m.Delivery.Operation()
	if err != nil {
		return db.Task{}, db.Invalid("%s", err)
	}
	detail := FollowUpSent
	switch op {
	case execution.OpSteer:
		detail = SteerSent
	case execution.OpQueue:
		detail = MessageQueued
	}
	if m.RequestID == "" {
		m.RequestID = "stage-send:" + rand.Text()
	}
	t, err := s.answer(ctx, id, op, expected, func(t db.Task, in execution.Interactive) error {
		if err := in.Send(ctx, *t.Session, m); err != nil {
			var invalid *execution.Invalid
			if errors.As(err, &invalid) {
				return db.Invalid("%s", invalid.Reason)
			}
			return harnessErr(err)
		}
		record, cancel := recordContext(ctx, db.Cause{Kind: "session_message", TaskID: id, Session: db.CallerRef(ctx)})
		defer cancel()
		if recorder != nil {
			return recorder(record)
		}
		if expected != nil {
			return s.store.RecordTaskAnswer(record, id, *t.Session, "", detail, expected)
		}
		if err := s.store.AddTaskEvent(record, id, detail); err != nil {
			s.log.Warn("record session message", "task", id, "err", err)
		}
		return nil
	})
	if err != nil {
		return t, err
	}
	return s.observeAfter(ctx, t)
}

// answer runs dispatch on task id's session when its harness offers op.
// With expected it is a guarded answer (ADR 0013 first answer wins): it
// holds the task's lock through dispatch and its recording, and requires
// the task in needs_input at state revision expected, not yet answered
// there; otherwise *db.AlreadyAnsweredError (409 already_answered).
// Without expected nothing is locked or required.
func (s *Service) answer(ctx context.Context, id int64, op execution.Op, expected *int64, dispatch func(db.Task, execution.Interactive) error) (db.Task, error) {
	if expected != nil {
		defer s.lock(id)()
	}
	t, in, err := s.interactive(ctx, id, op)
	if err != nil {
		return t, err
	}
	if expected != nil && (t.State != db.StateNeedsInput || t.StateRevision != *expected || (t.AnsweredRevision != nil && *t.AnsweredRevision == *expected)) {
		return t, &db.AlreadyAnsweredError{State: t.State, StateRevision: t.StateRevision}
	}
	return t, dispatch(t, in)
}

// recordContext records a dispatched interaction even when the client
// stopped waiting, attributed to cause (the caller session, the target task).
func recordContext(ctx context.Context, cause db.Cause) (context.Context, context.CancelFunc) {
	record, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	return db.WithWorkCause(record, cause), cancel
}

// observeAfter polls t once after an interaction (best effort) and rereads it.
func (s *Service) observeAfter(ctx context.Context, t db.Task) (db.Task, error) {
	if err := s.PollTask(ctx, t); err != nil {
		s.log.Warn("observe after interaction", "task", t.ID, "err", err)
	}
	return s.store.GetTask(ctx, t.ID)
}
