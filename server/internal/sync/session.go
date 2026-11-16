package sync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/t3"
)

func (s *Service) readSession(ctx context.Context, id string) (t3.ThreadDetail, error) {
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	d, err := s.t3.ReadThread(callCtx, id)
	if err != nil {
		return d, err
	}
	if d.ThreadID != id {
		return d, fmt.Errorf("T3 returned a different session for %s", id)
	}
	if err := s.mirrorDetail(ctx, d); err != nil {
		return d, err
	}
	return d, nil
}

// RefreshSession reads a session from T3 into the mirror without changing its task.
func (s *Service) RefreshSession(ctx context.Context, id string) error {
	_, err := s.readSession(ctx, id)
	if errors.Is(err, t3.ErrNotFound) {
		return db.ErrNotFound
	}
	return t3Err(err)
}
