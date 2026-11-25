package agenttools_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/codex"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// precedenceAPI serves GET /api/harnesses with a T3-like, an OpenCode-like
// and a nested-harness kind ("cc-like": owns under a background session
// kind, yields to T3's variable or a T3 entrypoint), and resolves each
// kind's connection to a distinct id.
func precedenceAPI(t *testing.T) *client.Client {
	t.Helper()
	conns := map[string]int64{"t3-like": 1, "oc-like": 2, "cc-like": 3, codex.Kind: 4}
	descs := []map[string]any{
		{"kind": "t3-like", "attribution": map[string]any{"metaKeys": []string{}, "env": "T3CODE_THREAD_ID"}},
		{"kind": "oc-like", "attribution": map[string]any{"metaKeys": []string{}, "env": "OPENCODE_SESSION_ID"}},
		{"kind": "cc-like", "attribution": map[string]any{"metaKeys": []string{}, "env": "CLAUDE_CODE_SESSION_ID",
			"precedence": harness.Precedence{
				Owns:   []harness.EnvMatch{{Env: "CLAUDE_CODE_SESSION_KIND", Value: "bg"}},
				Yields: []harness.EnvMatch{{Env: "T3CODE_THREAD_ID"}, {Env: "CLAUDE_CODE_ENTRYPOINT", Value: "t3-code"}},
			}}},
		{"kind": "no-env", "attribution": map[string]any{"metaKeys": []string{}}},
		// The real Codex descriptor: yields to T3.
		{"kind": codex.Kind, "attribution": codex.Descriptor.Attribution},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/harnesses":
			_ = json.NewEncoder(w).Encode(map[string]any{"harnesses": descs})
		case r.URL.Path == "/api/connections/resolve":
			id, ok := conns[r.URL.Query().Get("harness")]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "no active connection"})
				return
			}
			_ = json.NewEncoder(w).Encode(db.HarnessConnection{ID: id})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
		}
	}))
	t.Cleanup(srv.Close)
	return client.New(srv.URL)
}

