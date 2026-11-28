package storetest

import (
	"errors"
	"slices"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
)

func init() {
	register("NotifyEvents", caseNotifyEvents)
}

func caseNotifyEvents(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p, err := s.GetPreferences(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The owner's filter, extended by migration 0053 (schedule_started opt-in).
	all := []db.NotificationEvent{db.EventNeedsInput, db.EventFailed, db.EventReady, db.EventCompleted, db.EventTreeCompleted, db.EventScheduleSkipped, db.EventDone}
	if !slices.Equal(p.NotifyEvents, all) {
		t.Fatalf("default %v, want %v", p.NotifyEvents, all)
	}

	// Deduplicated, stored in display order.
	p, err = s.SetNotifyEvents(ctx, []db.NotificationEvent{db.EventScheduleStarted, db.EventReady, db.EventFailed, db.EventNeedsInput, db.EventFailed})
	if err != nil {
		t.Fatal(err)
	}
	if want := []db.NotificationEvent{db.EventNeedsInput, db.EventFailed, db.EventReady, db.EventScheduleStarted}; !slices.Equal(p.NotifyEvents, want) {
		t.Fatalf("set %v, want %v", p.NotifyEvents, want)
	}

	// Empty = all off (not nil in JSON).
	p, err = s.SetNotifyEvents(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.NotifyEvents == nil || len(p.NotifyEvents) != 0 {
		t.Fatalf("cleared %#v", p.NotifyEvents)
	}

	var inv *db.InvalidError
	if _, err := s.SetNotifyEvents(ctx, []db.NotificationEvent{"running"}); !errors.As(err, &inv) {
		t.Fatalf("running: %v, want InvalidError", err)
	}
	if _, err := s.SetNotifyEvents(ctx, []db.NotificationEvent{"settled"}); !errors.As(err, &inv) || err.Error() != "event settled was removed in API v9; use completed (want needs_input, failed, ready, completed, tree_completed, schedule_started, schedule_skipped, done)" {
		t.Fatalf("removed event: %v", err)
	}
	if p, _ = s.GetPreferences(ctx); len(p.NotifyEvents) != 0 {
		t.Fatalf("invalid set changed filter: %v", p.NotifyEvents)
	}
}
