package engine_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/coordinator"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/engine"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/remote"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

func TestContractSessionSurface(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		now := time.Date(2026, 11, 29, 4, 30, 0, 0, time.UTC)
		activity := execution.Activity{
			At: now, Model: "Fixture model",
			Plan:    []execution.PlanStep{{Text: "Verify", Status: "in_progress"}},
			Files:   []execution.FileChange{{Path: "work.go", Additions: 2, Deletions: 1, Status: "modified"}},
			Details: json.RawMessage(fmt.Sprintf(`{"data":%q}`, strings.Repeat("d", 16<<10))),
		}
		for i := range 103 {
			activity.Items = append(activity.Items, execution.ActivityItem{
				ID: fmt.Sprint(i), At: now.Add(time.Duration(i) * time.Second), Kind: "command", Status: "done",
				Title: strings.Repeat("界", 201), Text: strings.Repeat("λ", 2001), Preview: "go test",
			})
		}
		f := &hostedHarness{fakeBase: fakeBase{workspace: "volume://v2"}, surface: true, activity: activity}
		store, svc, task := v2Setup(t, p, "hosted", f)
		ctx := t.Context()
		running, err := svc.Coordinator.StartTask(ctx, task.ID)
		if err != nil || running.Session == nil {
			t.Fatal(running, err)
		}
		session := *running.Session
		router := api.NewRouter(api.Deps{Store: store, Engine: svc, Harnesses: v2Harnesses})
		request := func(method, subpath, body string, caller *execution.SessionRef) *httptest.ResponseRecorder {
			t.Helper()
			req := httptest.NewRequest(method, fmt.Sprintf("/api/tasks/%d/%s", task.ID, subpath), strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			if caller != nil {
				req.Header.Set(agentctx.CallerHeader, fmt.Sprintf("%d:%s", caller.ConnectionID, caller.ExternalSessionID))
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			return rec
		}
		check := func(rec *httptest.ResponseRecorder, status int, contains string) {
			t.Helper()
			if rec.Code != status || !strings.Contains(rec.Body.String(), contains) {
				t.Fatalf("HTTP %d %s", rec.Code, rec.Body)
			}
		}

		check(request(http.MethodPost, "messages", `{"text":"private","delivery":"auto"}`, nil), 400, "unknown message delivery")
		check(request(http.MethodPost, "messages", `{"text":"private"}`, nil), 409, "session_busy")
		foreign := execution.SessionRef{ConnectionID: session.ConnectionID, ExternalSessionID: "another-session"}
		for _, mode := range []execution.Delivery{execution.DeliverySteer, execution.DeliveryQueue} {
			body := fmt.Sprintf(`{"text":"private","delivery":%q}`, mode)
			check(request(http.MethodPost, "messages", body, &session), 403, "self_action_forbidden")
			check(request(http.MethodPost, "messages", body, &foreign), 200, "")
		}
		f.mu.Lock()
		f.state = "done"
		f.mu.Unlock()
		check(request(http.MethodPost, "messages", `{"text":"private","delivery":"queue"}`, nil), 409, "The turn ended")
		check(request(http.MethodPost, "messages", `{"text":"private","delivery":"send"}`, &foreign), 200, "")

		events, err := store.TaskEvents(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		var details []string
		for _, ev := range events {
			if ev.Cause != nil && ev.Cause.Kind == "session_message" {
				if ev.Cause.Session == nil || *ev.Cause.Session != foreign || ev.Cause.TaskID != task.ID || ev.Actor.Kind != "personal" {
					t.Fatalf("delivery attribution %+v", ev)
				}
				details = append(details, ev.Detail)
			}
		}
		wantDetails := []string{coordinator.SteerSent, coordinator.MessageQueued, coordinator.FollowUpSent}
		if !slices.Equal(details, wantDetails) {
			t.Fatalf("delivery events %q, want %q", details, wantDetails)
		}
		f.mu.Lock()
		messages := slices.Clone(f.messages)
		f.mu.Unlock()
		if len(messages) != 3 {
			t.Fatalf("delivered %+v", messages)
		}
		for i, mode := range []execution.Delivery{execution.DeliverySteer, execution.DeliveryQueue, execution.DeliverySend} {
			if m := messages[i]; m.Delivery != mode || m.Text != "private" || m.RequestID == "" {
				t.Fatalf("message %d %+v", i, m)
			}
		}

		for _, limit := range []struct {
			query, first string
			want         int
		}{{"", "73", 30}, {"?limit=200", "3", 100}, {"?limit=1", "102", 1}} {
			rec := request(http.MethodGet, "activity"+limit.query, "", &session)
			check(rec, 200, `"truncated":true`)
			var out client.SessionActivity
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			got := out.Activity
			if len(got.Items) != limit.want || got.Items[0].ID != limit.first || got.Items[len(got.Items)-1].ID != "102" || len(got.Details) != 0 ||
				!slices.Equal(got.Plan, activity.Plan) || !slices.Equal(got.Files, activity.Files) || got.Model != activity.Model || !got.At.Equal(now) {
				t.Fatalf("activity limit %s: %d items, plan %+v, files %+v, model %q, at %v", limit.query, len(got.Items), got.Plan, got.Files, got.Model, got.At)
			}
			if item := got.Items[0]; utf8.RuneCountInString(item.Title) != 200 || utf8.RuneCountInString(item.Text) != 2000 || item.Preview != "go test" {
				t.Fatal("activity item was not bounded on rune boundaries")
			}
			f.mu.Lock()
			actualLimit, ref := f.activityQ.Limit, f.activityRef
			f.mu.Unlock()
			if actualLimit != limit.want || ref != session {
				t.Fatalf("activity called with limit %d and session %+v", actualLimit, ref)
			}
		}
		check(request(http.MethodGet, "activity?limit=-1", "", nil), 400, "nonnegative integer")
		for _, invalid := range []execution.Activity{
			{Items: []execution.ActivityItem{{Kind: "bogus"}}},
			{Items: []execution.ActivityItem{{Kind: "event", Status: "bogus"}}},
			{Plan: []execution.PlanStep{{Status: "bogus"}}},
			{Files: []execution.FileChange{{Status: "bogus"}}},
		} {
			f.mu.Lock()
			f.activity = invalid
			f.mu.Unlock()
			check(request(http.MethodGet, "activity", "", nil), 502, "bogus")
		}
		f.mu.Lock()
		f.surface = false
		f.mu.Unlock()
		check(request(http.MethodGet, "activity", "", nil), 409, "Recent activity is in Hosted.")
		check(request(http.MethodPost, "messages", `{"text":"private","delivery":"steer"}`, nil), 409, "Steer in Hosted.")
	})
}

func TestContractSessionSurfaceWithOlderWorker(t *testing.T) {
	ctx := t.Context()
	f := &hostedHarness{fakeBase: fakeBase{workspace: "volume://v2"}, surface: true, state: "done"}
	store, svc, task := v2Setup(t, placements[1], "hosted", f)
	if _, err := svc.Coordinator.StartTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	rig := newContractWorker(t, store, map[int64]execution.Adapter{3: f})
	rig.StopWorker()
	rig.SetMissingRequestKinds(remote.KindDeliver, remote.KindActivity)
	rig.StartWorker()
	older := engine.NewForMachines(store, v2Harnesses, rig.Set(), nil)
	if _, ok := execution.As[execution.ActivityReader](rig.Adapter(3)); ok {
		t.Fatal("activity trait offered without its worker handler")
	}
	// Test both an older worker's omitted declarations and a worker that
	// reports availability but lacks the matching channel kinds.
	features, err := f.Features(ctx)
	if err != nil || features.DeclaresOperations() != nil {
		t.Fatal(err, features.DeclaresOperations())
	}
	checkUnavailable := func(op execution.Op, err error) {
		t.Helper()
		var conflict *db.ConflictError
		if !errors.As(err, &conflict) || conflict.Code != "operation_unavailable" || !strings.Contains(conflict.Msg, "Update Stage on the worker machine to use "+string(op)) {
			t.Fatalf("older worker %s: %v", op, err)
		}
	}
	for _, mode := range []execution.Delivery{execution.DeliverySteer, execution.DeliveryQueue} {
		op, err := mode.Operation()
		if err != nil {
			t.Fatal(err)
		}
		_, err = older.Coordinator.Send(ctx, task.ID, execution.Message{Text: "message", Delivery: mode}, nil)
		checkUnavailable(op, err)
		delete(features.Operations, op)
	}
	delete(features.Operations, execution.OpActivity)
	body, err := json.Marshal(features)
	if err != nil {
		t.Fatal(err)
	}
	rig.ReplaceResult(remote.KindFeatures, body)
	_, err = older.Coordinator.Activity(ctx, task.ID, execution.ActivityQuery{})
	checkUnavailable(execution.OpActivity, err)
	if _, err := older.Coordinator.Send(ctx, task.ID, execution.Message{Text: "legacy send"}, nil); err != nil {
		t.Fatalf("legacy send unavailable: %v", err)
	}

	local := machine.NewLocal(2, harness.Profile{}, nil)
	local.Install(3, 3, f)
	handlers := remote.NewHost(local).Handlers()
	body, err = json.Marshal(remote.SendRequest{Message: execution.Message{Text: "must never send", Delivery: execution.DeliverySteer}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = handlers[remote.KindSend](ctx, workerchan.Envelope{Kind: remote.KindSend, ConnectionID: 3, Body: body}, nil)
	var invalid *workerchan.RemoteError
	if !errors.As(err, &invalid) || invalid.Code != remote.CodeInvalidAnswer {
		t.Fatalf("legacy send channel accepted steer: %v", err)
	}
}
