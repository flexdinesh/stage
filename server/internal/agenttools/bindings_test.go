package agenttools_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/client"
)

func TestMCPWorkspaceChoice(t *testing.T) {
	const project = `{"id":1,"title":"Current","aliases":[{"id":2,"title":"Former"}],"bindings":[
		{"id":40,"connectionId":7,"connectionActive":true,"status":"available","primary":true},
		{"id":41,"connectionId":8,"connectionActive":true,"status":"available","primary":false},
		{"id":42,"connectionId":8,"connectionActive":true,"status":"available","primary":true}
	]}`
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/projects":
			io.WriteString(w, `{"projects":[`+project+`]}`)
		case "GET /api/connections":
			io.WriteString(w, `{"connections":[{"id":7,"active":true},{"id":8,"active":true}]}`)
		case "GET /api/preferences":
			io.WriteString(w, `{"defaultConnectionId":7}`)
		case "GET /api/connections/8/capabilities":
			if r.URL.Query().Get("projectId") != "1" {
				t.Errorf("capabilities query: %s", r.URL.RawQuery)
			}
			switch r.URL.Query().Get("bindingId") {
			case "41":
				io.WriteString(w, `{"models":[{"id":"workspace-model","label":"Workspace model","available":true}]}`)
			case "":
				io.WriteString(w, `{"models":[{"id":"primary-model","label":"Primary model","available":true}]}`)
			default:
				t.Errorf("capabilities query: %s", r.URL.RawQuery)
			}
		case "GET /api/tasks/13", "GET /api/schedules/13":
			io.WriteString(w, `{"id":13,"projectId":1,"connectionId":8,"bindingId":41}`)
		case "GET /api/connections/8/sessions/chosen/context":
			io.WriteString(w, `{"project":`+project+`,"task":{"id":13,"bindingId":41}}`)
		case "GET /api/connections/8/sessions/primary/context":
			io.WriteString(w, `{"project":`+project+`,"task":{"id":13,"bindingId":null,"workspace":{"bindingId":42,"source":"primary"}}}`)
		case "GET /api/connections/8/sessions/pinned/context":
			io.WriteString(w, `{"project":`+project+`,"task":{"id":13,"bindingId":41,"workspace":{"bindingId":42,"source":"pinned"}}}`)
		case "POST /api/tasks", "POST /api/schedules", "PATCH /api/tasks/13", "PATCH /api/schedules/13":
			var in struct {
				Title        string          `json:"title"`
				ProjectID    int64           `json:"projectId"`
				ConnectionID int64           `json:"connectionId"`
				BindingID    json.RawMessage `json:"bindingId"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Errorf("decode request: %v", err)
			}
			want := map[string]string{"chosen": "41", "primary": "null", "unchanged": ""}[in.Title]
			if string(in.BindingID) != want {
				t.Errorf("%s %s bindingId = %q, want %q", r.Method, r.URL.Path, in.BindingID, want)
			}
			if r.Method == "POST" {
				connectionID := int64(8)
				if in.Title == "default" {
					connectionID = 7
				}
				if in.ProjectID != 1 || in.ConnectionID != connectionID {
					t.Errorf("create destination = %d/%d, want 1/%d", in.ProjectID, in.ConnectionID, connectionID)
				}
			}
			io.WriteString(w, `{"id":13}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	})
	cs := connect(t, &agenttools.Ops{Client: client.NewInProcess(handler, "https://stage.test")})

	var projects agenttools.ProjectsOut
	if msg := call(t, cs, "list_projects", nil, &projects); msg != "" || len(projects.Projects) != 1 ||
		len(projects.Projects[0].Aliases) != 1 || projects.Projects[0].Aliases[0].ID != 2 {
		t.Fatalf("list_projects = %+v, %s", projects, msg)
	}
	for session, want := range map[string]string{"chosen": "41", "primary": "null", "pinned": "42"} {
		var ctx struct {
			BindingID json.RawMessage `json:"bindingId"`
		}
		msg := call(t, cs, "get_context", map[string]any{
			"callerSession": map[string]any{"connectionId": 8, "externalSessionId": session},
		}, &ctx)
		if msg != "" || string(ctx.BindingID) != want {
			t.Errorf("get_context(%s) bindingId = %q, %s, want %s", session, ctx.BindingID, msg, want)
		}
	}
	for _, tool := range []string{"create_task", "create_schedule"} {
		args := map[string]any{"title": "chosen", "project": "Former", "prompt": "Build it", "bindingId": 41,
			"executionConfig": map[string]any{"model": "workspace-model"}}
		if tool == "create_schedule" {
			args["cron"], args["tz"] = "@daily", "UTC"
		}
		if msg := call(t, cs, tool, args, nil); msg != "" {
			t.Errorf("%s: %s", tool, msg)
		}
		args["title"], args["bindingId"] = "default", nil
		delete(args, "executionConfig")
		if msg := call(t, cs, tool, args, nil); msg != "" {
			t.Errorf("%s using the primary: %s", tool, msg)
		}
	}
	for _, tool := range []string{"update_task", "update_schedule"} {
		for _, args := range []map[string]any{
			{"id": 13, "title": "chosen", "bindingId": 41, "executionConfig": map[string]any{"model": "workspace-model"}},
			{"id": 13, "title": "primary", "bindingId": nil, "executionConfig": map[string]any{"model": "primary-model"}},
			{"id": 13, "title": "unchanged", "executionConfig": map[string]any{"model": "workspace-model"}},
		} {
			if msg := call(t, cs, tool, args, nil); msg != "" {
				t.Errorf("%s %v: %s", tool, args, msg)
			}
		}
	}
}
