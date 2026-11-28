package opencode

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/opencode/opencodetest"
)

func queueEntry(id, kind, delivery, text string) map[string]any {
	return map[string]any{
		"id": id, "type": kind, "sessionID": liveSession, "delivery": delivery,
		"time": map[string]int64{"created": 12345}, "payload": map[string]any{"text": text},
	}
}

func TestQueueManagement(t *testing.T) {
	fake := opencodetest.New(t, "")
	compaction := queueEntry("msg_compaction", "compaction", "queue", "")
	compaction["payload"] = 1
	foreign := queueEntry("msg_foreign", "user", "queue", "other session")
	foreign["sessionID"] = "ses_other"
	fake.SetSession(liveSession, opencodetest.Session{Running: true, Inbox: []map[string]any{
		compaction, queueEntry("msg_synthetic", "synthetic", "queue", "internal"), foreign,
		queueEntry("msg_native", "user", "steer", "sent from OpenCode"),
	}})
	a, err := New(liveRef.ConnectionID, Settings{Endpoint: fake.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	features, err := a.Features(ctx)
	if err != nil || !features.Operations[execution.OpQueueManage].Available {
		t.Fatalf("features %+v: %v", features.Operations, err)
	}
	message := execution.Message{Text: "sent from Stage", RequestID: "queue-test", Delivery: execution.DeliveryQueue}
	if err := a.Send(ctx, liveRef, message); err != nil {
		t.Fatal(err)
	}
	id, _ := messageID(liveSession, message.RequestID)
	inbox, err := a.Queued(ctx, liveRef)
	native := execution.QueuedMessage{ID: "msg_native", Text: "sent from OpenCode", Delivery: execution.DeliverySteer,
		CreatedAt: time.UnixMilli(12345).UTC(), Source: "harness"}
	if err != nil || len(inbox) != 2 || inbox[0] != native {
		t.Fatalf("inbox %+v: %v", inbox, err)
	}
	if queued := inbox[1]; queued.ID != id || queued.Text != message.Text || queued.Delivery != execution.DeliveryQueue ||
		queued.Source != "stage" || queued.CreatedAt.UnixMilli() <= 0 {
		t.Fatalf("Stage admission %+v", queued)
	}
	if err := a.PromoteQueued(ctx, liveRef, id); err != nil {
		t.Fatal(err)
	}
	promoted, err := a.Queued(ctx, liveRef)
	inbox[1].Delivery = execution.DeliverySteer
	if err != nil || !slices.Equal(promoted, inbox) {
		t.Fatalf("promoted inbox %+v: %v", promoted, err)
	}
	if err := a.CancelQueued(ctx, liveRef, id); err != nil {
		t.Fatal(err)
	}
	pending, err := a.Queued(ctx, liveRef)
	if err != nil || !slices.Equal(pending, []execution.QueuedMessage{native}) {
		t.Fatalf("remaining inbox %+v: %v", pending, err)
	}
	for _, id := range []string{id, "msg_foreign", "msg_compaction"} {
		if err := a.CancelQueued(ctx, liveRef, id); !errors.Is(err, execution.ErrNotFound) {
			t.Fatalf("cancel %s: %v", id, err)
		}
		if err := a.PromoteQueued(ctx, liveRef, id); !errors.Is(err, execution.ErrNotFound) {
			t.Fatalf("promote %s: %v", id, err)
		}
	}
	gone := execution.SessionRef{ConnectionID: liveRef.ConnectionID, ExternalSessionID: "ses_gone"}
	if _, err := a.Queued(ctx, gone); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("missing session: %v", err)
	}
	foreignRef := execution.SessionRef{ConnectionID: liveRef.ConnectionID + 1, ExternalSessionID: liveSession}
	if err := a.PromoteQueued(ctx, foreignRef, native.ID); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("foreign connection: %v", err)
	}
}

