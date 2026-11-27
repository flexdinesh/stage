package agenttools_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/client"
)

func TestMCPRejectsRetiredDestinationSelectors(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("retired selector reached API: %s %s", r.Method, r.URL)
		http.NotFound(w, r)
	})
	cs := connect(t, &agenttools.Ops{Client: client.NewInProcess(handler, "https://stage.test")})

	for _, tc := range []struct {
		tool, selector string
		args           map[string]any
	}{
		{"list_models", "projectId", map[string]any{"connectionId": 7, "projectId": 1}},
		{"list_models", "bindingId", map[string]any{"connectionId": 7, "bindingId": 41}},
		{"create_task", "bindingId", map[string]any{"connectionId": 7, "project": "1", "title": "Task", "prompt": "Work", "bindingId": 41}},
		{"create_schedule", "bindingId", map[string]any{"connectionId": 7, "title": "Schedule", "prompt": "Work", "cron": "@daily", "tz": "UTC", "bindingId": 41}},
		{"update_task", "bindingId", map[string]any{"id": 13, "bindingId": nil}},
		{"update_schedule", "bindingId", map[string]any{"id": 13, "bindingId": nil}},
		{"list_actions", "bindingId", map[string]any{"projectId": 1, "onConnectionId": 7, "bindingId": 41}},
		{"run_action", "bindingId", map[string]any{"actionId": "inspect", "projectId": 1, "onConnectionId": 7, "bindingId": 41}},
	} {
		t.Run(tc.tool+"/"+tc.selector, func(t *testing.T) {
			if msg := call(t, cs, tc.tool, tc.args, nil); !strings.Contains(msg, "were retired") || !strings.Contains(msg, "select connectionId and workspaceId from list_projects") {
				t.Fatalf("expected retirement and repair guidance for %s, got %q", tc.selector, msg)
			}
		})
	}
}

func TestMCPLaunchRequiresExactDestination(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("incomplete launch destination reached API: %s %s", r.Method, r.URL)
		http.NotFound(w, r)
	})
	cs := connect(t, &agenttools.Ops{Client: client.NewInProcess(handler, "https://stage.test")})
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"create_task", map[string]any{"start": true}},
		{"create_task", map[string]any{"startLocal": "2027-01-01T09:00", "startTz": "UTC"}},
		{"create_schedule", map[string]any{"cron": "@daily", "tz": "UTC"}},
	} {
		tc.args["project"], tc.args["connectionId"], tc.args["title"], tc.args["prompt"] = "1", 7, "Work", "Do it"
		if msg := call(t, cs, tc.tool, tc.args, nil); !strings.Contains(msg, "select connectionId and workspaceId from list_projects") {
			t.Errorf("%s %v: expected destination repair guidance, got %q", tc.tool, tc.args, msg)
		}
	}
}