// Several harness session variables in one process resolve by each
// harness's attribution precedence, never by naming a harness.
func TestHarnessEnvPrecedence(t *testing.T) {
	c := precedenceAPI(t)
	cases := []struct {
		name    string
		env     map[string]string
		conn    int64 // 0: no caller
		id      string
		source  string
		warning string
	}{
		{"T3 inside nested harness, entrypoint", map[string]string{"T3CODE_THREAD_ID": "t3", "CLAUDE_CODE_SESSION_ID": "cc", "CLAUDE_CODE_ENTRYPOINT": "t3-code"}, 1, "t3", "T3CODE_THREAD_ID", ""},
		{"T3 inside nested harness, no entrypoint", map[string]string{"T3CODE_THREAD_ID": "t3", "CLAUDE_CODE_SESSION_ID": "cc"}, 1, "t3", "T3CODE_THREAD_ID", ""},
		{"nested background with leaked T3 id", map[string]string{"T3CODE_THREAD_ID": "stale", "CLAUDE_CODE_SESSION_ID": "cc", "CLAUDE_CODE_SESSION_KIND": "bg"}, 3, "cc", "CLAUDE_CODE_SESSION_ID", ""},
		{"nested background, entrypoint too", map[string]string{"T3CODE_THREAD_ID": "stale", "CLAUDE_CODE_SESSION_ID": "cc", "CLAUDE_CODE_SESSION_KIND": "bg", "CLAUDE_CODE_ENTRYPOINT": "t3-code"}, 3, "cc", "CLAUDE_CODE_SESSION_ID", ""},
		{"nested alone", map[string]string{"CLAUDE_CODE_SESSION_ID": "cc"}, 3, "cc", "CLAUDE_CODE_SESSION_ID", ""},
		{"nested alone, non-bg kind", map[string]string{"CLAUDE_CODE_SESSION_ID": "cc", "CLAUDE_CODE_SESSION_KIND": "interactive"}, 3, "cc", "CLAUDE_CODE_SESSION_ID", ""},
		{"T3 and OpenCode", map[string]string{"T3CODE_THREAD_ID": "t3", "OPENCODE_SESSION_ID": "oc"}, 0, "", "", "several harness session variables are set (T3CODE_THREAD_ID, OPENCODE_SESSION_ID)"},
		{"OpenCode and yielding nested", map[string]string{"OPENCODE_SESSION_ID": "oc", "CLAUDE_CODE_SESSION_ID": "cc", "CLAUDE_CODE_ENTRYPOINT": "t3-code"}, 2, "oc", "OPENCODE_SESSION_ID", ""},
		{"T3, OpenCode and yielding nested", map[string]string{"T3CODE_THREAD_ID": "t3", "OPENCODE_SESSION_ID": "oc", "CLAUDE_CODE_SESSION_ID": "cc"}, 0, "", "", "several harness session variables are set (T3CODE_THREAD_ID, OPENCODE_SESSION_ID)"},
		{"Codex inside T3", map[string]string{"T3CODE_THREAD_ID": "t3", "CODEX_THREAD_ID": "cx"}, 1, "t3", "T3CODE_THREAD_ID", ""},
		{"Codex alone", map[string]string{"CODEX_THREAD_ID": "cx"}, 4, "cx", "CODEX_THREAD_ID", ""},
		{"Codex and OpenCode", map[string]string{"OPENCODE_SESSION_ID": "oc", "CODEX_THREAD_ID": "cx"}, 0, "", "", "several harness session variables are set (OPENCODE_SESSION_ID, CODEX_THREAD_ID)"},
		{"blank values ignored", map[string]string{"T3CODE_THREAD_ID": " ", "CLAUDE_CODE_SESSION_ID": "cc"}, 3, "cc", "CLAUDE_CODE_SESSION_ID", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := &agenttools.Ops{Client: c, HarnessEnv: func(k string) string { return tc.env[k] }}
			out, err := o.Context(context.Background(), agenttools.CallerArgs{})
			if err != nil {
				t.Fatal(err)
			}
			if tc.conn == 0 {
				if out.Session != nil || !strings.Contains(out.CallerWarning, tc.warning) {
					t.Fatalf("want no caller, warning %q: %+v", tc.warning, out)
				}
				return
			}
			want := db.SessionRef{ConnectionID: tc.conn, ExternalSessionID: tc.id}
			if out.Session == nil || *out.Session != want || out.SessionSource != tc.source {
				t.Fatalf("want %+v from %s: %+v", want, tc.source, out)
			}
		})
	}
}

// Several harnesses owning the same environment is ambiguous; all
// yielding names no caller and warns nothing.
func TestHarnessEnvPrecedenceOwnsAndYieldsAll(t *testing.T) {
	descs := []map[string]any{
		{"kind": "a", "attribution": map[string]any{"metaKeys": []string{}, "env": "A_ID",
			"precedence": harness.Precedence{Owns: []harness.EnvMatch{{Env: "MODE", Value: "x"}}, Yields: []harness.EnvMatch{{Env: "MODE", Value: "y"}}}}},
		{"kind": "b", "attribution": map[string]any{"metaKeys": []string{}, "env": "B_ID",
			"precedence": harness.Precedence{Owns: []harness.EnvMatch{{Env: "MODE", Value: "x"}}, Yields: []harness.EnvMatch{{Env: "MODE", Value: "y"}}}}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/harnesses" {
			t.Errorf("unexpected request %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"harnesses": descs})
	}))
	t.Cleanup(srv.Close)
	for mode, warning := range map[string]string{"x": "several harness session variables are set (A_ID, B_ID); pass the caller explicitly", "y": ""} {
		env := map[string]string{"A_ID": "a", "B_ID": "b", "MODE": mode}
		o := &agenttools.Ops{Client: client.New(srv.URL), HarnessEnv: func(k string) string { return env[k] }}
		out, err := o.Context(context.Background(), agenttools.CallerArgs{})
		if err != nil {
			t.Fatal(err)
		}
		if out.Session != nil || out.CallerWarning != warning {
			t.Fatalf("MODE=%s: want warning %q: %+v", mode, warning, out)
		}
	}
}
