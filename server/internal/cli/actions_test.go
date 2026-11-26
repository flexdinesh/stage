package cli_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func TestActionCommandsForUsers(t *testing.T) {
	const journalWarning = "The action ran, but Stage could not record it in the task journal."
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/harnesses" {
			io.WriteString(w, `{"harnesses":[]}`)
			return
		}
		requests.Add(1)
		if r.Header.Get("Stage-Action-Agent") != "" || r.Header.Get("Stage-Caller-Session") != "" {
			t.Errorf("user headers %v", r.Header)
		}
		switch r.Method + " " + r.URL.RequestURI() {
		case "GET /api/connections/2/actions":
			if got := r.Header.Get("Stage-Action-Confirmed"); got != "" {
				t.Errorf("list confirmation %q", got)
			}
			json.NewEncoder(w).Encode([]execution.Action{{
				ID: "show.details", Label: "Show details", Target: execution.TargetConnection,
				Availability: execution.Availability{Available: true},
			}})
		case "POST /api/tasks/4/actions/show.details":
			if got := r.Header.Get("Stage-Action-Confirmed"); got != "1" {
				t.Errorf("run confirmation %q", got)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil || string(body) != "{\"label\": \"sample\"}\n" {
				t.Errorf("run body %q, %v", body, err)
			}
			json.NewEncoder(w).Encode(execution.ActionResult{
				Message: "Details loaded", Facts: []execution.Fact{{ID: "path", Label: "Path", Value: "/tmp"}},
				Link: "https://example.test/detail", JournalError: journalWarning,
			})
		case "POST /api/connections/2/actions/replace":
			json.NewEncoder(w).Encode(execution.ActionResult{Message: "Connected", Replaced: 5})
		case "POST /api/projects/3/actions/show.details?bindingId=6&connectionId=2":
			body, err := io.ReadAll(r.Body)
			if err != nil || string(body) != "{}" || r.Header.Get("Stage-Action-Confirmed") != "" {
				t.Errorf("default run body %q, %v, headers %v", body, err, r.Header)
			}
			w.WriteHeader(http.StatusConflict)
			io.WriteString(w, `{"error":"Action outcome is unknown. Check the harness before retrying.","code":"action_uncertain"}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	h := &harness{t: t, url: srv.URL}
	code, out, stderr := h.run("", "action", "ls", "--connection", "2", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("list %d %s %s", code, out, stderr)
	}
	if actions := decode[[]execution.Action](t, out); len(actions) != 1 || actions[0].ID != "show.details" {
		t.Fatalf("list output %s", out)
	}
	for _, jsonOutput := range []bool{false, true} {
		args := []string{"action", "run", "show.details", "--task", "4", "--input", "-", "--confirm"}
		if jsonOutput {
			args = append(args, "--json")
		}
		code, out, stderr := h.run("{\"label\": \"sample\"}\n", args...)
		if code != 0 {
			t.Fatalf("run %d %s %s", code, out, stderr)
		}
		if jsonOutput {
			result := decode[execution.ActionResult](t, out)
			if result.JournalError != journalWarning || len(result.Facts) != 1 || stderr != "" {
				t.Fatalf("JSON result %+v, stderr %s", result, stderr)
			}
		} else if !strings.Contains(out, "Details loaded") || !strings.Contains(out, "https://example.test/detail") ||
			!strings.Contains(out, "/tmp") ||
			!strings.Contains(stderr, journalWarning) {
			t.Fatalf("human result %s, stderr %s", out, stderr)
		}
	}
	code, out, stderr = h.run("", "action", "run", "replace", "--connection", "2")
	if code != 0 || !strings.Contains(out, "Replaced connection #5") || stderr != "" {
		t.Fatalf("replaced connection %d %s %s", code, out, stderr)
	}
	code, out, stderr = h.run("", "action", "run", "show.details", "--project", "3", "--on-connection", "2", "--binding", "6")
	if code != 1 || !strings.Contains(stderr, "Check the harness before retrying.") {
		t.Fatalf("uncertain action %d %s %s", code, out, stderr)
	}
	for _, args := range [][]string{
		{"action", "ls"},
		{"action", "ls", "--connection", "2", "--task", "4"},
		{"action", "ls", "--project", "3"},
		{"action", "ls", "--task", "4", "--on-connection", "2"},
		{"action", "ls", "--task", "4", "--binding", "6"},
		{"action", "ls", "--connection", "0"},
		{"action", "ls", "--connection", "2", "--confirm"},
		{"action", "run", "--connection", "2"},
		{"action", "run", "show.details", "--task", "4", "--input", "null"},
	} {
		if code, _, _ := h.run("", args...); code != 2 {
			t.Fatalf("invalid command %v exited %d", args, code)
		}
	}
	if got := requests.Load(); got != 5 {
		t.Fatalf("action request count %d", got)
	}
}

func TestActionCommandsDetectAgentsAndStageCallAlwaysUsesAgentPolicy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.RequestURI() {
		case "/api/harnesses":
			io.WriteString(w, `{"harnesses":[
				{"kind":"available","attribution":{"env":"ACTION_TEST_SESSION"}},
				{"kind":"missing","attribution":{"env":"ACTION_MISSING_SESSION"}}
			]}`)
		case "/api/connections/resolve?harness=available":
			io.WriteString(w, `{"id":7}`)
		case "/api/connections/resolve?harness=missing":
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"error":"no configured connection"}`)
		case "/api/tasks/4/actions", "/api/tasks/4/actions/user-only", "/api/tasks/4/actions/known":
			wantCaller, wantConfirm := "", ""
			if r.URL.Path == "/api/tasks/4/actions/known" {
				wantCaller, wantConfirm = "7:known", "1"
			}
			if r.Header.Get("Stage-Action-Agent") != "1" || r.Header.Get("Stage-Caller-Session") != wantCaller ||
				r.Header.Get("Stage-Action-Confirmed") != wantConfirm {
				t.Errorf("agent headers %v, want caller %q and confirmation %q", r.Header, wantCaller, wantConfirm)
			}
			if r.Method == http.MethodGet {
				io.WriteString(w, `[]`)
			} else {
				w.WriteHeader(http.StatusForbidden)
				io.WriteString(w, `{"error":"Action is available only to users.","code":"agent_forbidden"}`)
			}
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	h := &harness{t: t, url: srv.URL, env: map[string]string{"ACTION_TEST_SESSION": "known"}}
	code, _, stderr := h.run("", "action", "run", "known", "--task", "4", "--confirm")
	if code != 1 || !strings.Contains(stderr, "only to users") {
		t.Fatalf("resolved agent %d %s", code, stderr)
	}
	h.env = map[string]string{"ACTION_MISSING_SESSION": "detectable"}
	code, out, stderr := h.run("", "action", "ls", "--task", "4", "--json")
	if code != 0 || strings.TrimSpace(out) != "[]" {
		t.Fatalf("unresolved agent %d %s %s", code, out, stderr)
	}
	h.env = nil
	code, out, stderr = h.run("", "call", "list_actions", `{"taskId":4}`)
	if code != 0 || decode[agenttools.ActionsOut](t, out).Actions == nil {
		t.Fatalf("stage call list %d %s %s", code, out, stderr)
	}
	code, _, stderr = h.run("", "call", "run_action", `{"taskId":4,"actionId":"user-only"}`)
	if code != 1 || !strings.Contains(stderr, "only to users") {
		t.Fatalf("stage call run %d %s", code, stderr)
	}
}
