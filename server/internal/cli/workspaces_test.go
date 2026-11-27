package cli_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func TestWorkspacesCommands(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.RequestURI() {
		case "GET /api/harnesses":
			io.WriteString(w, `{"harnesses":[]}`)
		case "GET /api/connections/3/capabilities":
			json.NewEncoder(w).Encode(execution.Capabilities{
				Operations: map[execution.Op]execution.Availability{execution.OpWorkspaceAdd: {Available: true}},
				WorkspaceSources: []execution.WorkspaceSource{{ID: "trusted", Label: "Trust folder", Confirm: "Trust it?",
					Availability: execution.Availability{Available: true},
					Input:        []execution.Field{{ID: "path", Label: "Folder", Kind: execution.FieldPath, Required: true}}}},
			})
		case "POST /api/connections/3/workspaces":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			bodies = append(bodies, body)
			var out client.AddedWorkspace
			out.Workspace.WorkspaceID, out.Workspace.Title, out.Workspace.Path = "ws_1", "repo", "/w/repo"
			out.Group.ID, out.Group.Title = 9, "Repo"
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(out)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	h := &harness{t: t, url: srv.URL}

	code, out, stderr := h.run("", "workspaces", "sources", "--connection", "3")
	if code != 0 || !strings.Contains(out, "trusted  Trust folder") || !strings.Contains(out, "Trust it? (requires --confirm)") ||
		!strings.Contains(out, "path  Folder (path, required)") {
		t.Fatalf("sources %d %q %q", code, out, stderr)
	}
	code, out, _ = h.run("", "workspaces", "sources", "--connection", "3", "--json")
	if sources := decode[agenttools.WorkspaceSourcesOut](t, out); code != 0 || !sources.Availability.Available || len(sources.Sources) != 1 {
		t.Fatalf("sources json %d %s", code, out)
	}

	code, out, stderr = h.run(`{"path":"/w/repo"}`, "workspaces", "add", "--connection", "3", "--source", "trusted", "--input", "-", "--project", "9", "--confirm")
	if code != 0 || !strings.Contains(out, "Added ws_1  repo") || !strings.Contains(out, "project #9 Repo") {
		t.Fatalf("add %d %q %q", code, out, stderr)
	}
	code, _, _ = h.run("", "workspaces", "add", "--connection", "3", "--source", "trusted", "--input", `{"path":"/w/repo"}`, "--project", "9", "--confirm")
	if code != 0 || len(bodies) != 2 {
		t.Fatalf("second add %d %v", code, bodies)
	}
	first := bodies[0]
	if first["sourceId"] != "trusted" || first["confirmed"] != true || first["projectId"] != float64(9) ||
		first["input"].(map[string]any)["path"] != "/w/repo" || !strings.HasPrefix(first["requestId"].(string), "workspace-add-") {
		t.Fatalf("add body %v", first)
	}
	if bodies[1]["requestId"] != first["requestId"] {
		t.Fatalf("derived requestId differs for the same request: %v vs %v", bodies[1]["requestId"], first["requestId"])
	}
	code, _, _ = h.run("", "workspaces", "add", "--connection", "3", "--source", "trusted", "--input", `{"path":"/w/other"}`, "--request-id", "mine")
	if code != 0 || bodies[2]["requestId"] != "mine" || bodies[2]["requestId"] == first["requestId"] {
		t.Fatalf("explicit requestId %v", bodies[2])
	}
	for _, args := range [][]string{
		{"workspaces", "add", "--source", "trusted"},
		{"workspaces", "add", "--connection", "3"},
		{"workspaces", "add", "--connection", "3", "--source", "trusted", "--input", "[]"},
		{"workspaces", "sources"},
	} {
		if code, _, _ := h.run("", args...); code != 2 {
			t.Errorf("%v: exit %d, want usage error", args, code)
		}
	}
}
