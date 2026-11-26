package cli_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
)

func TestProjectWorkspaceCommands(t *testing.T) {
	const project = `{"id":1,"title":"Current","aliases":[{"id":2,"title":"Former"}],"bindings":[
		{"id":40,"machineId":3,"connectionId":7,"harness":"codex","workspace":"/primary","status":"available","primary":true,"remote":"example.test/Org/Repo"},
		{"id":41,"machineId":3,"connectionId":7,"harness":"codex","workspace":"/secondary","status":"available","primary":false}
	]}`
	const review = `{"workspaces":[
		{"reason":"suggested","connectionId":7,"workspace":"/candidate","resolution":{"candidates":[1,3]}},
		{"reason":"new","connectionId":8,"workspace":"/unrelated","resolution":{"candidates":[]}}
	],"merges":[],"counts":{"new":1,"suggested":1},"attention":1}`
	var previewCalls, confirmCalls, splitCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/projects":
			io.WriteString(w, `{"projects":[`+project+`],"imports":[]}`)
		case "GET /api/projects/2":
			io.WriteString(w, project)
		case "GET /api/connections":
			io.WriteString(w, `{"connections":[]}`)
		case "GET /api/workspaces/review":
			io.WriteString(w, review)
		case "POST /api/projects/1/merge":
			var in struct {
				From         int64  `json:"from"`
				Confirm      bool   `json:"confirm"`
				PreviewToken string `json:"previewToken"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Errorf("decode merge: %v", err)
			}
			if in.From != 3 {
				t.Errorf("merge from %d, want 3", in.From)
			}
			if in.Confirm {
				if want := "preview-for-current-merge-2"; in.PreviewToken != want || previewCalls.Load() != 2 {
					t.Errorf("confirmation token %q, want %q after the second preview", in.PreviewToken, want)
				}
				confirmCalls.Add(1)
				io.WriteString(w, `{"preview":false,"project":`+project+`}`)
				return
			}
			if in.PreviewToken != "" {
				t.Errorf("preview request sent token %q", in.PreviewToken)
			}
			fmt.Fprintf(w, `{"preview":true,"project":%s,"from":{"id":3,"title":"Source"},
				"moves":{"tasks":7,"schedules":2,"snippets":4,"workspaces":3},"previewToken":"preview-for-current-merge-%d"}`,
				project, previewCalls.Add(1))
		case "POST /api/projects/1/split":
			splitCalls.Add(1)
			var in struct {
				BindingID int64  `json:"bindingId"`
				Title     string `json:"title"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.BindingID != 41 || in.Title != "Split" {
				t.Errorf("split request = %+v, %v", in, err)
			}
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{"project":{"id":5,"title":"Split"},"source":`+project+`}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	h := &harness{t: t, url: srv.URL}
	run := func(args ...string) string {
		t.Helper()
		code, out, stderr := h.run("", append([]string{"projects"}, args...)...)
		if code != 0 || stderr != "" {
			t.Fatalf("projects %v = %d, %s, %s", args, code, out, stderr)
		}
		return out
	}

	list := run("ls")
	if !strings.Contains(list, "/primary") || strings.Contains(list, "/secondary") {
		t.Errorf("project list should show primary paths: %s", list)
	}
	for _, ref := range [][]string{nil, {"2"}} {
		out := run(append([]string{"workspaces"}, ref...)...)
		for _, want := range []string{"PROJECT", "BINDING", "MACHINE", "HARNESS", "PATH", "STATE", "PRIMARY", "REMOTE", "/primary", "/secondary"} {
			if !strings.Contains(out, want) {
				t.Errorf("workspace output lacks %q: %s", want, out)
			}
		}
	}
	if out := decode[db.Project](t, run("workspaces", "2", "--json")); out.ID != 1 || len(out.Bindings) != 2 {
		t.Errorf("workspace alias JSON: %+v", out)
	}
	if out := run("workspaces", "--review"); !strings.Contains(out, "/candidate") || !strings.Contains(out, "/unrelated") {
		t.Errorf("review output: %s", out)
	}
	if out := run("workspaces", "2", "--review"); !strings.Contains(out, "/candidate") || strings.Contains(out, "/unrelated") {
		t.Errorf("project review output: %s", out)
	}
	if out := decode[db.WorkspaceReview](t, run("workspaces", "--review", "--json")); out.Attention != 1 || len(out.Workspaces) != 2 {
		t.Errorf("review JSON: %+v", out)
	}

	if out := run("merge", "3", "Former"); !strings.Contains(out, "7 tasks, 2 schedules, 4 snippets, 3 workspaces") ||
		!strings.Contains(out, "Run again with --yes to merge.") || confirmCalls.Load() != 0 {
		t.Errorf("merge preview: %s", out)
	}
	if out := decode[db.MergeResult](t, run("merge", "3", "1", "--yes", "--json")); out.Project.ID != 1 || out.Preview || confirmCalls.Load() != 1 {
		t.Errorf("merge confirmation: %+v", out)
	}
	if code, _, stderr := h.run("", "projects", "split", "1", "--binding", "41"); code != 2 ||
		!strings.Contains(stderr, "split needs --yes") || splitCalls.Load() != 0 {
		t.Errorf("unconfirmed split = %d, %s", code, stderr)
	}
	if out := decode[db.SplitResult](t, run("split", "1", "--binding", "41", "--title", "Split", "--yes", "--json")); out.Project.ID != 5 || out.Source.ID != 1 {
		t.Errorf("split JSON: %+v", out)
	}
}

func TestTaskAndScheduleBindingFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/harnesses":
			io.WriteString(w, `{"harnesses":[]}`)
		case "GET /api/connections":
			io.WriteString(w, `{"connections":[{"id":7,"active":true}]}`)
		case "GET /api/projects":
			io.WriteString(w, `{"projects":[{"id":1,"title":"Project","bindings":[
				{"id":40,"connectionId":7,"connectionActive":true,"status":"available","primary":true},
				{"id":41,"connectionId":7,"connectionActive":true,"status":"available","primary":false}
			]}]}`)
		case "POST /api/tasks", "POST /api/schedules", "PATCH /api/tasks/5", "PATCH /api/schedules/5":
			var in struct {
				Title     string          `json:"title"`
				BindingID json.RawMessage `json:"bindingId"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Errorf("decode request: %v", err)
			}
			want := map[string]string{"chosen": "41", "cleared": "null", "unchanged": ""}[in.Title]
			if string(in.BindingID) != want {
				t.Errorf("%s %s bindingId = %q, want %q", r.Method, r.URL.Path, in.BindingID, want)
			}
			io.WriteString(w, `{"id":5}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	h := &harness{t: t, url: srv.URL}
	for _, kind := range []string{"task", "schedule"} {
		create := []string{kind, "add", "--project", "1", "--title", "chosen", "--prompt", "Build it", "--binding", "41"}
		if kind == "schedule" {
			create = append(create, "--cron", "@daily", "--tz", "UTC")
		}
		for _, args := range [][]string{
			create,
			{kind, "edit", "5", "--title", "chosen", "--binding", "41"},
			{kind, "edit", "5", "--title", "cleared", "--binding", ""},
			{kind, "edit", "5", "--title", "unchanged"},
		} {
			if code, out, stderr := h.run("", args...); code != 0 || stderr != "" {
				t.Errorf("%v = %d, %s, %s", args, code, out, stderr)
			}
		}
	}
}
