package api_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type sourced struct {
	ID              int64  `json:"id"`
	SourceMachineID *int64 `json:"sourceMachineId"`
	CreatedBy       int64  `json:"createdBy"`
	SourceSession   *struct {
		ConnectionID      int64  `json:"connectionId"`
		ExternalSessionID string `json:"externalSessionId"`
	} `json:"sourceSession"`
}

// gatewayEnv is an API env whose gateway route also serves MCP, wired as
// serve.go does (the MCP handler serves through the router).
func gatewayEnv(t *testing.T) *env {
	t.Helper()
	var mcpHandler http.Handler
	e := newEnv(t, withPublicURL, func(d *api.Deps) {
		d.MCP = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { mcpHandler.ServeHTTP(w, r) })
	})
	mcpHandler = agenttools.HTTPHandler(&agenttools.Ops{Client: client.NewInProcess(e.h, "http://stage")})
	return e
}

// gw sends a request through the gateway route with token.
func (e *env) gw(token, method, path string, body any, out any) *httptest.ResponseRecorder {
	e.t.Helper()
	return raw(e.t, e.h, method, api.GatewayPrefix+path, workerHeaders(token), body, out)
}

func machineIs(got *int64, want int64) bool { return got != nil && *got == want }

func TestWorkerGatewayRecordsForwardingMachine(t *testing.T) {
	e := gatewayEnv(t)
	j := e.join("laptop")
	p := e.project("/w/gw")

	var created sourced
	if rec := e.gw(j.Token, "POST", "/api/tasks", e.taskBody(p.ID, "via gateway", map[string]any{"prompt": "p"}), &created); rec.Code != 201 {
		t.Fatalf("create task: %d %s", rec.Code, rec.Body)
	}
	if !machineIs(created.SourceMachineID, j.MachineID) || created.CreatedBy != 1 {
		t.Fatalf("created task %+v, want source machine %d and the default user", created, j.MachineID)
	}
	var note sourced
	body := map[string]any{"text": "from the laptop", "requestId": "gw-1", "sourceSession": map[string]any{"connectionId": 1, "externalSessionId": "agent-1"}}
	if rec := e.gw(j.Token, "POST", fmt.Sprintf("/api/tasks/%d/journal", created.ID), body, &note); rec.Code != 201 {
		t.Fatalf("append note: %d %s", rec.Code, rec.Body)
	}
	if !machineIs(note.SourceMachineID, j.MachineID) || note.SourceSession == nil || note.SourceSession.ExternalSessionID != "agent-1" {
		t.Fatalf("note %+v", note)
	}
	var noteShape map[string]any
	e.must("GET", fmt.Sprintf("/api/journal/%d", note.ID), nil, 200, &noteShape)
	if _, exists := noteShape["sourceThreadId"]; exists {
		t.Fatal("journal entry still has sourceThreadId")
	}
	// Reads through either path show the stored machine.
	var got sourced
	e.must("GET", fmt.Sprintf("/api/tasks/%d", created.ID), nil, 200, &got)
	if !machineIs(got.SourceMachineID, j.MachineID) {
		t.Fatalf("direct read %+v", got)
	}
	// Direct writes have none.
	direct := e.task(p.ID, "direct", nil)
	e.must("GET", fmt.Sprintf("/api/tasks/%d", direct.ID), nil, 200, &got)
	if got.SourceMachineID != nil {
		t.Fatalf("direct task sourceMachineId %d", *got.SourceMachineID)
	}
	var directNote sourced
	e.must("POST", fmt.Sprintf("/api/tasks/%d/journal", direct.ID), map[string]any{"text": "here", "requestId": "d-1"}, 201, &directNote)
	if directNote.SourceMachineID != nil {
		t.Fatalf("direct note %+v", directNote)
	}
}

