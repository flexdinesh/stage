package coordinator

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func (s *Service) queueManager(ctx context.Context, id int64) (db.Task, execution.QueueManager, error) {
	t, a, err := s.sessionOperation(ctx, id, execution.OpQueueManage)
	if err != nil {
		return t, nil, err
	}
	manager, ok := execution.As[execution.QueueManager](a)
	if !ok {
		return t, nil, unavailable(execution.OpQueueManage, "this connection does not provide queue management")
	}
	return t, manager, nil
}

// Queued reads the current session's native queue.
func (s *Service) Queued(ctx context.Context, id int64) ([]execution.QueuedMessage, error) {
	t, manager, err := s.queueManager(ctx, id)
	if err != nil {
		return nil, err
	}
	messages, err := manager.Queued(ctx, *t.Session)
	if errors.Is(err, execution.ErrNotFound) {
		return nil, fmt.Errorf("%w: execution session is gone", db.ErrNotFound)
	}
	if err != nil {
		return nil, harnessErr(err)
	}
	return execution.BoundQueuedMessages(messages), nil
}

// CancelQueued removes a pending message from the current session's queue.
func (s *Service) CancelQueued(ctx context.Context, id int64, queueID string) error {
	return s.changeQueue(ctx, id, queueID, false)
}

// PromoteQueued changes a pending message into a steer.
func (s *Service) PromoteQueued(ctx context.Context, id int64, queueID string) error {
	return s.changeQueue(ctx, id, queueID, true)
}

func (s *Service) changeQueue(ctx context.Context, id int64, queueID string, steer bool) error {
	if strings.TrimSpace(queueID) == "" {
		return db.Invalid("queued message id is required")
	}
	defer s.lock(id)()
	t, manager, err := s.queueManager(ctx, id)
	if err != nil {
		return err
	}
	detail := "Queued message cancelled from Stage"
	if steer {
		detail = "Queued message steered from Stage"
		err = manager.PromoteQueued(ctx, *t.Session, queueID)
	} else {
		err = manager.CancelQueued(ctx, *t.Session, queueID)
	}
	var invalid *execution.Invalid
	switch {
	case errors.Is(err, execution.ErrNotFound):
		return fmt.Errorf("%w: queued message is no longer pending", db.ErrNotFound)
	case errors.As(err, &invalid):
		return db.Invalid("%s", invalid.Reason)
	case err != nil:
		return harnessErr(err)
	}
	record, cancel := recordContext(ctx, db.Cause{Kind: "session_queue", TaskID: id, Session: db.CallerRef(ctx)})
	defer cancel()
	if err := s.store.AddTaskEvent(record, id, detail); err != nil {
		s.log.Warn("record queue action", "task", id, "err", err)
	}
	return nil
}
