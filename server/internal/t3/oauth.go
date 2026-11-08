package t3

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OAuth for a full-access T3 mcp-client session, done headlessly. Mirrors
// t3code apps/server/src/auth/{McpOAuth,mcpOAuthHttp,http}.ts (t3 0.1.7):
//
//  1. POST /oauth/mcp/register {client_name, redirect_uris, token_endpoint_auth_method:"none"}
//     -> 201 {client_id}. Client ids are signed and stateless.
//  2. POST /api/auth/pairing-token (Bearer admin) {label, scopes: mcpClientScopes("full-access")}
//     -> {id, credential, expiresAt}. credential is a one-time pairing code.
//     The admin session must itself hold access:write + every delegated scope.
//  3. POST /oauth/mcp/decision {authorization: <authorize query params>,
//     decision: {_tag:"pairing-code", access:"full-access", code}} -> {redirectTo}.
//     redirectTo = redirect_uri?code=..&state=..&iss=.. (or ?error=..).
//     The redirect is never followed; the code is read from it.
//  4. POST /oauth/mcp/token (form) grant_type=authorization_code, code,
//     redirect_uri, client_id, code_verifier, resource
//     -> {access_token, token_type:"Bearer", expires_in, scope}. 30-day session.

const (
	oauthClientName = "t3-stage"
	// DefaultRedirectURI is registered with T3 but never served: the code is
	// read from the decision response. T3 accepts http only on loopback.
	DefaultRedirectURI = "http://127.0.0.1:7380/oauth/callback"
	fullAccess         = "full-access"
)

// fullAccessScopes is t3 mcpClientScopes("full-access"): the scopes a pairing
// code must carry to approve a full-access mcp-client session.
var fullAccessScopes = []string{
	"orchestration:read",
	"orchestration:operate",
	"settings:write",
	"source-control:write",
}

// OAuthError is a failed step of the OAuth flow with T3's error detail.
type OAuthError struct {
	Step    string // register|pairing-token|decision|token
	Status  int    // HTTP status, 0 if not an HTTP failure
	Code    string // OAuth error / T3 error tag, if any
	Message string
}

func (e *OAuthError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "t3 oauth %s", e.Step)
	if e.Status != 0 {
		fmt.Fprintf(&b, ": http %d", e.Status)
	}
	if e.Code != "" {
		fmt.Fprintf(&b, ": %s", e.Code)
	}
	if e.Message != "" {
		fmt.Fprintf(&b, ": %s", e.Message)
	}
	return b.String()
}

type oauthFlow struct {
	http        *http.Client
	baseURL     string // no trailing slash
	redirectURI string
	now         func() time.Time
}

// resource is the MCP resource T3 issues tokens for: <origin>/mcp.
func (f *oauthFlow) resource() string { return f.baseURL + "/mcp" }

// pkce returns a random S256 verifier and its challenge.
func pkce() (verifier, challenge string, err error) {
	verifier, err = randomToken(32)
	if err != nil {
		return "", "", err
	}
	return verifier, s256(verifier), nil
}

func s256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// authorize runs register -> (mint code) -> decision -> token. When
// pairingCode is "", a code is minted with adminToken.
func (f *oauthFlow) authorize(ctx context.Context, adminToken, pairingCode string) (StoredToken, error) {
	clientID, err := f.register(ctx)
	if err != nil {
		return StoredToken{}, err
	}
	code := strings.TrimSpace(pairingCode)
	if code == "" {
		if adminToken == "" {
			return StoredToken{}, fmt.Errorf("%w: no admin token or pairing code", ErrNotConnected)
		}
		code, err = f.mintPairingCode(ctx, adminToken)
		if err != nil {
			return StoredToken{}, err
		}
	}
	verifier, challenge, err := pkce()
	if err != nil {
		return StoredToken{}, err
	}
	state, err := randomToken(16)
	if err != nil {
		return StoredToken{}, err
	}
	authCode, err := f.decide(ctx, clientID, challenge, state, code)
	if err != nil {
		return StoredToken{}, err
	}
	return f.exchange(ctx, clientID, authCode, verifier)
}

func (f *oauthFlow) register(ctx context.Context) (string, error) {
	body := map[string]any{
		"client_name":                oauthClientName,
		"redirect_uris":              []string{f.redirectURI},
		"token_endpoint_auth_method": "none",
	}
	var out struct {
		ClientID string `json:"client_id"`
	}
	if err := f.postJSON(ctx, "register", "/oauth/mcp/register", "", body, &out); err != nil {
		return "", err
	}
	if out.ClientID == "" {
		return "", &OAuthError{Step: "register", Message: "response has no client_id"}
	}
	return out.ClientID, nil
}