func TestWorkerGatewayScope(t *testing.T) {
	e := gatewayEnv(t)
	j := e.join("laptop")
	other := e.join("desktop")
	var out errOut
	// The worker route's own checks come first.
	if rec := raw(t, e.h, "GET", api.GatewayPrefix+"/api/tasks", workerHeaders(""), nil, &out); rec.Code != 401 || out.Code != "machine_token_required" {
		t.Fatalf("no token: %d %+v", rec.Code, out)
	}
	if rec := raw(t, e.h, "GET", api.GatewayPrefix+"/api/tasks", map[string]string{"Authorization": "Bearer " + j.Token}, nil, &out); rec.Code != 409 || out.Code != "api_version_mismatch" {
		t.Fatalf("no version: %d %+v", rec.Code, out)
	}
	if rec := e.gw(j.Token, "GET", "/api/machines", nil, nil); rec.Code != 200 {
		t.Fatalf("read machines: %d", rec.Code)
	}
	var actions []execution.Action
	if rec := e.gw(j.Token, "GET", "/api/connections/1/actions", nil, &actions); rec.Code != 200 || len(actions) != 0 {
		t.Fatalf("action discovery through gateway: %d %s", rec.Code, rec.Body)
	}
	if rec := e.gw(j.Token, "POST", "/api/connections/1/actions/pair", map[string]string{"code": "fake"}, &out); rec.Code != 403 || out.Code != "action_forbidden" {
		t.Fatalf("setup action through gateway: %d %s", rec.Code, rec.Body)
	}
	// Machine management is refused.
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/machines/pairings"},
		{"DELETE", "/api/machines/pairings/1"},
		{"PATCH", fmt.Sprintf("/api/machines/%d", other.MachineID)},
		{"DELETE", fmt.Sprintf("/api/machines/%d", other.MachineID)},
		{"POST", fmt.Sprintf("/api/machines/%d/ping", other.MachineID)},
		{"GET", "/api/connections/1/workspaces"},
		{"GET", "/api/workspaces/review"},
		{"POST", "/api/connections/1/resolve"},
		{"POST", "/api/sync"},
	} {
		out = errOut{}
		if rec := e.gw(j.Token, c.method, c.path, map[string]any{"label": "x"}, &out); rec.Code != 403 || out.Code != "gateway_forbidden" {
			t.Errorf("%s %s: %d %+v", c.method, c.path, rec.Code, out)
		}
	}
	// Worker routes and anything but /api and /mcp are not forwarded.
	for _, path := range []string{"/api/worker/machine", "/api/worker/gateway/api/tasks", "/", "/health", "/mcpx"} {
		if rec := e.gw(j.Token, "GET", path, nil, nil); rec.Code != 404 {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	// A removed machine's gateway is refused at once.
	e.must("DELETE", fmt.Sprintf("/api/machines/%d", j.MachineID), nil, 204, nil)
	out = errOut{}
	if rec := e.gw(j.Token, "GET", "/api/tasks", nil, &out); rec.Code != 401 || out.Code != "machine_token_invalid" {
		t.Fatalf("removed: %d %+v", rec.Code, out)
	}
}

// gatewayTransport is what a worker's gateway adds: the machine token and
// the API version, on the main server's public Host. The caller header
// passes through.
type gatewayTransport struct{ token, caller string }

func (g gatewayTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Host = "stage.example.test" // a public Host on a loopback listener
	r.Header.Set("Authorization", "Bearer "+g.token)
	r.Header.Set("Stage-Api-Version", apiVersion)
	r.Header.Set(agentctx.CallerHeader, g.caller)
	return http.DefaultTransport.RoundTrip(r)
}

func TestWorkerGatewayMCPKeepsCallerAndMachine(t *testing.T) {
	e := gatewayEnv(t)
	j := e.join("laptop")
	p := e.project("/w/mcp")
	tk := e.task(p.ID, "mcp", nil)
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	tr := &mcp.StreamableClientTransport{
		Endpoint:   srv.URL + api.GatewayPrefix + "/mcp",
		HTTPClient: &http.Client{Transport: gatewayTransport{token: j.Token, caller: "1:agent-mcp"}},
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "gateway-test", Version: "1"}, nil).Connect(t.Context(), tr, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "append_note", Arguments: map[string]any{"taskId": tk.ID, "text": "over mcp", "requestId": "mcp-1"}})
	if err != nil || res.IsError {
		t.Fatalf("append_note: %v %+v", err, res)
	}
	var page struct {
		Entries []sourced `json:"entries"`
	}
	e.must("GET", fmt.Sprintf("/api/tasks/%d/journal?kind=notes", tk.ID), nil, 200, &page)
	if len(page.Entries) != 1 {
		t.Fatalf("entries %+v", page.Entries)
	}
	n := page.Entries[0]
	if !machineIs(n.SourceMachineID, j.MachineID) || n.SourceSession == nil || n.SourceSession.ConnectionID != 1 || n.SourceSession.ExternalSessionID != "agent-mcp" {
		t.Fatalf("mcp note %+v (source session %+v)", n, n.SourceSession)
	}
	if strings.Contains(fmt.Sprint(res.Content), "machine_token") {
		t.Fatalf("token error leaked: %+v", res.Content)
	}
}

// GET /api/connections/resolve names the harness's active connection on the
// caller's machine: the server's own for a direct request, the forwarding
// machine's through a gateway (which has none here).
func TestResolveConnectionUsesCallerMachine(t *testing.T) {
	e := gatewayEnv(t)
	j := e.join("laptop")
	var local struct {
		ID        int64  `json:"id"`
		MachineID int64  `json:"machineId"`
		Harness   string `json:"harness"`
	}
	e.must("GET", "/api/connections/resolve?harness=t3", nil, 200, &local)
	if local.ID != e.store.ActiveID(db.LocalMachine, "t3") || local.MachineID != 1 || local.Harness != "t3" {
		t.Fatalf("local resolve %+v", local)
	}
	var out errOut
	if rec := e.gw(j.Token, "GET", "/api/connections/resolve?harness=t3", nil, &out); rec.Code != 404 || out.Code != "no_active_connection" {
		t.Fatalf("gateway resolve: %d %+v", rec.Code, out)
	}
	e.must("GET", "/api/connections/resolve?harness=nope", nil, 400, nil)
}
