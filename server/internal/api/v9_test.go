package api_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
)

func TestRemovedV9InputsNameTheReplacement(t *testing.T) {
	e := newEnv(t)
	const completedFrom = "settledFrom was removed in API v9; use completedFrom"
	const group = "group key thread:... was renamed in API v9; use session:<connectionId>:<externalSessionId>"
	for _, tc := range []struct {
		name, method, path string
		body               any
		want               string
	}{
		{"task filter", "GET", "/api/tasks?settledFrom=2000-01-01", nil, completedFrom},
		{"tree filter", "GET", "/api/trees?settledFrom=", nil, completedFrom},
		{"task state", "GET", "/api/tasks?state=done,settled", nil,
			"state settled was removed in API v9; use state done (completedAt marks acceptance)"},
		{"notification preference", "PUT", "/api/preferences/notifications", map[string]any{"events": []string{"done", "settled"}},
			"event settled was removed in API v9; use completed (want needs_input, failed, ready, completed, tree_completed, schedule_started, schedule_skipped, done)"},
		{"notification history", "GET", "/api/notifications?group=thread%3A1%3Aext-1", nil, group},
		{"notification bulk read", "POST", "/api/notifications/read", map[string]any{"groups": []string{"thread:1:ext-1"}}, group},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out errOut
			rec := raw(t, e.h, tc.method, tc.path, nil, tc.body, &out)
			if rec.Code != 400 || out.Error != tc.want {
				t.Fatalf("status %d, error %q, want 400 %q", rec.Code, out.Error, tc.want)
			}
		})
	}
}

func TestTaskJSONHasNoLegacyFields(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/legacy-task-json")
	task := e.task(p.ID, "Started", map[string]any{"start": true})
	var detail map[string]json.RawMessage
	e.must("GET", taskPath(task.ID), nil, 200, &detail)
	for _, field := range []string{"artifactLinks", "settledAt", "sourceThreadId"} {
		if _, exists := detail[field]; exists {
			t.Fatalf("task still has %s", field)
		}
	}
	var links map[string]any
	if err := json.Unmarshal(detail["links"], &links); err != nil || links["native"] == nil || links["web"] == nil {
		t.Fatalf("task links %+v, err %v", links, err)
	}
	for _, field := range []string{"app", "service"} {
		if _, exists := links[field]; exists {
			t.Fatalf("task links still have %s", field)
		}
	}
}

func TestPushedObservationRemovedFields(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/push-v9")
	tk := e.task(p.ID, "Pushed", map[string]any{"start": true})
	session := *tk.ExternalSessionID
	at := time.Now().UTC().Add(time.Minute)
	path := "/api/connections/1/observations"
	valid := pushed(1, session, p.T3ProjectID, "done", at)

	for _, field := range []string{"artifacts", "modelLabel", "links.service", "links.app", "reply.messageId", "reply.position"} {
		t.Run(field, func(t *testing.T) {
			legacy := pushed(1, session, p.T3ProjectID, "done", at)
			if parent, key, nested := strings.Cut(field, "."); nested {
				legacy[parent] = map[string]any{key: nil}
			} else {
				legacy[field] = nil
			}
			var out errOut
			rec := raw(t, e.h, "POST", path, nil, pushBody(valid, legacy), &out)
			if want := "observations[1]." + field + " was removed in API v9"; rec.Code != 400 || out.Error != want {
				t.Fatalf("status %d, error %q, want 400 %q", rec.Code, out.Error, want)
			}
		})
	}
	if got := e.getTask(tk.ID); got.State != "running" {
		t.Fatalf("rejected batch changed task state: %s", got.State)
	}

	valid["links"] = map[string]any{"web": "https://stage.test/session", "native": "t3code://sessions/v9", "command": "resume v9"}
	valid["reply"] = map[string]any{"itemId": "reply-v9", "runId": "run-v9", "text": "Still here", "truncated": false, "updatedAt": at}
	var out observationResults
	e.must("POST", path, pushBody(valid), 200, &out)
	if len(out.Results) != 1 || out.Results[0].Status != "ingested" {
		t.Fatalf("results %+v", out)
	}
	got, err := e.store.Observation(t.Context(), db.SessionRef{ConnectionID: 1, ExternalSessionID: session})
	if err != nil || got.State != "done" || got.Links.Web != "https://stage.test/session" || got.Links.Native != "t3code://sessions/v9" || got.Links.Command != "resume v9" ||
		got.Reply == nil || got.Reply.ItemID != "reply-v9" || got.Reply.RunID != "run-v9" || got.Reply.Text != "Still here" {
		t.Fatalf("stored observation %+v, err %v", got, err)
	}
}

func TestPushObservationsKeepsStrictBoundedDecoding(t *testing.T) {
	e := newEnv(t)
	path := "/api/connections/1/observations"
	for _, tc := range []struct {
		name, body, error string
	}{
		{"malformed", `{"observations":[`, "invalid JSON body: unexpected EOF"},
		{"unknown nested field", `{"observations":[{"links":{"unknown":true}}]}`, `invalid JSON body: json: unknown field "unknown"`},
		{"trailing object", `{"observations":[]} {}`, "body must contain one JSON object"},
		{"body limit", `{"observations":[]}` + strings.Repeat(" ", 1<<20), "body exceeds 1048576 bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			e.h.ServeHTTP(rec, httptest.NewRequest("POST", path, strings.NewReader(tc.body)))
			var out errOut
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != 400 || out.Error != tc.error {
				t.Fatalf("status %d, body %q, want 400 with %q", rec.Code, rec.Body, tc.error)
			}
		})
	}
}
