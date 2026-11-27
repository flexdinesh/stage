package serve_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/http/httputil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/cli"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/sqlite"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/listen"
	"github.com/flexdinesh/stage/server/internal/serve"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/webui"
	"github.com/flexdinesh/stage/server/internal/worker"
	"github.com/flexdinesh/stage/server/internal/workerchan"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type transportRequest struct {
	method, path string
	origin, host string
	headers      http.Header
	body         string
}

func (req transportRequest) send(t *testing.T, h *http.Client, base string, want int, out any) http.Header {
	t.Helper()
	r, err := http.NewRequestWithContext(t.Context(), req.method, base+req.path, strings.NewReader(req.body))
	if err != nil {
		t.Fatal(err)
	}
	if req.headers != nil {
		r.Header = req.headers.Clone()
	}
	if req.origin != "" {
		r.Header.Set("Origin", req.origin)
	}
	if req.host != "" {
		r.Host = req.host
	}
	if req.body != "" && r.Header.Get("Content-Type") == "" {
		r.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.Do(r)
	if err != nil {
		t.Fatalf("%s %s: %v", req.method, req.path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != want {
		var errBody struct{ Code string }
		_ = json.Unmarshal(data, &errBody)
		t.Fatalf("%s %s: status %d (%s), want %d, read err %v", req.method, req.path, resp.StatusCode, errBody.Code, want, err)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("%s %s: decode: %v", req.method, req.path, err)
		}
	}
	return resp.Header
}

func proxyFor(t *testing.T, upstream string) *httptest.Server {
	t.Helper()
	p := httptest.NewTLSServer(&httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		r.Out.URL.Scheme, r.Out.URL.Host = "http", upstream
		r.Out.Host = r.In.Host
		r.Out.Header.Set("X-Forwarded-Proto", "http") // never the source of browser trust
	}})
	t.Cleanup(p.Close)
	p.Client().CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return p
}

func browserClient(t *testing.T, proxy *httptest.Server) *http.Client {
	t.Helper()
	h := *proxy.Client()
	var err error
	h.Jar, err = cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &h
}

func browserPost(t *testing.T, h *http.Client, base, path, body string, want int, out any) http.Header {
	t.Helper()
	return (transportRequest{method: "POST", path: path, origin: base, body: body}).send(t, h, base, want, out)
}

func claimBrowser(t *testing.T, browser *http.Client, base, output string) http.Header {
	t.Helper()
	token := linkToken(t, setupLink(t, output, base, "/auth/setup"))
	payload := fmt.Sprintf(`{"token":%q,"email":"owner@example.test","name":"Owner","password":"spindrift persimmon telescope"}`, token)
	browserPost(t, browser, base, "/api/auth/setup/inspect", fmt.Sprintf(`{"token":%q}`, token), 200, nil)
	return browserPost(t, browser, base, "/api/auth/setup", payload, 200, nil)
}

