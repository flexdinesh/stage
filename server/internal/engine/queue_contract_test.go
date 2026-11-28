package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/execution"
)

type hostedQueue struct {
	*hostedHarness
	off      bool
	messages []execution.QueuedMessage
	steered  []string
	ref      execution.SessionRef
}

func (f *hostedQueue) Features(ctx context.Context) (execution.Capabilities, error) {
	caps, err := f.hostedHarness.Features(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	caps.Operations[execution.OpQueueManage] = execution.Availability{Available: !f.off, Reason: "Manage queued messages in Hosted."}
	return caps, err
}

func (f *hostedQueue) Queued(_ context.Context, ref execution.SessionRef) ([]execution.QueuedMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ref = ref
	return slices.Clone(f.messages), nil
}

func (f *hostedQueue) CancelQueued(_ context.Context, ref execution.SessionRef, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.remove(ref, id)
}

func (f *hostedQueue) PromoteQueued(_ context.Context, ref execution.SessionRef, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.remove(ref, id); err != nil {
		return err
	}
	f.steered = append(f.steered, id)
	return nil
}

func (f *hostedQueue) remove(ref execution.SessionRef, id string) error {
	f.ref = ref
	i := slices.IndexFunc(f.messages, func(m execution.QueuedMessage) bool { return m.ID == id })
	if i < 0 {
		return execution.ErrNotFound
	}
	f.messages = slices.Delete(f.messages, i, i+1)
	return nil
}

func TestContractQueueManagement(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		now := time.Date(2026, 11, 29, 4, 30, 0, 0, time.UTC)
		const cancelID, steerID = "cancel/1", "promote-2"
		f := &hostedQueue{
			hostedHarness: &hostedHarness{fakeBase: fakeBase{workspace: "volume://v2"}, surface: true},
			messages: []execution.QueuedMessage{
				{ID: cancelID, Text: strings.Repeat("界", 2001), Delivery: execution.DeliveryQueue, CreatedAt: now, Source: "stage"},
				{ID: steerID, Text: "private steer", Delivery: execution.DeliveryQueue, CreatedAt: now, Source: "harness"},
			},
		}
		store, svc, task := v2Setup(t, p, "hosted", f)
		running, err := svc.Coordinator.StartTask(t.Context(), task.ID)
		if err != nil || running.Session == nil {
			t.Fatal(running, err)
		}
		session := *running.Session
		foreign := execution.SessionRef{ConnectionID: session.ConnectionID, ExternalSessionID: "another-session"}
		router := api.NewRouter(api.Deps{Store: store, Engine: svc, Harnesses: v2Harnesses})
		request := func(method, path string, caller execution.SessionRef, status int, contains string) *httptest.ResponseRecorder {
			t.Helper()
			req := httptest.NewRequest(method, fmt.Sprintf("/api/tasks/%d/queue%s", task.ID, path), nil)
			req.Header.Set(agentctx.CallerHeader, fmt.Sprintf("%d:%s", caller.ConnectionID, caller.ExternalSessionID))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != status || !strings.Contains(rec.Body.String(), contains) {
				t.Fatalf("%s %s: HTTP %d %s", method, path, rec.Code, rec.Body)
			}
			return rec
		}
		rec := request(http.MethodGet, "", session, 200, "private steer")
		var queue client.SessionQueue
		if err := json.Unmarshal(rec.Body.Bytes(), &queue); err != nil {
			t.Fatal(err)
		}
		if len(queue.Messages) != 2 || queue.Messages[0] != (execution.QueuedMessage{
			ID: cancelID, Text: strings.Repeat("界", 2000), Delivery: execution.DeliveryQueue, CreatedAt: now, Source: "stage",
		}) || queue.Messages[1].ID != steerID || queue.Messages[1].Source != "harness" {
			t.Fatalf("queue metadata, order or preview did not cross the boundary")
		}
		del, promote := "/"+url.PathEscape(cancelID), "/"+steerID+"/steer"
		request(http.MethodDelete, del, session, 403, "self_action_forbidden")
		request(http.MethodPost, promote, session, 403, "self_action_forbidden")
		request(http.MethodDelete, del, foreign, 204, "")
		request(http.MethodDelete, del, foreign, 404, "queued message is no longer pending")
		request(http.MethodPost, promote, foreign, 204, "")
		request(http.MethodPost, promote, foreign, 404, "queued message is no longer pending")
		request(http.MethodGet, "", session, 200, `"messages":[]`)

		f.mu.Lock()
		steered, ref := slices.Clone(f.steered), f.ref
		f.off = true
		f.mu.Unlock()
		if !slices.Equal(steered, []string{steerID}) || ref != session {
			t.Fatalf("steered %q in %v", steered, ref)
		}
		request(http.MethodGet, "", foreign, 409, "Manage queued messages in Hosted.")
		request(http.MethodDelete, del, foreign, 409, "operation_unavailable")
		request(http.MethodPost, promote, foreign, 409, "operation_unavailable")

		events, err := store.TaskEvents(t.Context(), task.ID)
		if err != nil {
			t.Fatal(err)
		}
		var details []string
		for _, event := range events {
			if event.Cause != nil && event.Cause.Kind == "session_queue" {
				if event.Cause.Session == nil || *event.Cause.Session != foreign || event.Cause.TaskID != task.ID {
					t.Fatalf("queue event attribution: %+v", event)
				}
				details = append(details, event.Detail)
			}
		}
		if !slices.Equal(details, []string{"Queued message cancelled from Stage", "Queued message steered from Stage"}) {
			t.Fatalf("queue events %q", details)
		}
	})
}
