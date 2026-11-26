package api_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// API v8 removed the legacy T3 model inputs: each is a 400 that names the
// replacement, on task and schedule create and update.
func TestRemovedModelFieldsNameTheReplacement(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/v8")
	tk := e.task(p.ID, "A", nil)
	var sc schedule
	e.must("POST", "/api/schedules", map[string]any{"projectId": p.ID, "title": "s", "prompt": "p", "cron": "0 2 * * *", "tz": "UTC"}, 201, &sc)
	for _, field := range []string{"providerInstanceId", "model", "modelOptions", "runtimeMode", "interactionMode"} {
		want := field + " was removed in API v8; send executionConfig with connectionId"
		for _, req := range []struct {
			method, path string
			body         map[string]any
		}{
			{"POST", "/api/tasks", map[string]any{"projectId": p.ID, "title": "x", "prompt": "y", "connectionId": dbtest.T3Connection}},
			{"PATCH", fmt.Sprintf("/api/tasks/%d", tk.ID), map[string]any{}},
			{"POST", "/api/schedules", map[string]any{"projectId": p.ID, "title": "x", "prompt": "y", "cron": "0 2 * * *", "tz": "UTC"}},
			{"PATCH", fmt.Sprintf("/api/schedules/%d", sc.ID), map[string]any{}},
		} {
			req.body[field] = nil
			if msg := e.refused(req.method, req.path, req.body, 400); msg != want {
				t.Fatalf("%s %s with %s: %q, want %q", req.method, req.path, field, msg, want)
			}
		}
	}
}

// A worktree is a harness launch option: workspace is a 400 that names
// executionConfig.options, on task and schedule create and update.
func TestRemovedWorkspaceFieldNamesLaunchOptions(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/v8-ws")
	tk := e.task(p.ID, "A", nil)
	var sc schedule
	e.must("POST", "/api/schedules", map[string]any{"projectId": p.ID, "title": "s", "prompt": "p", "cron": "0 2 * * *", "tz": "UTC"}, 201, &sc)
	for _, req := range []struct {
		method, path string
		body         map[string]any
	}{
		{"POST", "/api/tasks", map[string]any{"projectId": p.ID, "title": "x", "prompt": "y", "connectionId": dbtest.T3Connection}},
		{"PATCH", fmt.Sprintf("/api/tasks/%d", tk.ID), map[string]any{}},
		{"POST", "/api/schedules", map[string]any{"projectId": p.ID, "title": "x", "prompt": "y", "cron": "0 2 * * *", "tz": "UTC"}},
		{"PATCH", fmt.Sprintf("/api/schedules/%d", sc.ID), map[string]any{}},
	} {
		req.body["workspace"] = map[string]any{"type": "worktree", "baseRef": "origin/main"}
		msg := e.refused(req.method, req.path, req.body, 400)
		if !strings.HasPrefix(msg, "workspace was removed in API v8") || !strings.Contains(msg, "executionConfig.options") {
			t.Fatalf("%s %s with workspace: %q", req.method, req.path, msg)
		}
	}
	// The replacement: T3's worktree launch options.
	opts := []map[string]any{{"id": "worktree", "value": true}, {"id": "baseRef", "value": "origin/main"}}
	var created task
	e.must("POST", "/api/tasks", map[string]any{"projectId": p.ID, "title": "x", "prompt": "y", "connectionId": dbtest.T3Connection, "executionConfig": map[string]any{"options": opts}}, 201, &created)
	if created.Config == nil || len(created.Config.Options) != 2 || !strings.Contains(string(created.Config.Options[1]), `"origin/main"`) {
		t.Fatalf("launch options %+v", created.Config)
	}
	if msg := e.refused("POST", "/api/tasks", map[string]any{"projectId": p.ID, "title": "x", "prompt": "y", "connectionId": dbtest.T3Connection, "executionConfig": map[string]any{"options": opts[1:]}}, 400); !strings.Contains(msg, "applies only when worktree is true") {
		t.Fatalf("baseRef without worktree: %q", msg)
	}
}

