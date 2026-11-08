package t3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeT3 mimics T3's OAuth endpoints (validation as in McpOAuth.ts) and
// serves /mcp with a go-sdk server behind bearer auth.
type fakeT3 struct {
	t   *testing.T
	srv *httptest.Server

	adminToken  string
	adminScopes []string

	mu             sync.Mutex
	registered     map[string]string // client_id -> redirect uri
	pairingCodes   map[string][]string
	pendingCodes   map[string]pendingCode
	tokens         map[string]bool
	seq            int
	mintCalls      int
	tokenCalls     int
	lastDecision   map[string]any
	lastTokenForm  url.Values
	mcpAuthHeaders []string
	expiresIn      int

	tools   map[string]mcp.ToolHandler
	handler atomic.Pointer[mcp.StreamableHTTPHandler]
}

type pendingCode struct {
	clientID, redirectURI, challenge, resource, access string
}

func newFakeT3(t *testing.T) *fakeT3 {
	t.Helper()
	f := &fakeT3{
		t:            t,
		adminToken:   "admin-secret",
		adminScopes:  []string{"access:write", "orchestration:read", "orchestration:operate", "settings:write", "source-control:write"},
		registered:   map[string]string{},
		pairingCodes: map[string][]string{},
		pendingCodes: map[string]pendingCode{},
		tokens:       map[string]bool{},
		tools:        map[string]mcp.ToolHandler{},
		expiresIn:    30 * 24 * 3600,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oauth/mcp/register", f.register)
	mux.HandleFunc("POST /api/auth/pairing-token", f.pairingToken)
	mux.HandleFunc("POST /oauth/mcp/decision", f.decision)
	mux.HandleFunc("POST /oauth/mcp/token", f.token)
	mux.HandleFunc("GET /.well-known/t3/environment", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"environmentId": "env-fake", "label": "fake"})
	})
	mux.HandleFunc("/mcp", f.mcp)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	f.resetMCP()
	return f
}

func (f *fakeT3) url() string { return f.srv.URL }

// resetMCP replaces the MCP handler, dropping all server-side sessions.
func (f *fakeT3) resetMCP() {
	server := mcp.NewServer(&mcp.Implementation{Name: "fake-t3", Version: "0"}, nil)
	f.mu.Lock()
	for name, h := range f.tools {
		server.AddTool(&mcp.Tool{Name: name, InputSchema: json.RawMessage(`{"type":"object"}`)}, h)
	}
	f.mu.Unlock()
	f.handler.Store(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
}

// tool registers a tool returning structured JSON (raw) built by fn.
func (f *fakeT3) tool(name string, fn func(args map[string]any) (*mcp.CallToolResult, error)) {
	f.mu.Lock()
	f.tools[name] = func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args map[string]any
		_ = json.Unmarshal(req.Params.Arguments, &args)
		return fn(args)
	}
	f.mu.Unlock()
	f.resetMCP()
}

func structured(raw string) *mcp.CallToolResult {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		panic(err)
	}
	return &mcp.CallToolResult{StructuredContent: v, Content: []mcp.Content{&mcp.TextContent{Text: raw}}}
}

func (f *fakeT3) addToken(tok string) {
	f.mu.Lock()
	f.tokens[tok] = true
	f.mu.Unlock()
}

func (f *fakeT3) revokeAll() {
	f.mu.Lock()
	f.tokens = map[string]bool{}
	f.mu.Unlock()
}

func (f *fakeT3) mcp(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	f.mu.Lock()
	f.mcpAuthHeaders = append(f.mcpAuthHeaders, auth)
	ok := f.tokens[strings.TrimPrefix(auth, "Bearer ")]
	f.mu.Unlock()
	if !ok {
		w.Header().Set("www-authenticate", `Bearer error="invalid_token"`)
		writeJSON(w, 401, map[string]any{"error": "invalid_mcp_credential", "message": "A valid T3 Code MCP credential is required."})
		return
	}
	f.handler.Load().ServeHTTP(w, r)
}

func (f *fakeT3) register(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
		AuthMethod   string   `json:"token_endpoint_auth_method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.RedirectURIs) != 1 {
		writeJSON(w, 400, map[string]any{"error": "invalid_redirect_uri", "error_description": "Register between 1 and 5 redirect URIs."})
		return
	}
	f.mu.Lock()
	f.seq++
	id := fmt.Sprintf("client-%d.sig", f.seq)
	f.registered[id] = body.RedirectURIs[0]
	f.mu.Unlock()
	writeJSON(w, 201, map[string]any{
		"client_id": id, "client_name": body.ClientName, "redirect_uris": body.RedirectURIs,
		"grant_types": []string{"authorization_code"}, "response_types": []string{"code"}, "token_endpoint_auth_method": "none",
	})
}

func (f *fakeT3) pairingToken(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+f.adminToken {
		writeJSON(w, 401, map[string]any{"_tag": "EnvironmentAuthInvalidError", "code": "auth_invalid", "reason": "invalid_token"})
		return
	}
	var body struct {
		Label  string   `json:"label"`
		Scopes []string `json:"scopes"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	for _, s := range body.Scopes {
		if !contains(f.adminScopes, s) {
			writeJSON(w, 403, map[string]any{"_tag": "EnvironmentScopeRequiredError", "code": "insufficient_scope", "requiredScope": s})
			return
		}
	}
	f.mu.Lock()
	f.mintCalls++
	f.seq++
	code := fmt.Sprintf("PAIR-%d", f.seq)
	f.pairingCodes[code] = body.Scopes
	f.mu.Unlock()
	writeJSON(w, 200, map[string]any{"id": "link-1", "credential": code, "label": body.Label, "expiresAt": "2030-01-01T00:00:00.000Z"})
}

