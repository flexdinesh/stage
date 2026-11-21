package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/guard"
)

const stagePublic = "https://stage.example.test"

var apiVersion = strconv.Itoa(buildinfo.APIVersion)

type machineOut struct {
	ID          int64   `json:"id"`
	Label       string  `json:"label"`
	Kind        string  `json:"kind"`
	Online      bool    `json:"online"`
	LastSeenAt  *string `json:"lastSeenAt"`
	Version     *string `json:"version"`
	CreatedBy   int64   `json:"createdBy"`
	CreatedAt   string  `json:"createdAt"`
	Connections []struct {
		ID      int64  `json:"id"`
		Harness string `json:"harness"`
		Label   string `json:"label"`
	} `json:"connections"`
}

type pairingOut struct {
	Code      string `json:"code"`
	Label     string `json:"label"`
	ExpiresAt string `json:"expiresAt"`
	Link      string `json:"link"`
	Command   string `json:"command"`
}

type joinOut struct {
	MachineID int64  `json:"machineId"`
	Label     string `json:"label"`
	Token     string `json:"token"`
}

type errOut struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// raw sends a request with headers to h and decodes the JSON body into out.
func raw(t *testing.T, h http.Handler, method, path string, headers map[string]string, body any, out any) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if out != nil && rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("%s %s: decode %q: %v", method, path, rec.Body.String(), err)
		}
	}
	return rec
}

func workerHeaders(token string) map[string]string {
	h := map[string]string{buildinfo.APIVersionHeader: apiVersion}
	if token != "" {
		h["Authorization"] = "Bearer " + token
	}
	return h
}

func withPublicURL(d *api.Deps) { d.Config.PublicURL = stagePublic + "/" }

// join pairs a machine through the API and returns the join response.
func (e *env) join(label string) joinOut {
	e.t.Helper()
	var p pairingOut
	e.must("POST", "/api/machines/pairings", map[string]any{}, 201, &p)
	var j joinOut
	if rec := raw(e.t, e.h, "POST", "/api/worker/join", workerHeaders(""), map[string]any{"code": p.Code, "label": label, "version": "9.9.9"}, &j); rec.Code != 201 {
		e.t.Fatalf("join: %d %s", rec.Code, rec.Body)
	}
	return j
}

func TestMachinesListRenameRemove(t *testing.T) {
	e := newEnv(t, withPublicURL)
	var list struct {
		Machines []machineOut `json:"machines"`
	}
	e.must("GET", "/api/machines", nil, 200, &list)
	ms := list.Machines
	if len(ms) != 1 || ms[0].ID != 1 || ms[0].Kind != "local" || !ms[0].Online || ms[0].Version == nil || *ms[0].Version != buildinfo.Version || ms[0].CreatedBy != 1 {
		t.Fatalf("machines %+v", ms)
	}
	if len(ms[0].Connections) != 1 || ms[0].Connections[0].Harness != "t3" {
		t.Fatalf("local connections %+v", ms[0].Connections)
	}
	j := e.join("laptop")
	var m machineOut
	e.must("GET", fmt.Sprintf("/api/machines/%d", j.MachineID), nil, 200, &m)
	if m.Kind != "remote" || m.Online || m.Label != "laptop" || m.Version == nil || *m.Version != "9.9.9" || m.LastSeenAt == nil || len(m.Connections) != 0 {
		t.Fatalf("remote %+v", m)
	}
	e.must("PATCH", fmt.Sprintf("/api/machines/%d", j.MachineID), map[string]any{"label": "work laptop"}, 200, &m)
	if m.Label != "work laptop" {
		t.Fatalf("renamed %+v", m)
	}
	e.must("PATCH", fmt.Sprintf("/api/machines/%d", j.MachineID), map[string]any{"label": " "}, 400, nil)
	e.must("PATCH", fmt.Sprintf("/api/machines/%d", j.MachineID), map[string]any{"name": "x"}, 400, nil)
	var conflict errOut
	e.must("DELETE", "/api/machines/1", nil, 409, &conflict)
	if conflict.Code != "local_machine" {
		t.Fatalf("remove local %+v", conflict)
	}
	e.must("DELETE", fmt.Sprintf("/api/machines/%d", j.MachineID), nil, 204, nil)
	e.must("GET", fmt.Sprintf("/api/machines/%d", j.MachineID), nil, 404, nil)
	e.must("DELETE", fmt.Sprintf("/api/machines/%d", j.MachineID), nil, 404, nil)
	e.must("GET", "/api/machines", nil, 200, &list)
	if ms = list.Machines; len(ms) != 1 {
		t.Fatalf("after removal %+v", ms)
	}
}

