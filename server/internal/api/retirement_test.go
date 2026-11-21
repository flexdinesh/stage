package api_test

import (
	"fmt"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/t3"
)

func TestRetiredEndpointsAreNotAvailable(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{"/api/threads", "/api/connections/1/sessions/search?q=work", "/api/stats", "/api/connections/1/sessions/x/transcript", "/api/connections/1/sessions/x/transcript/stream", "/api/connections/1/sessions/x/transcript/opening", "/api/connections/1/sessions/x/transcript/items/y", "/api/connections/1/sessions/x/questions"} {
		e.must("GET", path, nil, 404, nil)
	}
	e.must("POST", "/api/connections/1/sessions/x/questions/y/answer", map[string]any{}, 404, nil)
	p := e.project("/w/retirement")
	task := e.task(p.ID, "Retained task", map[string]any{"start": true})
	e.must("POST", fmt.Sprintf("/api/tasks/%d/followup", task.ID), map[string]any{"message": "retired"}, 404, nil)
	e.must("GET", fmt.Sprintf("/api/tasks/%d/opening-prompt", task.ID), nil, 400, nil)
	var metadata map[string]any
	e.must("GET", "/api/connections/1/sessions/"+*task.T3ThreadID, nil, 200, &metadata)
	for _, field := range []string{"children", "createdTasks"} {
		if _, exists := metadata[field]; exists {
			t.Fatalf("retired field %s", field)
		}
	}
	var detail map[string]any
	e.must("GET", fmt.Sprintf("/api/tasks/%d", task.ID), nil, 200, &detail)
	for _, field := range []string{"pullRequests", "pendingQuestionCount"} {
		if _, exists := detail[field]; exists {
			t.Fatalf("retired field %s", field)
		}
	}
	if detail["artifactLinks"] == nil {
		t.Fatal("missing historical artifact links")
	}
	e.must("POST", fmt.Sprintf("/api/tasks/%d/stop", task.ID), nil, 200, nil)
}

func TestSilentWaitingSessionsDoNotCreateNotifications(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/silent")
	id := runningSession(t, e, p.T3ProjectID, "Waiting before startup")
	if err := e.store.SetObservedState(t.Context(), db.SessionRef{ConnectionID: e.store.LegacyT3ID(), ExternalSessionID: id}, db.StateNeedsInput); err != nil {
		t.Fatal(err)
	}
	var inbox struct {
		Waiting []struct {
			ID    string `json:"externalSessionId"`
			Links links  `json:"links"`
		} `json:"waitingSessions"`
		Groups []db.NotificationGroup `json:"groups"`
		Unread int                    `json:"unread"`
		Latest int64                  `json:"latestId"`
	}
	read := func() { t.Helper(); e.must("GET", "/api/notifications/groups", nil, 200, &inbox) }
	read()
	if len(inbox.Waiting) != 1 || inbox.Waiting[0].ID != id || inbox.Waiting[0].Links.Native != t3.AppThreadURL(t3.FakeEnvironmentID, id) || inbox.Waiting[0].Links.Web == "" || inbox.Unread != 0 || inbox.Latest != 0 || len(inbox.Groups) != 0 {
		t.Fatalf("silent inbox %+v", inbox)
	}
	if err := e.store.SetObservedState(t.Context(), db.SessionRef{ConnectionID: e.store.LegacyT3ID(), ExternalSessionID: id}, db.StateRunning); err != nil {
		t.Fatal(err)
	}
	read()
	if len(inbox.Waiting) != 0 {
		t.Fatalf("resolved session still waiting %+v", inbox)
	}
	if err := e.store.SetObservedState(t.Context(), db.SessionRef{ConnectionID: e.store.LegacyT3ID(), ExternalSessionID: id}, db.StateNeedsInput); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.InsertNotification(t.Context(), db.NewNotification{Event: db.StateNeedsInput, T3ThreadID: &id, Title: "Waiting", URL: "/?inbox"}); err != nil {
		t.Fatal(err)
	}
	read()
	if len(inbox.Waiting) != 0 || len(inbox.Groups) != 1 || inbox.Unread != 1 {
		t.Fatalf("duplicate waiting session %+v", inbox)
	}
}
