package cli_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agenttools"
)

func TestProjectDestinations(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.RequestURI() {
		case "GET /api/projects?view=destinations":
			io.WriteString(w, `{"groups":[{"id":1,"title":"Current"}],"destinations":[
				{"connectionId":7,"workspaceId":"ws_grouped","title":"Build","connectionLabel":"Codex","machineLabel":"Laptop","discriminator":"4ab2","groupProjectId":1,"available":true},
				{"connectionId":8,"workspaceId":"ws_ungrouped","title":"Build","connectionLabel":"Codex","machineLabel":"Laptop","discriminator":"5nd9","available":true}
			]}`)
		case "GET /api/connections":
			io.WriteString(w, `{"connections":[]}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	h := &harness{t: t, url: srv.URL}
	code, out, stderr := h.run("", "projects", "ls")
	if code != 0 || stderr != "" {
		t.Fatalf("projects ls = %d, %s, %s", code, out, stderr)
	}
	for _, want := range []string{"#1  Current", "ws_grouped", "ws_ungrouped", "Build (4ab2)", "Build (5nd9)", "available"} {
		if !strings.Contains(out, want) {
			t.Errorf("project list lacks %q: %s", want, out)
		}
	}
	code, out, stderr = h.run("", "projects", "ls", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("projects ls --json = %d, %s, %s", code, out, stderr)
	}
	catalog := decode[agenttools.ProjectsOut](t, out)
	if len(catalog.Groups) != 1 || catalog.Groups[0].ID != 1 || len(catalog.Destinations) != 2 ||
		catalog.Destinations[0].GroupProjectID == nil || *catalog.Destinations[0].GroupProjectID != 1 ||
		catalog.Destinations[1].GroupProjectID != nil {
		t.Fatalf("project destinations: %+v", catalog)
	}
}

func TestRetiredDestinationCommandsAndSelectors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("retired command reached API: %s %s", r.Method, r.URL)
		http.NotFound(w, r)
	}))
	defer srv.Close()
	h := &harness{t: t, url: srv.URL}
	for _, tc := range []struct {
		args   []string
		repair string
	}{
		{[]string{"projects", "workspaces", "1", "--review"}, "Use projects ls for exact destinations"},
		{[]string{"projects", "merge", "1", "2", "--yes"}, "Use Projects to move or separate an exact workspace"},
		{[]string{"projects", "split", "1", "--binding", "41", "--yes"}, "Use Projects to move or separate an exact workspace"},
		{[]string{"models", "ls", "--connection", "7", "--project", "1"}, "Choose the connection and --workspace-id from projects ls"},
		{[]string{"models", "ls", "--connection", "7", "--binding", "41"}, "Choose the connection and --workspace-id from projects ls"},
		{[]string{"task", "add", "--project", "1", "--title", "T", "--prompt", "P", "--binding", "41"}, "Choose the connection and --workspace-id from projects ls"},
		{[]string{"task", "edit", "5", "--binding="}, "Choose the connection and --workspace-id from projects ls"},
		{[]string{"schedule", "add", "--title", "S", "--prompt", "P", "--cron", "@daily", "--tz", "UTC", "--binding", "41"}, "Choose the connection and --workspace-id from projects ls"},
		{[]string{"schedule", "edit", "5", "--binding="}, "Choose the connection and --workspace-id from projects ls"},
		{[]string{"action", "ls", "--project", "1", "--on-connection", "7", "--binding", "41"}, "Choose the connection and --workspace-id from projects ls"},
		{[]string{"action", "run", "inspect", "--project", "1", "--on-connection", "7", "--binding", "41"}, "Choose the connection and --workspace-id from projects ls"},
	} {
		if code, _, stderr := h.run("", tc.args...); code != 2 || !strings.Contains(stderr, "was retired") || !strings.Contains(stderr, tc.repair) {
			t.Errorf("%v = %d, %s", tc.args, code, stderr)
		}
	}
}

func TestLaunchCommandsRequireExactWorkspace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/harnesses" {
			io.WriteString(w, `{"harnesses":[]}`)
			return
		}
		t.Errorf("incomplete launch destination reached API: %s %s", r.Method, r.URL)
		http.NotFound(w, r)
	}))
	defer srv.Close()
	h := &harness{t: t, url: srv.URL}
	for _, args := range [][]string{
		{"task", "add", "--start"},
		{"task", "add", "--start-at", "2027-01-01T09:00", "--tz", "UTC"},
		{"schedule", "add", "--cron", "@daily", "--tz", "UTC"},
	} {
		args = append(args, "--project", "1", "--connection", "8", "--title", "Work", "--prompt", "Do it")
		if code, _, stderr := h.run("", args...); code != 1 || !strings.Contains(stderr, "select connectionId and workspaceId from list_projects") {
			t.Errorf("%v = %d, %s", args, code, stderr)
		}
	}
}

func TestExactWorkspaceFlags(t *testing.T) {
	const workspace = "ws_second_root"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/harnesses":
			io.WriteString(w, `{"harnesses":[]}`)
		case "GET /api/connections":
			io.WriteString(w, `{"connections":[{"id":7,"active":true},{"id":8,"active":true}]}`)
		case "GET /api/connections/8/capabilities":
			switch r.URL.RawQuery {
			case "":
				io.WriteString(w, `{"models":[{"id":"connection-model","available":true}]}`)
			case "workspaceId=" + workspace:
				io.WriteString(w, `{"models":[{"id":"selected-model","label":"Selected model","available":true}]}`)
			default:
				t.Errorf("capabilities scope = %s", r.URL.RawQuery)
			}
		case "POST /api/tasks", "POST /api/schedules", "PATCH /api/tasks/5", "PATCH /api/schedules/5":
			var in struct {
				Title        string          `json:"title"`
				WorkspaceID  json.RawMessage `json:"workspaceId"`
				ConnectionID int64           `json:"connectionId"`
				ProjectID    json.RawMessage `json:"projectId"`
				TreeID       *int64          `json:"treeId"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Errorf("decode request: %v", err)
			}
			want := map[string]string{"create": `"` + workspace + `"`, "reselect": `"` + workspace + `"`, "clear": "null", "unchanged": ""}[in.Title]
			if string(in.WorkspaceID) != want || len(in.ProjectID) != 0 {
				t.Errorf("%s %s workspaceId = %s, want %s, projectId = %s", r.Method, r.URL.Path, in.WorkspaceID, want, in.ProjectID)
			}
			if (in.Title == "create" || in.Title == "reselect") && in.ConnectionID != 8 {
				t.Errorf("%s %s connectionId = %d, want 8", r.Method, r.URL.Path, in.ConnectionID)
			}
			if r.Method == "POST" && r.URL.Path == "/api/tasks" && (in.TreeID == nil || *in.TreeID != 12) {
				t.Errorf("member create lost its tree: %+v", in)
			}
			io.WriteString(w, `{"id":5}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	h := &harness{t: t, url: srv.URL}
	if code, out, stderr := h.run("", "models", "ls", "--connection", "8"); code != 0 || !strings.Contains(out, "connection-model") {
		t.Fatalf("connection models ls = %d, %s, %s", code, out, stderr)
	}
	if code, out, stderr := h.run("", "models", "ls", "--connection", "8", "--workspace-id", workspace); code != 0 || !strings.Contains(out, "selected-model") {
		t.Fatalf("models ls = %d, %s, %s", code, out, stderr)
	}
	for _, kind := range []string{"task", "schedule"} {
		create := []string{kind, "add", "--title", "create", "--prompt", "Build it", "--connection", "8", "--workspace-id", workspace, "--execution-config", `{"model":"selected-model"}`}
		if kind == "task" {
			create = append(create, "--tree", "12")
		} else {
			create = append(create, "--cron", "@daily", "--tz", "UTC")
		}
		commands := [][]string{
			create,
			{kind, "edit", "5", "--title", "reselect", "--connection", "8", "--workspace-id", workspace},
			{kind, "edit", "5", "--title", "unchanged"},
		}
		if kind == "task" {
			commands = append(commands, []string{kind, "edit", "5", "--title", "clear", "--workspace-id", ""})
		}
		for _, args := range commands {
			if code, out, stderr := h.run("", args...); code != 0 || stderr != "" {
				t.Errorf("%v = %d, %s, %s", args, code, out, stderr)
			}
		}
	}
}
