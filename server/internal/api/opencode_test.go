package api_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/opencode/opencodetest"
	"github.com/flexdinesh/stage/server/internal/opencodesetup"
)

func withOpenCode(t *testing.T, env config.OpenCodeSettings) func(*api.Deps) {
	return func(d *api.Deps) {
		s, err := opencodesetup.New(t.Context(), d.Store, env, d.Sync.SwapAdapter, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatal(err)
		}
		d.OpenCode = s
	}
}

// raw sends a request and returns the status and decoded JSON object body.
func (e *env) raw(method, path string, body string) (int, map[string]any) {
	e.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	out := map[string]any{}
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			e.t.Fatalf("%s %s: %q", method, path, rec.Body.String())
		}
	}
	return rec.Code, out
}

func TestOpenCodeConnectionEndpoints(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	e := newEnv(t, withOpenCode(t, config.OpenCodeSettings{}))

	code, st := e.raw("GET", "/api/opencode/status", "")
	local, _ := st["local"].(map[string]any)
	if code != 200 || st["auth"] != "none" || st["state"] != "unconfigured" || local["reason"] != "missing" || local["available"] != false || local["url"] != "" {
		t.Fatalf("initial status %d %v", code, st)
	}
	if _, ok := st["expiresAt"]; ok {
		t.Fatal("expiresAt without pairing")
	}

	code, body := e.raw("POST", "/api/opencode/connect-local", "")
	if code != http.StatusConflict || body["code"] != "opencode_local_unavailable" {
		t.Fatalf("connect-local %d %v", code, body)
	}
	if code, body := e.raw("POST", "/api/opencode/pair", `{"link":"http://box/auth/connect/"}`); code != 400 || !strings.Contains(body["error"].(string), "Pairing link") {
		t.Fatalf("bad link %d %v", code, body)
	}
	if code, body := e.raw("PUT", "/api/opencode/settings", `{"endpoint":"http://127.0.0.1:49374\u200b"}`); code != 400 || body["error"] != "Service URL contains an invisible character (U+200B); retype it" {
		t.Fatalf("invisible character %d %v", code, body)
	}

	fake := opencodetest.New(t, "server-password")
	link := fake.Link()
	payload, _ := json.Marshal(map[string]string{"link": link})
	if code, body := e.raw("POST", "/api/opencode/pair", string(payload)); code != 204 {
		t.Fatalf("pair %d %v", code, body)
	}
	code, st = e.raw("GET", "/api/opencode/status", "")
	if code != 200 || st["auth"] != "paired" || st["state"] != "connected" || st["endpoint"] != fake.URL || st["hasCredentials"] != true {
		t.Fatalf("paired status %v", st)
	}
	if at, _ := st["expiresAt"].(string); !strings.HasSuffix(at, "Z") || len(at) != len("2026-12-19T00:00:00Z") {
		t.Fatalf("expiresAt %v", st["expiresAt"])
	}
	if code, body := e.raw("POST", "/api/opencode/pair", string(payload)); code != 409 || body["code"] != "opencode_pairing_expired" ||
		body["error"] != "This pairing link expired or was already used. Run opencode pair for a new one." {
		t.Fatalf("reused link %d %v", code, body)
	}
	if code, body := e.raw("PUT", "/api/opencode/settings", `{"username":"opencode","password":"wrong"}`); code != 409 || body["code"] != "opencode_auth_required" {
		t.Fatalf("rejected credentials %d %v", code, body)
	}

	if code, body := e.raw("DELETE", "/api/opencode/settings", ""); code != 204 {
		t.Fatalf("disconnect %d %v", code, body)
	}
	code, st = e.raw("GET", "/api/opencode/status", "")
	if code != 200 || st["auth"] != "none" || st["state"] != "unconfigured" || st["endpoint"] != "" {
		t.Fatalf("after disconnect %v", st)
	}
	if _, err := e.sync.Adapter(e.openCodeID(t)); err == nil {
		t.Fatal("adapter still installed")
	}
}

func (e *env) openCodeID(t *testing.T) int64 {
	id, err := e.store.EnsureOpenCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestOpenCodeEnvironmentManaged(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	e := newEnv(t, withOpenCode(t, config.OpenCodeSettings{Endpoint: "http://127.0.0.1:1"}))
	for _, r := range []struct{ method, path, body string }{
		{"POST", "/api/opencode/connect-local", ""},
		{"POST", "/api/opencode/pair", `{"link":"http://127.0.0.1:1/auth/connect/x"}`},
		{"DELETE", "/api/opencode/settings", ""},
	} {
		if code, body := e.raw(r.method, r.path, r.body); code != 409 || !strings.Contains(body["error"].(string), "managed by the Stage server environment") {
			t.Errorf("%s %s: %d %v", r.method, r.path, code, body)
		}
	}
	_, st := e.raw("GET", "/api/opencode/status", "")
	if local, _ := st["local"].(map[string]any); local["reason"] != "environment" {
		t.Fatalf("%v", st)
	}
}
