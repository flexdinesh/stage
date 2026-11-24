package db

import (
	"context"
	"fmt"
	"github.com/flexdinesh/stage/server/internal/db/store"
)

// Handoff statuses: sent or skipped.
const (
	HandoffSent    = store.HandoffSent
	HandoffSkipped = store.HandoffSkipped
)

type (
	Handoff       = store.Handoff
	HandoffSource = store.HandoffSource
)

// RecordHandoff stores attempt's handoff for one source (replacing an
// earlier record of the same attempt and source) and a launching task event
// describing it, in one transaction.
func (s *Store) RecordHandoff(ctx context.Context, taskID int64, h Handoff) error {
	detail := fmt.Sprintf("handoff from %s appended (%d chars)", sourceLabel(h), len([]rune(h.Text)))
	if h.Status == HandoffSkipped {
		reason := ""
		if h.Reason != nil {
			reason = *h.Reason
		}
		detail = fmt.Sprintf("handoff from %s skipped: %s", sourceLabel(h), reason)
	}
	return s.e.Write(ctx, func(tx store.Tx) error {
		var sourceID *int64
		if h.Session != nil {
			id, err := ensureSession(ctx, tx, *h.Session)
			if err != nil {
				return err
			}
			sourceID = &id
		}
		if err := tx.Attempts().RecordHandoff(ctx, taskID, h, sourceID); err != nil {
			return err
		}
		from := StateLaunching
		return tx.Tasks().AddEvent(ctx, taskID, &from, StateLaunching, detail)
	})
}

func sourceLabel(h Handoff) string {
	return HandoffSource{TaskID: h.SourceTaskID, Title: h.SourceTitle}.Label()
}

// GetHandoffs are attempt's handoff records, by source task id ([] when
// none).
func (s *Store) GetHandoffs(ctx context.Context, taskID int64, attempt int) ([]Handoff, error) {
	return read(ctx, s, func(tx store.Tx) ([]Handoff, error) { return tx.Attempts().Handoffs(ctx, taskID, attempt) })
}
