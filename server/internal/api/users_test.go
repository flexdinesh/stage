package api_test

import (
	"fmt"
	"testing"
)

func TestMeAndCreatedBy(t *testing.T) {
	e := newEnv(t)
	var me struct {
		ID          int64  `json:"id"`
		DisplayName string `json:"displayName"`
		CreatedAt   string `json:"createdAt"`
	}
	e.must("GET", "/api/me", nil, 200, &me)
	if me.ID != 1 || me.DisplayName != "Default user" || me.CreatedAt == "" {
		t.Fatalf("me = %+v", me)
	}

	type created struct {
		ID        int64  `json:"id"`
		CreatedBy *int64 `json:"createdBy"`
	}
	check := func(name string, c created) {
		t.Helper()
		if c.CreatedBy == nil || *c.CreatedBy != me.ID {
			t.Errorf("%s createdBy = %v, want %d", name, c.CreatedBy, me.ID)
		}
	}
	p := e.project("/w/me")
	var proj created
	e.must("GET", fmt.Sprintf("/api/projects/%d", p.ID), nil, 200, &proj)
	check("project", proj)
	tk := e.task(p.ID, "a", nil)
	var got created
	e.must("GET", fmt.Sprintf("/api/tasks/%d", tk.ID), nil, 200, &got)
	check("task", got)
	var tree created
	e.must("POST", "/api/trees", map[string]any{"title": "T", "tasks": []any{map[string]any{"taskId": tk.ID}}}, 201, &tree)
	check("tree", tree)
	var note created
	e.must("POST", fmt.Sprintf("/api/tasks/%d/journal", tk.ID), map[string]any{"text": "n", "requestId": "me-1"}, 201, &note)
	check("journal entry", note)
	var sc created
	e.must("POST", "/api/schedules", map[string]any{"projectId": p.ID, "title": "s", "prompt": "go", "cron": "0 2 * * *", "tz": "UTC"}, 201, &sc)
	check("schedule", sc)
	var hs struct {
		Machine created `json:"machine"`
	}
	e.must("GET", "/api/harnesses", nil, 200, &hs)
	check("machine", hs.Machine)
}
