package coordinator

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// interactive is the task's session and its adapter's Interactive when the
// harness offers op; otherwise an operation_unavailable conflict naming why.
func (s *Service) interactive(ctx context.Context, id int64, op execution.Op) (db.Task, execution.Interactive, error) {
	t, err := s.store.GetTask(ctx, id)
	if err != nil {
		return t, nil, err
	}
	if t.Session == nil {
		return t, nil, db.Conflict("task has no execution session")
	}
	a, err := s.Adapter(t.ConnectionID)
	if err != nil {
		return t, nil, err
	}
	in, ok := execution.As[execution.Interactive](a)
	if !ok {
		return t, nil, unavailable(op, "this harness does not support it")
	}
	features, err := a.Features(ctx)
	if err != nil {
		return t, nil, harnessErr(err)
	}
	if avail := features.Operations[op]; !avail.Available {
		return t, nil, unavailable(op, avail.Reason)
	}
	return t, in, nil
}

func unavailable(op execution.Op, reason string) error {
	if strings.TrimSpace(reason) == "" {
		reason = "the harness reports it unavailable"
	}
	return &db.ConflictError{Code: "operation_unavailable", Msg: "Operation " + string(op) + " unavailable: " + reason + "."}
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

// Respond answers one pending request, then observes the session once.
func (s *Service) Respond(ctx context.Context, id int64, requestID string, r execution.Response) (db.Task, error) {
	if strings.TrimSpace(requestID) == "" {
		return db.Task{}, db.Invalid("request id is required")
	}
	if r.Option == "" && strings.TrimSpace(r.Text) == "" && len(r.Answers) == 0 {
		return db.Task{}, db.Invalid("respond with an option, text or answers")
	}
	t, in, err := s.interactive(ctx, id, execution.OpRespond)
	if err != nil {
		return t, err
	}
	var invalid *execution.Invalid
	if err = in.Respond(ctx, *t.Session, requestID, r); errors.Is(err, execution.ErrNotFound) {
		return t, fmt.Errorf("%w: request %q is no longer pending", db.ErrNotFound, requestID)
	} else if errors.As(err, &invalid) {
		return t, db.Invalid("%s", invalid.Reason)
	} else if err != nil {
		return t, harnessErr(err)
	}
	return s.observeAfter(ctx, t)
}

// MaxMessage bounds a message's text (bytes): a harness may pass it as one
// command-line argument.
const MaxMessage = 32 << 10

// FollowUpSent is the task event a sent message records.
const FollowUpSent = "Follow-up sent from Stage"

// Send sends the task's session a message, records it on the task's
// timeline, then observes the session once. *execution.Busy (the session
// can't take it now) is 409 session_busy.
func (s *Service) Send(ctx context.Context, id int64, m execution.Message) (db.Task, error) {
	if strings.TrimSpace(m.Text) == "" {
		return db.Task{}, db.Invalid("message text is required")
	}
	if len(m.Text) > MaxMessage {
		return db.Task{}, db.Invalid("message text is longer than %d KiB", MaxMessage>>10)
	}
	t, in, err := s.interactive(ctx, id, execution.OpSend)
	if err != nil {
		return t, err
	}
	if err = in.Send(ctx, *t.Session, m); err != nil {
		return t, harnessErr(err)
	}
	if err := s.store.AddTaskEvent(ctx, t.ID, FollowUpSent); err != nil {
		s.log.Warn("record follow-up", "task", t.ID, "err", err)
	}
	return s.observeAfter(ctx, t)
}

// observeAfter polls t once after an interaction (best effort) and rereads it.
func (s *Service) observeAfter(ctx context.Context, t db.Task) (db.Task, error) {
	if err := s.PollTask(ctx, t); err != nil {
		s.log.Warn("observe after interaction", "task", t.ID, "err", err)
	}
	return s.store.GetTask(ctx, t.ID)
}