func (f *oauthFlow) mintPairingCode(ctx context.Context, adminToken string) (string, error) {
	body := map[string]any{"label": "t3-stage mcp", "scopes": fullAccessScopes}
	var out struct {
		Credential string `json:"credential"`
	}
	if err := f.postJSON(ctx, "pairing-token", "/api/auth/pairing-token", adminToken, body, &out); err != nil {
		return "", err
	}
	if out.Credential == "" {
		return "", &OAuthError{Step: "pairing-token", Message: "response has no credential"}
	}
	return out.Credential, nil
}

func (f *oauthFlow) decide(ctx context.Context, clientID, challenge, state, pairingCode string) (string, error) {
	body := map[string]any{
		// Same fields as the /oauth/mcp/authorize query; T3 re-validates them.
		"authorization": map[string]string{
			"response_type":         "code",
			"client_id":             clientID,
			"redirect_uri":          f.redirectURI,
			"code_challenge":        challenge,
			"code_challenge_method": "S256",
			"state":                 state,
			"resource":              f.resource(),
		},
		"decision": map[string]string{"_tag": "pairing-code", "access": fullAccess, "code": pairingCode},
	}
	var out struct {
		RedirectTo string `json:"redirectTo"`
	}
	if err := f.postJSON(ctx, "decision", "/oauth/mcp/decision", "", body, &out); err != nil {
		return "", err
	}
	u, err := url.Parse(out.RedirectTo)
	if err != nil || out.RedirectTo == "" {
		return "", &OAuthError{Step: "decision", Message: fmt.Sprintf("bad redirect %q", out.RedirectTo)}
	}
	q := u.Query()
	if e := q.Get("error"); e != "" {
		return "", &OAuthError{Step: "decision", Code: e, Message: q.Get("error_description")}
	}
	if q.Get("state") != state {
		return "", &OAuthError{Step: "decision", Message: "state mismatch in redirect"}
	}
	code := q.Get("code")
	if code == "" {
		return "", &OAuthError{Step: "decision", Message: "redirect has no code"}
	}
	return code, nil
}

func (f *oauthFlow) exchange(ctx context.Context, clientID, code, verifier string) (StoredToken, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {f.redirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
		"resource":      {f.resource()},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.baseURL+"/oauth/mcp/token", strings.NewReader(form.Encode()))
	if err != nil {
		return StoredToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var out struct {
		AccessToken string  `json:"access_token"`
		TokenType   string  `json:"token_type"`
		ExpiresIn   float64 `json:"expires_in"`
	}
	if err := f.do(req, "token", &out); err != nil {
		return StoredToken{}, err
	}
	if out.AccessToken == "" {
		return StoredToken{}, &OAuthError{Step: "token", Message: "response has no access_token"}
	}
	return StoredToken{
		ClientID:  clientID,
		Token:     out.AccessToken,
		ExpiresAt: f.now().Add(time.Duration(out.ExpiresIn) * time.Second).UTC(),
	}, nil
}

func (f *oauthFlow) postJSON(ctx context.Context, step, path, bearer string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	return f.do(req, step, out)
}

func (f *oauthFlow) do(req *http.Request, step string, out any) error {
	resp, err := f.http.Do(req)
	if err != nil {
		return &OAuthError{Step: step, Message: err.Error()}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return &OAuthError{Step: step, Status: resp.StatusCode, Message: err.Error()}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return httpError(step, resp.StatusCode, raw)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &OAuthError{Step: step, Status: resp.StatusCode, Message: "decode response: " + err.Error()}
	}
	return nil
}

// httpError extracts T3's error detail. Shapes seen in source:
// OAuth {error, error_description}; Effect tagged {_tag, message};
// environment errors {_tag, reason|code, requiredScope, ...}.
func httpError(step string, status int, raw []byte) error {
	var body struct {
		Error            json.RawMessage `json:"error"`
		ErrorDescription string          `json:"error_description"`
		Tag              string          `json:"_tag"`
		Message          string          `json:"message"`
		Reason           string          `json:"reason"`
		Code             string          `json:"code"`
		RequiredScope    string          `json:"requiredScope"`
	}
	e := &OAuthError{Step: step, Status: status}
	if json.Unmarshal(raw, &body) != nil {
		e.Message = truncate(strings.TrimSpace(string(raw)), 300)
		return e
	}
	var errStr string
	_ = json.Unmarshal(body.Error, &errStr)
	e.Code = firstNonEmpty(errStr, body.Code, body.Tag)
	e.Message = firstNonEmpty(body.ErrorDescription, body.Message)
	if body.Reason != "" {
		e.Message = strings.TrimSpace(e.Message + " (reason: " + body.Reason + ")")
	}
	if body.RequiredScope != "" {
		e.Message = strings.TrimSpace(e.Message + " (required scope: " + body.RequiredScope + ")")
	}
	if e.Code == "" && e.Message == "" {
		e.Message = truncate(strings.TrimSpace(string(raw)), 300)
	}
	return e
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