func TestPairingNeedsPublicURL(t *testing.T) {
	e := newEnv(t)
	var out errOut
	e.must("POST", "/api/machines/pairings", map[string]any{}, 409, &out)
	if out.Code != "public_url_required" {
		t.Fatalf("no public URL %+v", out)
	}
}

func TestPairingAndJoin(t *testing.T) {
	e := newEnv(t, withPublicURL)
	var p pairingOut
	rec := raw(t, e.h, "POST", "/api/machines/pairings", nil, map[string]any{"label": "desk"}, &p)
	if rec.Code != 201 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("pairing %d %v", rec.Code, rec.Header())
	}
	if p.Label != "desk" || !strings.HasPrefix(p.Code, "stage_pc_") || p.Link != stagePublic+"/join#"+p.Code || p.Command != "stage worker join "+p.Link || p.ExpiresAt == "" {
		t.Fatalf("pairing %+v", p)
	}
	var j joinOut
	rec = raw(t, e.h, "POST", "/api/worker/join", workerHeaders(""), map[string]any{"code": p.Code, "label": "host", "version": "1"}, &j)
	if rec.Code != 201 || j.Label != "desk" || !strings.HasPrefix(j.Token, "stage_mt_") || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("join %d %+v", rec.Code, j)
	}
	var reuse errOut
	if rec := raw(t, e.h, "POST", "/api/worker/join", workerHeaders(""), map[string]any{"code": p.Code}, &reuse); rec.Code != 401 || reuse.Code != "pairing_code_invalid" {
		t.Fatalf("reuse %d %+v", rec.Code, reuse)
	}
	if rec := raw(t, e.h, "POST", "/api/worker/join", workerHeaders(""), map[string]any{}, &reuse); rec.Code != 401 {
		t.Fatalf("no code %d", rec.Code)
	}
	if rec := raw(t, e.h, "POST", "/api/worker/join", workerHeaders(j.Token), map[string]any{"code": "x"}, nil); rec.Code != 400 {
		t.Fatalf("join with a bearer: %d", rec.Code)
	}
	var who machineOut
	if rec := raw(t, e.h, "GET", "/api/worker/machine", workerHeaders(j.Token), nil, &who); rec.Code != 200 || who.ID != j.MachineID || who.Kind != "remote" {
		t.Fatalf("whoami %d %+v", rec.Code, who)
	}
}