func TestQueueManagementDegradesWithoutBreakingLegacyInboxRecovery(t *testing.T) {
	fake := opencodetest.New(t, "")
	fake.WithoutQueueManagement()
	id, _ := messageID(liveSession, "legacy-retry")
	item := queueEntry(id, "user", "", "legacy")
	delete(item, "delivery")
	item["payload"] = map[string]any{"text": "legacy", "metadata": map[string]string{"stageMessage": "legacy-retry"}}
	fake.SetSession(liveSession, opencodetest.Session{Running: true, Inbox: []map[string]any{item}})
	a, err := New(liveRef.ConnectionID, Settings{Endpoint: fake.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	c, err := a.Features(ctx)
	if err != nil || c.Operations[execution.OpQueueManage] != (execution.Availability{Reason: queueManageUnavailable}) ||
		!c.Operations[execution.OpQueue].Available {
		t.Fatalf("legacy capabilities %+v: %v", c.Operations, err)
	}
	if _, err := a.Queued(ctx, liveRef); err == nil || err.Error() != queueManageUnavailable {
		t.Fatalf("legacy queue management: %v", err)
	}
	// The running session blocks a fresh send, so the legacy inbox is the
	// only possible receipt for this retry.
	if err := a.Send(ctx, liveRef, execution.Message{Text: "legacy", RequestID: "legacy-retry"}); err != nil {
		t.Fatalf("retry against legacy inbox: %v", err)
	}
	if calls := fake.Interactions(); len(calls) != 0 {
		t.Fatalf("retry posted a second prompt: %+v", calls)
	}
}

func TestPromoteQueuedDistinguishesConflictsFromConsumedMessages(t *testing.T) {
	for _, tc := range []struct {
		name     string
		consumed bool
	}{
		{"consumed during promote", true},
		{"still pending after conflict", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := opencodetest.New(t, "")
			fake.SetSession(liveSession, opencodetest.Session{Inbox: []map[string]any{
				queueEntry("msg_race", "user", "queue", "pending"),
			}})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPatch && r.URL.Path == inboxPath(liveSession)+"/msg_race" {
					if tc.consumed {
						fake.SetSession(liveSession, opencodetest.Session{})
					} else {
						http.Error(w, `{"_tag":"ConflictError"}`, http.StatusConflict)
						return
					}
				}
				fake.Config.Handler.ServeHTTP(w, r)
			}))
			t.Cleanup(srv.Close)
			a, err := New(liveRef.ConnectionID, Settings{Endpoint: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			err = a.PromoteQueued(t.Context(), liveRef, "msg_race")
			if tc.consumed {
				if !errors.Is(err, execution.ErrNotFound) {
					t.Fatalf("consumed message: %v", err)
				}
			} else {
				var status responseError
				if !errors.As(err, &status) || status.code != http.StatusConflict {
					t.Fatalf("live message conflict: %v", err)
				}
			}
		})
	}
}

func TestLiveOpenCodeQueueManagement(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	in := f.intent("fixture-interrupt", "root")
	var cp execution.Checkpoint
	save := func(_ context.Context, v execution.Checkpoint) error { cp = v; return nil }
	obs, err := f.a.Launch(ctx, in, cp, save)
	if err != nil {
		obs, err = f.a.Launch(ctx, in, cp, save)
	}
	if err != nil {
		t.Fatal(err)
	}
	ref := obs.Session
	observeUntil(t, f.a, ref, "running")
	for _, text := range []string{"to cancel", "to steer"} {
		if err := f.a.Send(ctx, ref, execution.Message{Text: text, RequestID: text, Delivery: execution.DeliveryQueue}); err != nil {
			t.Fatal(err)
		}
	}
	inbox, err := f.a.Queued(ctx, ref)
	if err != nil || len(inbox) != 2 || inbox[0].Text != "to cancel" || inbox[1].Text != "to steer" ||
		inbox[0].Source != "stage" || inbox[1].Delivery != execution.DeliveryQueue {
		t.Fatalf("native inbox %+v: %v", inbox, err)
	}
	if err := f.a.CancelQueued(ctx, ref, inbox[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := f.a.CancelQueued(ctx, ref, inbox[0].ID); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("cancelled item: %v", err)
	}
	if err := f.a.PromoteQueued(ctx, ref, inbox[1].ID); err != nil {
		t.Fatal(err)
	}
	promoted, err := f.a.Queued(ctx, ref)
	inbox[1].Delivery = execution.DeliverySteer
	if err != nil || !slices.Equal(promoted, inbox[1:]) {
		t.Fatalf("native promoted inbox %+v: %v", promoted, err)
	}
}
