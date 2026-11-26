package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/opencode"
	"github.com/flexdinesh/stage/server/internal/opencode/opencodetest"
	"github.com/flexdinesh/stage/server/internal/opencodesetup"
)

func withOpenCode(t *testing.T) func(*api.Deps) {
	return func(d *api.Deps) {
		local, _ := d.Engine.Connections.Machines().Local(db.LocalMachine)
		ports := machine.NewStorePorts(d.Store.OnMachine(db.LocalMachine), harness.MustRegistry(opencode.Descriptor))
		composed, err := opencodesetup.Module.Compose(t.Context(), harness.Ports{
			Settings: ports.Settings(), Secrets: ports.Secrets(), Identity: ports, Install: local.Install,
			SaveSettingsWithSecret: ports.SaveSettingsWithSecret, Logger: d.Logger,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(composed.Close)
		d.Connectors = mustConnectors(map[string]harness.Connector{opencode.Kind: composed.Connector})
	}
}

// status is GET /api/harnesses/{kind}/status's details object.
func (e *env) status(kind string) (int, map[string]any) {
	e.t.Helper()
	code, body := e.raw("GET", "/api/harnesses/"+kind+"/status", "")
	if d, ok := body["details"].(map[string]any); ok {
		return code, d
	}
	return code, body
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
	e := newEnv(t, withOpenCode(t))

	code, st := e.status("opencode")
	local, _ := st["local"].(map[string]any)
	if code != 200 || st["auth"] != "none" || st["state"] != "unconfigured" || local["reason"] != "missing" || local["available"] != false || local["url"] != "" {
		t.Fatalf("initial status %d %v", code, st)
	}
	if _, ok := st["expiresAt"]; ok {
		t.Fatal("expiresAt without pairing")
	}

	code, body := e.raw("POST", "/api/harnesses/opencode/actions/connect-local", "")
	if code != http.StatusConflict || body["code"] != "opencode_local_unavailable" {
		t.Fatalf("connect-local %d %v", code, body)
	}
	if code, body := e.raw("POST", "/api/harnesses/opencode/actions/pair", `{"link":"http://box/auth/connect/"}`); code != 400 || !strings.Contains(body["error"].(string), "Pairing link") {
		t.Fatalf("bad link %d %v", code, body)
	}
	if code, body := e.raw("PUT", "/api/harnesses/opencode/settings", `{"endpoint":"http://127.0.0.1:49374\u200b"}`); code != 400 || body["error"] != "Service URL contains an invisible character (U+200B); retype it" {
		t.Fatalf("invisible character %d %v", code, body)
	}

	fake := opencodetest.New(t, "server-password")
	link := fake.Link()
	payload, _ := json.Marshal(map[string]string{"link": link})
	if code, body := e.raw("POST", "/api/harnesses/opencode/actions/pair", string(payload)); code != 200 {
		t.Fatalf("pair %d %v", code, body)
	}
	connection := e.store.ActiveID(db.LocalMachine, opencode.Kind)
	var checked execution.ActionResult
	e.must("POST", fmt.Sprintf("/api/connections/%d/actions/check", connection), json.RawMessage(`{}`), http.StatusOK, &checked)
	if checked.Message == "" || len(checked.Facts) == 0 {
		t.Fatalf("check result %+v", checked)
	}
	if _, err := e.engine.Connections.Adapter(connection); err != nil {
		t.Fatalf("adapter not installed: %v", err)
	}
	code, st = e.status("opencode")
	if code != 200 || st["auth"] != "paired" || st["state"] != "connected" || st["endpoint"] != fake.URL || st["hasCredentials"] != true {
		t.Fatalf("paired status %v", st)
	}
	if at, _ := st["expiresAt"].(string); !strings.HasSuffix(at, "Z") || len(at) != len("2026-12-19T00:00:00Z") {
		t.Fatalf("expiresAt %v", st["expiresAt"])
	}
	if code, body := e.raw("POST", "/api/harnesses/opencode/actions/pair", string(payload)); code != 409 || body["code"] != "opencode_pairing_expired" ||
		body["error"] != "This pairing link expired or was already used. Run opencode pair for a new one." {
		t.Fatalf("reused link %d %v", code, body)
	}
	if code, body := e.raw("PUT", "/api/harnesses/opencode/settings", `{"username":"opencode","password":"wrong"}`); code != 409 || body["code"] != "opencode_auth_required" {
		t.Fatalf("rejected credentials %d %v", code, body)
	}

	if code, body := e.raw("DELETE", "/api/harnesses/opencode/connection", ""); code != 200 {
		t.Fatalf("disconnect %d %v", code, body)
	}
	code, st = e.status("opencode")
	if code != 200 || st["auth"] != "none" || st["state"] != "unconfigured" || st["endpoint"] != "" {
		t.Fatalf("after disconnect %v", st)
	}
	if _, err := e.engine.Connections.Adapter(connection); err == nil {
		t.Fatal("adapter still installed")
	}
}
