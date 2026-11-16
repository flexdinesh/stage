package db_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
)

func TestNotifyEvents(t *testing.T) {
	s, ctx := setup(t)
	p, err := s.GetPreferences(ctx)
	if err != nil {
		t.Fatal(err)
	}
	all := []db.TaskState{db.StateNeedsInput, db.StateFailed, db.EventReady, db.StateDone, db.StateSettled}
	if !slices.Equal(p.NotifyEvents, all) {
		t.Fatalf("default %v, want all", p.NotifyEvents)
	}

	// Deduplicated, stored in display order.
	p, err = s.SetNotifyEvents(ctx, []db.TaskState{db.EventReady, db.StateFailed, db.StateNeedsInput, db.StateFailed})
	if err != nil {
		t.Fatal(err)
	}
	if want := []db.TaskState{db.StateNeedsInput, db.StateFailed, db.EventReady}; !slices.Equal(p.NotifyEvents, want) {
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
	if _, err := s.SetNotifyEvents(ctx, []db.TaskState{db.StateRunning}); !errors.As(err, &inv) {
		t.Fatalf("running: %v, want InvalidError", err)
	}
	if p, _ = s.GetPreferences(ctx); len(p.NotifyEvents) != 0 {
		t.Fatalf("invalid set changed filter: %v", p.NotifyEvents)
	}
}
