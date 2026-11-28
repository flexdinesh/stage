package t3

import (
	"errors"
	"slices"
	"testing"

	"github.com/flexdinesh/stage/server/internal/execution"
)

func TestSessionActionsSettleAndArchiveThroughT3(t *testing.T) {
	ctx := t.Context()
	fake := NewFake()
	adapter := NewAdapter(fake)
	caps, err := adapter.Features(ctx)
	if err != nil || caps.DeclaresOperations() != nil {
		t.Fatal(err, caps.DeclaresOperations())
	}
	if err := execution.ValidateActions(caps.Actions, ""); err != nil {
		t.Fatal(err)
	}
	caps.Actions = slices.DeleteFunc(caps.Actions, func(a execution.Action) bool { return a.Target != execution.TargetSession })
	ids := []string{}
	for _, a := range caps.Actions {
		ids = append(ids, a.ID+":"+a.Label)
		if a.Destructive || !a.Allows(execution.CallerAgent) || a.Confirm == "" || !a.Availability.Available {
			t.Fatalf("declaration %+v", a)
		}
	}
	if !slices.Equal(ids, []string{"settle:Settle in T3", "archive:Archive in T3"}) {
		t.Fatalf("declared %v", ids)
	}

	launched, err := fake.LaunchThread(ctx, LaunchInput{ProjectID: "p", Title: "work"})
	if err != nil {
		t.Fatal(err)
	}
	ref := execution.SessionRef{ConnectionID: 1, ExternalSessionID: launched.ThreadID}
	act := func(id string) (execution.ActionResult, error) {
		return adapter.Act(ctx, execution.ActionRequest{ID: id, Target: execution.TargetSession, Session: &ref, Input: []byte("{}")})
	}
	states := func() map[string]string {
		o, err := adapter.Observe(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, a := range execution.ApplyActionStates(caps.Actions, o.Actions) {
			out[a.ID] = a.Availability.Reason
		}
		return out
	}

	// Running: T3 refuses settle, so the state rules it out first.
	if got := states(); got["settle"] != "Still running in T3. Stop it before settling." || got["archive"] != "" {
		t.Fatalf("running states %v", got)
	}
	var busy *execution.Busy
	if _, err := act(ActionSettle); !errors.As(err, &busy) || busy.Reason == "" {
		t.Fatalf("settle while running = %v", err)
	}

	if err := fake.SetStatus(launched.ThreadID, StatusCompleted); err != nil {
		t.Fatal(err)
	}
	if got := states(); got["settle"] != "" || got["archive"] != "" {
		t.Fatalf("finished states %v", got)
	}
	if res, err := act(ActionSettle); err != nil || res.Message != "Settled in T3." {
		t.Fatal(res, err)
	}
	o, err := adapter.Observe(ctx, ref)
	if err != nil || o.Closed == nil || o.Closed.Signal != execution.ClosedSettled {
		t.Fatalf("settle not observed: %+v %v", o.Closed, err)
	}
	// A settled thread can still be archived in T3.
	if got := states(); got["settle"] != "Already settled in T3." || got["archive"] != "" {
		t.Fatalf("settled states %v", got)
	}
	if res, err := act(ActionArchive); err != nil || res.Message != "Archived in T3." {
		t.Fatal(res, err)
	}
	if got := states(); got["settle"] != "Archived in T3. Unarchive it in T3 to settle." || got["archive"] != "Already archived in T3." {
		t.Fatalf("archived states %v", got)
	}
	if _, err := act(ActionArchive); !errors.As(err, &busy) {
		t.Fatalf("second archive = %v", err)
	}
	if want := []string{launched.ThreadID + ":settle", launched.ThreadID + ":archive"}; !slices.Equal(fake.Organized, want) {
		t.Fatalf("organized %v, want %v", fake.Organized, want)
	}

	var invalid *execution.Invalid
	if _, err := act("pin"); !errors.As(err, &invalid) {
		t.Fatalf("undeclared action = %v", err)
	}
	other := execution.SessionRef{ConnectionID: 2, ExternalSessionID: launched.ThreadID}
	if _, err := adapter.Act(ctx, execution.ActionRequest{ID: ActionArchive, Target: execution.TargetSession, Session: &other}); !errors.As(err, &invalid) {
		t.Fatalf("foreign session = %v", err)
	}
	gone := execution.SessionRef{ConnectionID: 1, ExternalSessionID: "missing"}
	if _, err := adapter.Act(ctx, execution.ActionRequest{ID: ActionArchive, Target: execution.TargetSession, Session: &gone}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("missing thread = %v", err)
	}
}

func TestSettleStateWhileWaitingForAnAnswer(t *testing.T) {
	d := ThreadDetail{Thread: Thread{ThreadID: "thread", ProjectID: "project", Status: StatusWaiting}, PendingRequestCount: 1}
	o := d.Observation()
	if len(o.Actions) != 1 || o.Actions[0].ID != ActionSettle || o.Actions[0].Reason != "Waiting for an answer in T3. Answer it before settling." {
		t.Fatalf("states %+v", o.Actions)
	}
}