func TestWorkerRoutesNeedVersionAndToken(t *testing.T) {
	e := newEnv(t, withPublicURL)
	j := e.join("a")
	var out errOut
	for name, h := range map[string]map[string]string{
		"no version":    {"Authorization": "Bearer " + j.Token},
		"other version": {"Authorization": "Bearer " + j.Token, buildinfo.APIVersionHeader: "1"},
	} {
		if rec := raw(t, e.h, "GET", "/api/worker/machine", h, nil, &out); rec.Code != 409 || out.Code != "api_version_mismatch" {
			t.Errorf("%s: %d %+v", name, rec.Code, out)
		}
	}
	var p pairingOut
	e.must("POST", "/api/machines/pairings", map[string]any{}, 201, &p)
	if rec := raw(t, e.h, "POST", "/api/worker/join", nil, map[string]any{"code": p.Code}, &out); rec.Code != 409 || out.Code != "api_version_mismatch" {
		t.Fatalf("join without version: %d %+v", rec.Code, out)
	}
	if rec := raw(t, e.h, "POST", "/api/worker/join", workerHeaders(""), map[string]any{"code": p.Code}, nil); rec.Code != 201 {
		t.Fatalf("code survives a refused join: %d", rec.Code)
	}

	rec := raw(t, e.h, "GET", "/api/worker/machine", workerHeaders(""), nil, &out)
	if rec.Code != 401 || out.Code != "machine_token_required" || !strings.HasPrefix(rec.Header().Get("WWW-Authenticate"), "Bearer") {
		t.Fatalf("no token %d %+v %v", rec.Code, out, rec.Header())
	}
	bad := j.Token[:len(j.Token)-1] + "x"
	if strings.HasSuffix(j.Token, "x") {
		bad = j.Token[:len(j.Token)-1] + "y"
	}
	for name, h := range map[string]map[string]string{
		"wrong token":  workerHeaders(bad),
		"basic scheme": {buildinfo.APIVersionHeader: apiVersion, "Authorization": "Basic " + j.Token},
	} {
		if rec := raw(t, e.h, "GET", "/api/worker/machine", h, nil, &out); rec.Code != 401 {
			t.Errorf("%s: %d %+v", name, rec.Code, out)
		}
	}
	if rec := raw(t, e.h, "GET", "/api/worker/machine", map[string]string{buildinfo.APIVersionHeader: apiVersion, "Authorization": "bearer " + j.Token}, nil, nil); rec.Code != 200 {
		t.Fatalf("lowercase scheme: %d", rec.Code)
	}
	if rec := raw(t, e.h, "GET", "/api/worker/nope", workerHeaders(""), nil, nil); rec.Code != 401 {
		t.Fatalf("unknown worker route without token: %d", rec.Code)
	}
	if rec := raw(t, e.h, "GET", "/api/worker/nope", workerHeaders(j.Token), nil, nil); rec.Code != 404 {
		t.Fatalf("unknown worker route: %d", rec.Code)
	}

	// Removing the machine revokes its token at once.
	e.must("DELETE", fmt.Sprintf("/api/machines/%d", j.MachineID), nil, 204, nil)
	rec = raw(t, e.h, "GET", "/api/worker/machine", workerHeaders(j.Token), nil, &out)
	if rec.Code != 401 || out.Code != "machine_token_invalid" {
		t.Fatalf("revoked %d %+v", rec.Code, out)
	}
}

func TestMachineTokenRefusedOffWorkerRoutes(t *testing.T) {
	e := newEnv(t, withPublicURL)
	j := e.join("a")
	var out errOut
	for _, path := range []string{"/api/tasks", "/api/machines", "/api/health"} {
		if rec := raw(t, e.h, "GET", path, map[string]string{"Authorization": "Bearer " + j.Token}, nil, &out); rec.Code != 401 || out.Code != "machine_token_not_accepted" {
			t.Errorf("%s: %d %+v", path, rec.Code, out)
		}
	}
	// A proxy's own credentials pass through untouched.
	if rec := raw(t, e.h, "GET", "/api/tasks", map[string]string{"Authorization": "Bearer proxy-jwt"}, nil, nil); rec.Code != 200 {
		t.Fatalf("other bearer: %d", rec.Code)
	}
	// /mcp wraps the same check (serve.go); the wrapper alone refuses it.
	mcp := api.RejectMachineTokens(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(299) }))
	if rec := raw(t, mcp, "POST", "/mcp", map[string]string{"Authorization": "Bearer " + j.Token}, map[string]any{}, nil); rec.Code != 401 {
		t.Fatalf("/mcp with a machine token: %d", rec.Code)
	}
	if rec := raw(t, mcp, "POST", "/mcp", nil, map[string]any{}, nil); rec.Code != 299 {
		t.Fatalf("/mcp without: %d", rec.Code)
	}
}

// Worker routes stay behind guard.Policy: a worker reaches the public host
// with no Origin and a JSON body, so it passes; a rebinding Host or a
// cross-site browser request does not.
func TestWorkerRoutesBehindRequestOriginPolicy(t *testing.T) {
	e := newEnv(t, withPublicURL)
	policy, err := guard.New([]string{stagePublic}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h := policy.Wrap(e.h)
	var p pairingOut
	e.must("POST", "/api/machines/pairings", map[string]any{}, 201, &p)
	send := func(host string, extra map[string]string) int {
		req := httptest.NewRequest("POST", "/api/worker/join", strings.NewReader(`{"code":"`+p.Code+`"}`))
		req.Host = host
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(buildinfo.APIVersionHeader, apiVersion)
		for k, v := range extra {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := send("evil.example", nil); got != 403 {
		t.Fatalf("rebinding host: %d", got)
	}
	if got := send("stage.example.test", map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}); got != 403 {
		t.Fatalf("cross-site browser: %d", got)
	}
	if got := send("stage.example.test", nil); got != 201 {
		t.Fatalf("worker: %d", got)
	}
}