func TestSharedAccountsThroughHTTPSProxyAndUnix(t *testing.T) {
	short := isolate(t)
	upstream := freePort(t)
	proxy := proxyFor(t, upstream)
	origin := proxy.URL
	c := cfg(t, filepath.Join(short, "data"), upstream, "unix:"+filepath.Join(short, "product.sock"))
	c.PublicURL = origin
	c.AllowedOrigins = []string{"https://other.example"}
	fake := newT3()
	personal := start(t, c, fake)
	task := startTask(t, fake, personal.ready.Listeners[1])
	stdio := connectStdio(t, personal.ready.Listeners[1].URL())
	entry, err := appendMCPNote(t, stdio, task.ID, "Personal stdio")
	if err != nil || entry.Actor.Kind != "agent" || entry.Actor.UserID != 0 {
		t.Fatalf("personal stdio actor %+v: %v", entry.Actor, err)
	}
	if err := stdio.Close(); err != nil {
		t.Fatal(err)
	}
	if err := personal.stop(t); err != nil {
		t.Fatal(err)
	}
	output := &syncBuffer{}
	on := true
	i, _ := startSharedOwner(t, serve.Options{Config: c, SharedAccess: &on, BootstrapOutput: output, HarnessOverrides: map[string]any{t3.Kind: fake}})
	unix := clientFor(i.ready.Listeners[1])
	anon, owner, member := proxy.Client(), browserClient(t, proxy), browserClient(t, proxy)
	get := func(h *http.Client, path string, want int, out any) {
		t.Helper()
		(transportRequest{method: "GET", path: path}).send(t, h, origin, want, out)
	}
	var health map[string]any
	get(anon, "/api/health", 200, &health)
	for _, key := range []string{"harnesses", "publicUrl", "instanceId", "engine", "commit", "identity", "schema"} {
		if _, ok := health[key]; ok {
			t.Errorf("public health contains %s", key)
		}
	}
	if health["setupPending"] != true || health["apiVersion"] != float64(buildinfo.APIVersion) {
		t.Fatalf("pending health omitted readiness information: %+v", health)
	}
	status, err := unix.AccessStatus(t.Context())
	if err != nil || status.Mode != "setup_pending" || status.InstallationID == "" {
		t.Fatalf("setup state %+v: %v", status, err)
	}
	static := httptest.NewRecorder()
	webui.Handler().ServeHTTP(static, httptest.NewRequest("GET", "/", nil))
	get(anon, "/", static.Code, nil)
	get(anon, "/api/tasks", 403, nil)
	get(anon, "/mcp", 403, nil)
	get(anon, "/api/attachments/1", 403, nil)
	(transportRequest{method: "POST", path: "/api/auth/setup", body: "{}", headers: http.Header{
		"X-Forwarded-Host": {strings.TrimPrefix(origin, "https://")}, "X-Forwarded-Proto": {"https"},
	}}).send(t, anon, origin, 403, nil)

	h := claimBrowser(t, owner, origin, output.String())
	cookies := (&http.Response{Header: h}).Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode ||
		cookies[0].Domain != "" || cookies[0].Path != "/" || h.Get("Cache-Control") != "no-store" {
		t.Fatal("browser cookie or cache policy is incorrect")
	}
	status, err = unix.AccessStatus(t.Context())
	if err != nil || status.Mode != "claimed" || status.InstallationID == "" {
		t.Fatalf("claimed state %+v: %v", status, err)
	}
	if _, err := unix.Projects(t.Context()); !client.IsStatus(err, 401) {
		t.Fatalf("Unix granted implicit human access: %v", err)
	}
	socketSession, err := unix.Login(t.Context(), status.InstallationID, "owner@example.test", "spindrift persimmon telescope")
	if err != nil {
		t.Fatalf("CLI login via the product socket: %v", err)
	}
	socketHuman := unix.WithCredential(func() (client.Credential, error) { return socketSession, nil })
	if account, err := socketHuman.Me(t.Context()); err != nil || account.Role != "admin" {
		t.Fatalf("CLI session via the product socket: %+v %v", account, err)
	}
	if err := socketHuman.Logout(t.Context()); err != nil {
		t.Fatalf("CLI logout via the product socket: %v", err)
	}
	get(anon, "/api/tasks", 401, nil)
	var details struct{ Harnesses []any }
	get(owner, "/api/health", 200, &details)
	if details.Harnesses == nil {
		t.Fatal("authenticated health lost harness details")
	}
	(transportRequest{method: "POST", path: "/api/auth/login", origin: "https://other.example", body: "{}"}).send(t, anon, origin, 403, nil)
	(transportRequest{method: "POST", path: "/api/auth/logout", origin: "https://other.example", body: "{}"}).send(t, owner, origin, 403, nil)
	(transportRequest{method: "POST", path: "/api/auth/logout", body: "{}"}).send(t, owner, origin, 403, nil)
	(transportRequest{method: "POST", path: "/api/auth/logout", origin: origin, host: strings.TrimPrefix(origin, "https://") + "0", body: "{}"}).send(t, owner, origin, 403, nil)

	var invite struct{ URL string }
	browserPost(t, owner, origin, "/api/admin/invitations", `{"email":"member@example.test"}`, 201, &invite)
	token := linkToken(t, invite.URL)
	browserPost(t, member, origin, "/api/auth/reset", fmt.Sprintf(`{"token":%q,"password":"starfish collides tonight"}`, token), 403, nil)
	browserPost(t, member, origin, "/api/auth/invite", fmt.Sprintf(`{"token":%q,"name":"Member","password":"starfish collides tonight"}`, token), 200, nil)
	var admin, standard struct {
		ID   int64
		Role string
	}
	get(owner, "/api/me", 200, &admin)
	get(member, "/api/me", 200, &standard)
	if admin.ID != 1 || admin.Role != "admin" || standard.ID == admin.ID || standard.Role != "member" {
		t.Fatalf("accounts: %+v %+v", admin, standard)
	}
	get(member, "/api/admin/accounts", 403, nil)
	get(member, "/api/harnesses", 200, nil)
	get(member, "/api/machines", 200, nil)
	browserPost(t, member, origin, "/api/machines/pairings", `{"label":"Member worker"}`, 201, nil)
	get(anon, "/api/notifications/stream", 401, nil)
	get(member, "/api/push/config", 200, nil)
	(transportRequest{method: "PUT", path: "/api/preferences/default-connection", origin: origin, body: `{"connectionId":null}`}).send(t, member, origin, 200, nil)
	var created struct{ ID int64 }
	browserPost(t, member, origin, "/api/projects", `{"title":"Workspace project"}`, 201, &created)
	get(owner, fmt.Sprintf("/api/projects/%d", created.ID), 200, nil)
	rolePath := fmt.Sprintf("/api/admin/accounts/%d/role", standard.ID)
	role := func(value string) {
		(transportRequest{method: "PATCH", path: rolePath, origin: origin, body: fmt.Sprintf(`{"role":%q}`, value)}).send(t, owner, origin, 204, nil)
	}
	role("admin")
	get(member, "/api/admin/accounts", 200, nil)
	role("member")
	get(member, "/api/admin/accounts", 403, nil)

	var native struct{ Token, InstallationID string }
	(transportRequest{method: "POST", path: "/api/auth/cli/login", origin: origin, body: "{}"}).send(t, anon, origin, 403, nil)
	(transportRequest{method: "POST", path: "/api/auth/cli/login", body: `{"email":"member@example.test","password":"starfish collides tonight"}`}).send(t, anon, origin, 200, &native)
	if native.InstallationID != status.InstallationID || native.Token == "" {
		t.Fatal("CLI session lacks installation binding")
	}
	cliHeaders := http.Header{"Authorization": {"Bearer " + native.Token}}
	(transportRequest{method: "GET", path: "/api/me", headers: cliHeaders}).send(t, unix.HTTP, "http://stage", 200, nil)
	taskPath := fmt.Sprintf("/api/tasks/%d", task.ID)
	(transportRequest{method: "PATCH", path: taskPath, origin: origin, body: `{"title":"Owner edit"}`}).send(t, owner, origin, 200, nil)
	editHeaders := cliHeaders.Clone()
	editHeaders.Set(agentctx.CallerHeader, "1:guessed")
	(transportRequest{method: "PATCH", path: taskPath, headers: editHeaders, body: `{"title":"Member edit"}`}).send(t, unix.HTTP, "http://stage", 200, nil)
	var edited client.TaskDetail
	get(owner, taskPath, 200, &edited)
	var edits []store.Actor
	for _, event := range edited.Events {
		if event.Detail == "Task edited" {
			edits = append(edits, event.Actor)
		}
	}
	if len(edits) != 2 || edits[0].Kind != "human" || edits[0].UserID != admin.ID ||
		edits[1].Kind != "human" || edits[1].UserID != standard.ID || edits[1].DisplayName != "Member" ||
		edited.Events[0].Actor.Kind != "personal" || edited.Events[0].Actor.UserID != 0 {
		t.Fatalf("task actors: edits %+v, created %+v", edits, edited.Events[0].Actor)
	}
	(transportRequest{method: "GET", path: "/api/admin/accounts", headers: cliHeaders}).send(t, anon, origin, 403, nil)
	role("admin")
	(transportRequest{method: "GET", path: "/api/admin/accounts", headers: cliHeaders}).send(t, anon, origin, 200, nil)
	role("member")
	(transportRequest{method: "GET", path: "/mcp", headers: cliHeaders}).send(t, anon, origin, 403, nil)
	(transportRequest{method: "GET", path: "/api/me", headers: http.Header{"Cookie": {"stage_session=" + native.Token}}}).send(t, anon, origin, 401, nil)
	(transportRequest{method: "GET", path: "/api/me", headers: http.Header{"Authorization": {"Bearer " + cookies[0].Value}}}).send(t, anon, origin, 401, nil)

	issueAgent := func(h *http.Client) string {
		t.Helper()
		var credential struct{ Token string }
		browserPost(t, h, origin, "/api/account/agent-credential", "", 200, &credential)
		return credential.Token
	}
	ownerAgent, memberAgent := issueAgent(owner), issueAgent(member)
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	upload := client.New(origin)
	uploadHTTP := *anon
	uploadHTTP.Transport = credentialTransport{anon.Transport, memberAgent}
	upload.HTTP = &uploadHTTP
	image, err := upload.Upload(t.Context(), "work.png", imageBytes.Bytes())
	if err != nil {
		t.Fatalf("authenticated image upload: %v", err)
	}
	imageRequest := transportRequest{method: "GET", path: fmt.Sprintf("/api/attachments/%d", image.ID)}
	imageRequest.send(t, anon, origin, 401, nil)
	if h := imageRequest.send(t, owner, origin, 200, nil); h.Get("Content-Type") != "image/png" ||
		!strings.Contains(h.Get("Cache-Control"), "no-store") {
		t.Fatalf("authenticated attachment policy: %v", h)
	}
	agentHeaders := http.Header{"Authorization": {"Bearer " + memberAgent}}
	(transportRequest{method: "GET", path: "/api/projects", headers: agentHeaders}).send(t, unix.HTTP, "http://stage", 200, nil)
	(transportRequest{method: "GET", path: "/api/me", headers: agentHeaders}).send(t, anon, origin, 403, nil)
	(transportRequest{method: "GET", path: "/api/notifications/stream", headers: agentHeaders}).send(t, anon, origin, 403, nil)
	(transportRequest{method: "GET", path: "/api/push/config", headers: cliHeaders}).send(t, anon, origin, 403, nil)
	(transportRequest{method: "POST", path: "/api/tasks/1/requests/1", headers: agentHeaders}).send(t, anon, origin, 403, nil)
	(transportRequest{method: "PUT", path: "/api/harnesses/t3/settings", headers: agentHeaders}).send(t, anon, origin, 403, nil)
	(transportRequest{method: "PUT", path: "/api/machines/1/harnesses/t3/settings", headers: agentHeaders}).send(t, anon, origin, 403, nil)
	claims := http.Header{agentctx.CallerHeader: {"1:guessed"}, "Stage-Action-Agent": {"1"}}
	(transportRequest{method: "GET", path: "/api/projects", headers: claims}).send(t, anon, origin, 401, nil)
	(transportRequest{method: "POST", path: "/mcp", headers: claims, body: "{}"}).send(t, anon, origin, 401, nil)
	assertMCP(t, anon, origin+"/mcp", ownerAgent, task.ID, "Owner MCP", admin.ID, 0)
	assertMCP(t, anon, origin+"/mcp", memberAgent, task.ID, "Member MCP", standard.ID, 0)
	waiting := waitMCP(t, anon, origin+"/mcp", memberAgent, task.ID)
	_ = issueAgent(member)
	select {
	case err := <-waiting:
		if err == nil {
			t.Fatal("a revoked member credential completed an open MCP call")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("revoked member's MCP stream did not end")
	}
	(transportRequest{method: "GET", path: "/api/projects", headers: agentHeaders}).send(t, anon, origin, 401, nil)
	get(member, "/api/me", 200, nil)
	browserPost(t, owner, origin, fmt.Sprintf("/api/admin/accounts/%d/disable", standard.ID), "", 204, nil)
	get(member, "/api/me", 401, nil)
	imageRequest.send(t, member, origin, 401, nil)
	(transportRequest{method: "GET", path: "/api/me", headers: cliHeaders}).send(t, anon, origin, 401, nil)
	get(owner, "/api/me", 200, nil)
	get(owner, taskPath, 200, &edited)
	if edited.State != db.StateRunning || edited.Title != "Member edit" {
		t.Fatalf("disabling a member affected shared work: %s %q", edited.State, edited.Title)
	}
	browserPost(t, owner, origin, "/api/auth/logout", "", 204, nil)
	get(owner, "/api/me", 401, nil)
	browserPost(t, owner, origin, "/api/auth/login", `{"email":"owner@example.test","password":"spindrift persimmon telescope"}`, 200, nil)
	get(owner, "/api/me", 200, nil)

	assertLegacyClients(t, i.ready.Listeners[1].URL())
}

type credentialTransport struct {
	http.RoundTripper
	token string
}

func (c credentialTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+c.token)
	return c.RoundTripper.RoundTrip(r)
}

