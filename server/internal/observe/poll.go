package observe

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
)

func (s *Service) PollActive(ctx context.Context) error {
	var errs []error
	for id := range s.adapters.All() {
		errs = append(errs, s.pollConnection(ctx, id))
	}
	return errors.Join(errs...)
}
func (s *Service) pollConnection(ctx context.Context, id int64) error {
	v, _ := s.lastStalePoll.LoadOrStore(id, &atomic.Int64{})
	lastPoll := v.(*atomic.Int64)
	now := time.Now()
	last := lastPoll.Load()
	stale := now.Sub(time.Unix(0, last)) >= s.StalePollEvery && lastPoll.CompareAndSwap(last, now.UnixNano())
	tasks, e := s.store.PollableTasks(ctx, stale)
	if e != nil {
		return e
	}
	for _, t := range tasks {
		if t.ConnectionID != id {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if t.State == db.StateLaunching {
			if err := s.coord.ReconcileLaunch(ctx, t); err != nil {
				s.log.Warn("reconcile launch", "task", t.ID, "err", err)
			}
		} else if t.Session != nil {
			if e = s.coord.PollTask(ctx, t); e != nil {
				s.log.Warn("observe task", "task", t.ID, "err", e)
			}
		}
	}
	return nil
}