// AddUserPairingCode registers a code as if from `t3 pair` (standard scopes).
func (f *fakeT3) addUserPairingCode(code string) {
	f.mu.Lock()
	f.pairingCodes[code] = append([]string(nil), fullAccessScopes...)
	f.mu.Unlock()
}

func (f *fakeT3) decision(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Authorization map[string]string `json:"authorization"`
		Decision      map[string]string `json:"decision"`
	}
	raw := map[string]any{}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&raw); err != nil {
		writeJSON(w, 400, map[string]any{"_tag": "AuthMcpApprovalError", "message": "bad json"})
		return
	}
	b, _ := json.Marshal(raw)
	_ = json.Unmarshal(b, &body)
	f.mu.Lock()
	f.lastDecision = raw
	f.mu.Unlock()
	a := body.Authorization
	f.mu.Lock()
	registered, ok := f.registered[a["client_id"]]
	f.mu.Unlock()
	if !ok {
		writeJSON(w, 400, map[string]any{"_tag": "AuthMcpApprovalError", "message": "This sign-in link names an unknown app."})
		return
	}
	if a["redirect_uri"] != registered {
		writeJSON(w, 400, map[string]any{"_tag": "AuthMcpApprovalError", "message": "unregistered redirect"})
		return
	}
	redirectErr := func(code, desc string) {
		u, _ := url.Parse(a["redirect_uri"])
		q := u.Query()
		q.Set("error", code)
		q.Set("error_description", desc)
		q.Set("state", a["state"])
		u.RawQuery = q.Encode()
		writeJSON(w, 200, map[string]any{"redirectTo": u.String()})
	}
	if a["response_type"] != "code" {
		redirectErr("unsupported_response_type", "Only response_type=code is supported.")
		return
	}
	if a["code_challenge_method"] != "S256" || len(a["code_challenge"]) != 43 {
		redirectErr("invalid_request", "PKCE with code_challenge_method=S256 is required.")
		return
	}
	if a["resource"] != f.url()+"/mcp" {
		redirectErr("invalid_target", "wrong resource")
		return
	}
	d := body.Decision
	if d["_tag"] != "pairing-code" {
		writeJSON(w, 400, map[string]any{"_tag": "AuthMcpApprovalError", "message": "unexpected decision"})
		return
	}
	f.mu.Lock()
	scopes, ok := f.pairingCodes[d["code"]]
	delete(f.pairingCodes, d["code"])
	f.mu.Unlock()
	if !ok {
		writeJSON(w, 400, map[string]any{"_tag": "AuthMcpApprovalError", "message": "That code is unknown or already used."})
		return
	}
	for _, s := range fullAccessScopes {
		if d["access"] == "full-access" && !contains(scopes, s) {
			writeJSON(w, 400, map[string]any{"_tag": "AuthMcpApprovalError", "message": "insufficient_scope"})
			return
		}
	}
	f.mu.Lock()
	f.seq++
	code := fmt.Sprintf("authcode-%d", f.seq)
	f.pendingCodes[code] = pendingCode{clientID: a["client_id"], redirectURI: a["redirect_uri"], challenge: a["code_challenge"], resource: a["resource"], access: d["access"]}
	f.mu.Unlock()
	u, _ := url.Parse(a["redirect_uri"])
	q := u.Query()
	q.Set("code", code)
	q.Set("state", a["state"])
	q.Set("iss", f.url())
	u.RawQuery = q.Encode()
	writeJSON(w, 200, map[string]any{"redirectTo": u.String()})
}

func (f *fakeT3) token(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		writeJSON(w, 400, map[string]any{"error": "invalid_request", "error_description": "form body required"})
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid_request", "error_description": err.Error()})
		return
	}
	form := r.PostForm
	f.mu.Lock()
	f.tokenCalls++
	f.lastTokenForm = form
	pending, ok := f.pendingCodes[form.Get("code")]
	delete(f.pendingCodes, form.Get("code"))
	f.mu.Unlock()
	if form.Get("grant_type") != "authorization_code" {
		writeJSON(w, 400, map[string]any{"error": "unsupported_grant_type", "error_description": "Only authorization_code is supported."})
		return
	}
	if !ok || pending.clientID != form.Get("client_id") || pending.redirectURI != form.Get("redirect_uri") ||
		pending.resource != form.Get("resource") || s256(form.Get("code_verifier")) != pending.challenge {
		writeJSON(w, 400, map[string]any{"error": "invalid_grant", "error_description": "The authorization code is invalid or expired."})
		return
	}
	f.mu.Lock()
	f.seq++
	tok := fmt.Sprintf("access-%d.sig", f.seq)
	f.tokens[tok] = true
	expiresIn := f.expiresIn
	f.mu.Unlock()
	writeJSON(w, 200, map[string]any{"access_token": tok, "token_type": "Bearer", "expires_in": expiresIn, "scope": strings.Join(fullAccessScopes, " ")})
}

func (f *fakeT3) counts() (mint, token int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mintCalls, f.tokenCalls
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