// With no harness binding and no default connection, a task or schedule
// names its connection: there is no default harness.
func TestConnectionRequiredWithoutBinding(t *testing.T) {
	e := newEnv(t)
	var p project
	e.must("POST", "/api/projects", map[string]any{"title": "Unbound"}, 201, &p)
	msg := e.refused("POST", "/api/tasks", map[string]any{"projectId": p.ID, "title": "x", "prompt": "y"}, 400)
	if !strings.Contains(msg, "connectionId is required") {
		t.Fatalf("task without connection: %q", msg)
	}
	msg = e.refused("POST", "/api/schedules", map[string]any{"projectId": p.ID, "title": "x", "prompt": "y", "cron": "0 2 * * *", "tz": "UTC"}, 400)
	if !strings.Contains(msg, "connectionId is required") {
		t.Fatalf("schedule without connection: %q", msg)
	}
}

// connectionId and executionConfig choose the harness and its config, on
// T3 like any other harness.
func TestConnectionAndExecutionConfigOnT3(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/t3")
	cfg := map[string]any{"model": t3.ModelKey("claude", "claude-haiku-5-5"), "options": []map[string]any{{"id": "effort", "value": "low"}}}
	var tk struct {
		task
		ConnectionID int64 `json:"connectionId"`
	}
	e.must("POST", "/api/tasks", map[string]any{"projectId": p.ID, "title": "A", "prompt": "p", "connectionId": dbtest.T3Connection, "executionConfig": cfg}, 201, &tk)
	if tk.ConnectionID != dbtest.T3Connection || tk.Config == nil || tk.Config.Model != t3.ModelKey("claude", "claude-haiku-5-5") || len(tk.Config.Options) != 1 {
		t.Fatalf("task %+v", tk)
	}
	var started task
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", tk.ID), nil, 200, &started)
	d, err := e.fake.ReadThread(t.Context(), *started.ExternalSessionID)
	if err != nil || d.ProviderInstanceID != "claude" || d.Model != "claude-haiku-5-5" {
		t.Fatalf("launched %+v %v", d, err)
	}
	var sc struct {
		ID           int64       `json:"id"`
		ConnectionID int64       `json:"connectionId"`
		Config       *execConfig `json:"executionConfig"`
	}
	e.must("POST", "/api/schedules", map[string]any{"projectId": p.ID, "title": "s", "prompt": "p", "cron": "0 2 * * *", "tz": "UTC", "connectionId": dbtest.T3Connection, "executionConfig": cfg}, 201, &sc)
	if sc.ConnectionID != dbtest.T3Connection || sc.Config == nil || sc.Config.Model != t3.ModelKey("claude", "claude-haiku-5-5") {
		t.Fatalf("schedule %+v", sc)
	}
}

// The same inputs choose a non-T3 harness.
func TestConnectionAndExecutionConfigOnAnotherHarness(t *testing.T) {
	e, _, _, textConn, projectID := imagesEnv(t)
	cfg := map[string]any{"model": "m"}
	var tk struct {
		task
		ConnectionID int64 `json:"connectionId"`
	}
	e.must("POST", "/api/tasks", map[string]any{"projectId": projectID, "title": "A", "prompt": "p", "connectionId": textConn, "executionConfig": cfg}, 201, &tk)
	if tk.ConnectionID != textConn || tk.Config == nil || tk.Config.Model != "m" {
		t.Fatalf("task %+v", tk)
	}
	var sc struct {
		ConnectionID int64       `json:"connectionId"`
		Config       *execConfig `json:"executionConfig"`
	}
	e.must("POST", "/api/schedules", map[string]any{"projectId": projectID, "title": "s", "prompt": "p", "cron": "0 2 * * *", "tz": "UTC", "connectionId": textConn, "executionConfig": cfg}, 201, &sc)
	if sc.ConnectionID != textConn || sc.Config == nil || sc.Config.Model != "m" {
		t.Fatalf("schedule %+v", sc)
	}
}

// API v8 removed the T3-only routes.
func TestT3RoutesRemoved(t *testing.T) {
	e := newEnv(t)
	e.must("GET", "/api/catalog", nil, 404, nil)
	// "t3" is no connection id: the generic connection route refuses it.
	if msg := e.refused("GET", "/api/connections/t3/projects", nil, 400); !strings.Contains(msg, "connection") {
		t.Fatalf("t3 projects %q", msg)
	}
}
