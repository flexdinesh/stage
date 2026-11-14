package sync

import (
	"context"
	"errors"
	"time"

	"github.com/flexdinesh/stage/server/internal/t3"
)

// lastWordMaxChars caps the stored reply excerpt (T3 UTF-16 units).
const lastWordMaxChars = 4000

// mirrorDetail upserts a t3_thread_read result, then refreshes the thread's
// last word (latest assistant reply) when due. Last-word failures are logged
// and retried on a later read; they never fail the caller.
func (s *Service) mirrorDetail(ctx context.Context, d t3.ThreadDetail) error {
	if err := s.store.UpsertThreadDetail(ctx, d); err != nil {
		return err
	}
	if err := s.refreshLastWord(ctx, d); err != nil && ctx.Err() == nil {
		s.log.Warn("refresh last word", "thread", d.ThreadID, "err", err)
	}
	return nil
}

// refreshLastWord reads the latest reply only when the thread is at rest (no
// active run, or waiting on input), has items, and its updatedAt differs from
// the last check. No reply found advances the marker and keeps the stored one.
func (s *Service) refreshLastWord(ctx context.Context, d t3.ThreadDetail) error {
	atRest := d.ActiveRunID == "" || d.PendingRequestCount > 0
	if !atRest || d.ItemCount == 0 || d.UpdatedAt.IsZero() {
		return nil
	}
	updated := d.UpdatedAt.Truncate(time.Microsecond) // Postgres precision
	checked, err := s.store.LastWordCheckedAt(ctx, d.ThreadID)
	if err != nil {
		return err
	}
	if checked != nil && checked.Equal(updated) {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	r, err := s.t3.LatestReply(callCtx, t3.LatestReplyInput{ThreadID: d.ThreadID, MaxChars: lastWordMaxChars, ItemCount: d.ItemCount})
	cancel()
	switch {
	case errors.Is(err, t3.ErrNoReply):
		return s.store.SetThreadLastWord(ctx, d.ThreadID, updated, nil)
	case err != nil:
		return err
	}
	return s.store.SetThreadLastWord(ctx, d.ThreadID, updated, &r)
}