func connectMCP(t *testing.T, h *http.Client, endpoint, token string) *mcp.ClientSession {
	t.Helper()
	httpClient := *h
	if token != "" {
		httpClient.Transport = credentialTransport{h.Transport, token}
	}
	m := mcp.NewClient(&mcp.Implementation{Name: "shared transport test", Version: "1"}, nil)
	s, err := m.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: &httpClient, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatalf("open authenticated MCP: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func assertMCP(t *testing.T, h *http.Client, endpoint, token string, taskID int64, note string, creator, machine int64) {
	t.Helper()
	s := connectMCP(t, h, endpoint, token)
	entry, err := appendMCPNote(t, s, taskID, note)
	if err != nil || entry.CreatedBy != creator || (entry.SourceMachineID == nil) != (machine == 0) ||
		(entry.SourceMachineID != nil && *entry.SourceMachineID != machine) {
		t.Fatalf("MCP creator/machine %+v, want %d/%d: %v", entry, creator, machine, err)
	}
	wantAccount := creator
	if machine != 0 {
		wantAccount = 0
	}
	if a := entry.Actor; a.Kind != "agent" || a.UserID != 0 || a.InitiatorUserID != 0 || a.CredentialAccountID != wantAccount || a.DisplayName != "" {
		t.Fatalf("MCP actor %+v, want agent with account %d", a, wantAccount)
	}
}

func appendMCPNote(t *testing.T, s *mcp.ClientSession, taskID int64, note string) (db.JournalEntry, error) {
	t.Helper()
	out, err := s.CallTool(t.Context(), &mcp.CallToolParams{Name: "append_note", Arguments: map[string]any{
		"taskId": taskID, "text": note, "requestId": note,
	}})
	if err != nil || out.IsError {
		return db.JournalEntry{}, fmt.Errorf("MCP note failed: %+v %v", out, err)
	}
	data, err := json.Marshal(out.StructuredContent)
	if err != nil {
		return db.JournalEntry{}, err
	}
	var entry db.JournalEntry
	err = json.Unmarshal(data, &entry)
	return entry, err
}

func waitMCP(t *testing.T, h *http.Client, endpoint, token string, taskID int64) <-chan error {
	t.Helper()
	s := connectMCP(t, h, endpoint, token)
	done := make(chan error, 1)
	go func() {
		result, err := s.CallTool(t.Context(), &mcp.CallToolParams{Name: "wait_task", Arguments: map[string]any{
			"id": taskID, "until": []string{"backlog"}, "timeoutSeconds": 30,
		}})
		if err == nil && result.IsError {
			err = fmt.Errorf("MCP wait interrupted")
		}
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("MCP wait ended before revocation: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	return done
}

func assertLegacyClients(t *testing.T, socket string) {
	t.Helper()
	var stderr, stdout bytes.Buffer
	env := cli.OSEnv()
	env.Stderr, env.Stdout = &stderr, &stdout
	if code := cli.Run(t.Context(), []string{"projects", "ls", "--url", socket}, env); code == 0 || !strings.Contains(stderr.String(), "Sign in to continue") {
		t.Fatalf("legacy CLI: exit %d, stderr %s", code, &stderr)
	}
	stdout.Reset()
	if code := cli.Run(t.Context(), []string{"status", "--url", socket}, env); code != 0 || !strings.Contains(stdout.String(), "Sign in to view harness details") {
		t.Fatalf("CLI readiness: exit %d, stdout %s", code, &stdout)
	}
	session := connectStdio(t, socket)
	defer session.Close()
	out, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "list_projects"})
	if err != nil || !out.IsError || !strings.Contains(mcpText(out), "Sign in to continue") {
		t.Fatalf("legacy stdio MCP: %+v %v", out, err)
	}
}

func connectStdio(t *testing.T, socket string) *mcp.ClientSession {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "mcp", "--url", socket)
	cmd.Env = append(os.Environ(), "SERVE_TEST_STAGE_CLI=1", "STAGE_CALLER_SESSION=1:legacy")
	m := mcp.NewClient(&mcp.Implementation{Name: "stdio transport test", Version: "1"}, nil)
	s, err := m.Connect(t.Context(), &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mcpText(result *mcp.CallToolResult) string {
	var out strings.Builder
	for _, item := range result.Content {
		if text, ok := item.(*mcp.TextContent); ok {
			out.WriteString(text.Text)
		}
	}
	return out.String()
}

func TestSharedWorkerReadinessAndPrivateGateway(t *testing.T) {
	short := isolate(t)
	upstream := freePort(t)
	proxy := proxyFor(t, upstream)
	c := cfg(t, filepath.Join(short, "data"), upstream)
	c.PublicURL = proxy.URL
	fake := newT3()
	personal := start(t, c, fake)
	task := startTask(t, fake, personal.ready.Listeners[0])
	var pair struct{ Link string }
	browserPost(t, proxy.Client(), proxy.URL, "/api/machines/pairings", `{"label":"worker"}`, 201, &pair)
	workerConf := filepath.Join(short, "worker", "worker.json")
	joined, err := worker.Join(t.Context(), worker.JoinOptions{Link: pair.Link, ConfigPath: workerConf, HTTP: proxy.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if err := personal.stop(t); err != nil {
		t.Fatal(err)
	}
	on := true
	output := &syncBuffer{}
	startSharedOwner(t, serve.Options{Config: c, SharedAccess: &on, BootstrapOutput: output, HarnessOverrides: map[string]any{t3.Kind: fake}})
	private := listen.Addr{Network: "unix", Address: filepath.Join(short, "worker", "gateway.sock"), Private: true}
	tcp := listen.Addr{Network: "tcp", Address: freePort(t)}
	ctx, cancel := context.WithCancel(t.Context())
	done, online := make(chan error, 1), make(chan struct{}, 1)
	go func() {
		done <- worker.Run(ctx, worker.RunOptions{ConfigPath: workerConf, HTTP: proxy.Client(), Logger: quiet(),
			Listen: []listen.Addr{private, tcp}, OnState: func(s worker.State) {
				if s.State == worker.StateOnline {
					select {
					case online <- struct{}{}:
					default:
					}
				}
			}})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !strings.Contains(err.Error(), "removed") {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("worker did not stop")
		}
	})
	select {
	case <-online:
	case <-time.After(10 * time.Second):
		t.Fatal("worker liveness and channel readiness failed during setup")
	}
	if status, code := worker.ReadStatus(t.Context(), workerConf, proxy.Client()); code != worker.StatusOnline {
		t.Fatalf("pending worker status: %+v", status.Server)
	}
	gw := clientFor(private)
	req := transportRequest{method: "GET", path: "/api/projects"}
	req.send(t, gw.HTTP, "http://stage", 403, nil)
	owner := browserClient(t, proxy)
	claimBrowser(t, owner, proxy.URL, output.String())
	req.send(t, gw.HTTP, "http://stage", 200, nil)
	if status, err := gw.AccessStatus(t.Context()); err != nil || !status.AgentOnly || status.Mode != "claimed" {
		t.Fatalf("worker gateway must identify itself as agent-only: %+v, %v", status, err)
	}
	req.headers = http.Header{workerchan.PrivateGatewayHeader: {"1"}}
	req.send(t, http.DefaultClient, tcp.URL(), 403, nil)
	req.path = api.GatewayPrefix + "/api/projects"
	req.headers.Set(buildinfo.APIVersionHeader, strconv.Itoa(buildinfo.APIVersion))
	req.send(t, proxy.Client(), proxy.URL, 401, nil)
	req.headers.Set("Authorization", "Bearer "+joined.Token) // legacy gateway reflecting a caller header
	req.send(t, proxy.Client(), proxy.URL, 403, nil)
	req.path = "/api/projects"
	req.send(t, proxy.Client(), proxy.URL, 401, nil)

	for _, restricted := range []transportRequest{
		{method: "POST", path: "/api/machines/pairings", body: "{}"},
		{method: "PUT", path: "/api/harnesses/t3/settings", body: "{}"},
		{method: "PUT", path: "/api/machines/1/harnesses/t3/settings", body: "{}"},
		{method: "POST", path: "/api/tasks/1/requests/1", body: "{}"},
		{method: "POST", path: "/api/auth/cli/login", body: "{}"},
		{method: "POST", path: fmt.Sprintf("/api/connections/%d/observations", task.ConnectionID), body: `{"observations":[]}`},
	} {
		restricted.send(t, gw.HTTP, "http://stage", 403, nil)
	}
	(transportRequest{method: "GET", path: api.GatewayPrefix + "/api/projects"}).send(t, gw.HTTP, "http://stage", 404, nil)
	(transportRequest{method: "GET", path: "/api/me"}).send(t, gw.HTTP, "http://stage", 403, nil)
	(transportRequest{method: "GET", path: "/api/machines"}).send(t, gw.HTTP, "http://stage", 200, nil)
	(transportRequest{method: "GET", path: "/api/machines"}).send(t, owner, proxy.URL, 200, nil)
	assertMCP(t, gw.HTTP, "http://stage/mcp", "", task.ID, "Worker MCP", 1, joined.MachineID)
}

func TestSharedClaimResumesWorkOnce(t *testing.T) {
	short := isolate(t)
	upstream := freePort(t)
	proxy := proxyFor(t, upstream)
	c := cfg(t, filepath.Join(short, "data"), upstream)
	c.PublicURL = proxy.URL
	fake := newT3()
	personal := start(t, c, fake)
	seed := startTask(t, fake, personal.ready.Listeners[0])
	if err := personal.stop(t); err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	disk, err := sqlite.Open(ctx, filepath.Join(c.DataDir, sqlite.DBFile))
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	database := db.NewStore(disk)
	cron, err := database.CreateSchedule(ctx, db.NewSchedule{
		Title: "Cron paused for setup", Prompt: "work", ProjectID: seed.ProjectID, ConnectionID: seed.ConnectionID,
		Cron: "*/5 * * * *", Tz: "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	due := time.Now().Add(-time.Second).UTC().Truncate(time.Second)
	if err := disk.Write(ctx, func(tx store.Tx) error {
		return tx.Schedules().SetNextRun(ctx, cron.ID, &due)
	}); err != nil {
		t.Fatal(err)
	}
	tz := "UTC"
	one, err := database.CreateTask(ctx, db.NewTask{
		Title: "One-shot due during setup", Prompt: "work", ProjectID: seed.ProjectID, ConnectionID: seed.ConnectionID,
		StartAt: &due, StartTz: &tz,
	})
	if err != nil {
		t.Fatal(err)
	}
	on := true
	output := &syncBuffer{}
	opts := serve.Options{Config: c, SharedAccess: &on, BootstrapOutput: output, HarnessOverrides: map[string]any{t3.Kind: fake}}
	instance, _ := startSharedOwner(t, opts)
	time.Sleep(150 * time.Millisecond)
	if runs, err := database.ScheduleRuns(ctx, cron.ID, 10); err != nil || len(runs) != 0 || fake.launches.Load() != 1 {
		t.Fatalf("background work started during setup: runs %+v launch count %d err %v", runs, fake.launches.Load(), err)
	}
	claimBrowser(t, browserClient(t, proxy), proxy.URL, output.String())
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		got, err := database.GetTask(ctx, one.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.ExternalSessionID != nil {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("due task did not resume after claim: %s", got.State)
		case <-tick.C:
		}
	}
	if runs, err := database.ScheduleRuns(ctx, cron.ID, 10); err != nil || len(runs) != 1 || runs[0].Outcome != db.RunMissed {
		t.Fatalf("cron fire during setup was not recorded once as missed: %+v, %v", runs, err)
	}
	if err := instance.stop(t); err != nil {
		t.Fatal(err)
	}
	opts.SharedAccess = nil
	startSharedOwner(t, opts)
	time.Sleep(150 * time.Millisecond)
	if n := fake.launches.Load(); n != 2 {
		t.Fatalf("launch count across claim and restart %d, want 2", n)
	}
}
