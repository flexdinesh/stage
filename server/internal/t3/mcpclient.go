package t3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// renewWindow: renew the 30-day token when less than this is left.
	renewWindow = 5 * 24 * time.Hour
	// expirySkew: treat a token as expired this long before it actually is.
	expirySkew = time.Minute
	// lazyRetryAfter: calls retry a failed renewal at most this often while
	// the current token is still valid (Run owns the steady retry loop).
	lazyRetryAfter = time.Minute
	runCheckEvery  = time.Hour
	runBackoffMin  = 30 * time.Second
	runBackoffMax  = 30 * time.Minute
	oauthTimeout   = 30 * time.Second
	envTimeout     = 3 * time.Second
	uploadTimeout  = 2 * time.Minute
	// mcpProtocolVersion: T3 0.1.7 serves only this version
	// (apps/server/src/mcp/McpHttpServer.ts layerMcpTransport).
	mcpProtocolVersion = "2025-06-18"
)

// Options configures NewMCPClient.
type Options struct {
	BaseURL    string // T3_URL, e.g. http://127.0.0.1:3773 (OAuth + /mcp)
	AdminToken string // T3_ADMIN_TOKEN; "" disables auto-renew (Pair only)
	PublicURL  string // T3_PUBLIC_URL, browser base for "Open in T3" links
	Store      TokenStore
	HTTPClient *http.Client // nil = http.DefaultClient transport
	Logger     *slog.Logger // nil = slog.Default()
	// RedirectURI registered with T3; never served. "" = DefaultRedirectURI.
	RedirectURI string
	// ClientName identifies this app's session in T3 (OAuth client_name,
	// pairing label "<name> mcp"). "" = DefaultClientName.
	ClientName string
}

// MCPClient is the real Client over T3's HTTP MCP endpoint. It keeps a
// full-access mcp-client OAuth token fresh (lazily on calls, on 401, and in
// the background via Run) and reconnects the MCP session when it drops.
type MCPClient struct {
	opts  Options
	log   *slog.Logger
	oauth *oauthFlow
	mcpHC *http.Client
	envHC *http.Client
	upHC  *http.Client
	now   func() time.Time

	authMu sync.Mutex // serializes token acquisition

	mu         sync.Mutex // guards fields below
	tok        StoredToken
	loaded     bool
	lastErr    string
	lastFailAt time.Time
	rejected   bool // T3 answered 401 to the current token
	envID      string
	envFailAt  time.Time

	sessMu  sync.Mutex
	session *mcp.ClientSession
}

var _ Client = (*MCPClient)(nil)

// NewMCPClient builds the client. It does no I/O; tokens are loaded from
// Store and obtained lazily.
func NewMCPClient(opts Options) *MCPClient {
	opts.BaseURL = strings.TrimRight(opts.BaseURL, "/")
	if opts.RedirectURI == "" {
		opts.RedirectURI = DefaultRedirectURI
	}
	if opts.Store == nil {
		opts.Store = NewMemoryTokenStore()
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	base := opts.HTTPClient
	if base == nil {
		base = &http.Client{}
	}
	rt := base.Transport
	if rt == nil {
		rt = http.DefaultTransport
	}
	if opts.ClientName == "" {
		opts.ClientName = DefaultClientName
	}
	c := &MCPClient{opts: opts, log: log, now: time.Now}
	oauthHC := *base
	if oauthHC.Timeout == 0 {
		oauthHC.Timeout = oauthTimeout
	}
	// OAuth responses are JSON; T3 redirects are read, never followed.
	oauthHC.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c.oauth = &oauthFlow{http: &oauthHC, baseURL: opts.BaseURL, redirectURI: opts.RedirectURI, clientName: opts.ClientName, now: c.now}
	envHC := *base
	envHC.Timeout = envTimeout
	c.envHC = &envHC
	upHC := *base
	upHC.Timeout = uploadTimeout
	c.upHC = &upHC
	mcpHC := *base
	mcpHC.Timeout = 0 // streamable responses may be SSE; bound by ctx instead
	mcpHC.Transport = &authTransport{base: rt, c: c}
	c.mcpHC = &mcpHC
	return c
}

// Run keeps the token fresh until ctx is done: renews when less than 5 days
// remain (or none/expired), retrying with backoff and recording errors via
// Store.SetLastError. Without an AdminToken it only reports expiry.
func (c *MCPClient) Run(ctx context.Context) {
	backoff := time.Duration(0)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			c.Close()
			return
		case <-timer.C:
		}
		err := c.refreshIfNeeded(ctx)
		next := runCheckEvery
		if err != nil && ctx.Err() == nil {
			backoff = min(max(backoff*2, runBackoffMin), runBackoffMax)
			next = backoff
			c.log.Warn("t3 token renewal failed", "err", err, "retryIn", next)
		} else {
			backoff = 0
		}
		timer.Reset(next)
	}
}

// refreshIfNeeded renews when the token is missing, expired or in the renew
// window. No-op without an admin token.
func (c *MCPClient) refreshIfNeeded(ctx context.Context) error {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	tok := c.current(ctx)
	if c.opts.AdminToken == "" {
		return nil // nothing to renew with; Status reports expiry
	}
	if c.valid(tok) && !c.needsRenew(tok) && !c.isRejected() {
		return nil
	}
	_, err := c.renewLocked(ctx)
	return err
}

// Close drops the MCP session.
func (c *MCPClient) Close() {
	c.sessMu.Lock()
	defer c.sessMu.Unlock()
	if c.session != nil {
		_ = c.session.Close()
		c.session = nil
	}
}

// Pair authorizes with a user-supplied pairing code (`t3 pair`) instead of a
// minted one, then stores the token.
func (c *MCPClient) Pair(ctx context.Context, code string) error {
	if strings.TrimSpace(code) == "" {
		return errors.New("empty pairing code")
	}
	c.authMu.Lock()
	defer c.authMu.Unlock()
	c.current(ctx)
	tok, err := c.oauth.authorize(ctx, "", code)
	if err != nil {
		return err
	}
	return c.adoptLocked(ctx, tok)
}

// Status reports connection state. Never fails; may fetch the environment
// id from T3 (unauthenticated, short timeout) when not yet known.
func (c *MCPClient) Status(ctx context.Context) ConnectionStatus {
	tok := c.current(ctx)
	env := c.environmentID(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	st := ConnectionStatus{
		Connected:     c.valid(tok) && !c.rejected,
		ClientID:      tok.ClientID,
		LastError:     c.lastErr,
		EnvironmentID: env,
	}
	if !tok.ExpiresAt.IsZero() {
		exp := tok.ExpiresAt
		st.ExpiresAt = &exp
	}
	return st
}

// PublicURL is the browser base for T3 web links (Options.PublicURL).
func (c *MCPClient) PublicURL() string { return c.opts.PublicURL }

// WebURL is the T3 web link for a thread, "" if the environment id is unknown.
func (c *MCPClient) WebURL(ctx context.Context, threadID string) string {
	return WebThreadURL(c.opts.PublicURL, c.environmentID(ctx), threadID)
}

// ---- token state ----

// current returns the in-memory token, loading it from Store once.
func (c *MCPClient) current(ctx context.Context) StoredToken {
	c.mu.Lock()
	if c.loaded {
		tok := c.tok
		c.mu.Unlock()
		return tok
	}
	c.mu.Unlock()
	tok, err := c.opts.Store.LoadToken(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.log.Warn("t3 token load failed", "err", err)
		return c.tok
	}
	if !c.loaded {
		c.tok, c.loaded = tok, true
	}
	return c.tok
}

func (c *MCPClient) valid(tok StoredToken) bool {
	return tok.Token != "" && c.now().Add(expirySkew).Before(tok.ExpiresAt)
}

func (c *MCPClient) needsRenew(tok StoredToken) bool {
	return tok.ExpiresAt.Sub(c.now()) < renewWindow
}

func (c *MCPClient) isRejected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rejected
}

// accessToken returns a usable token, obtaining or renewing it if needed.
// rejected is a token T3 just answered 401 to ("" if none): it is renewed
// unless another caller already replaced it.
func (c *MCPClient) accessToken(ctx context.Context, rejected string) (string, error) {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	tok := c.current(ctx)
	if rejected != "" && tok.Token != rejected && c.valid(tok) {
		return tok.Token, nil
	}
	stale := rejected != "" || c.isRejected()
	if c.valid(tok) && !stale && !c.needsRenew(tok) {
		return tok.Token, nil
	}
	if c.opts.AdminToken == "" {
		if c.valid(tok) && !stale {
			return tok.Token, nil // in renew window; cannot renew without admin token
		}
		return "", fmt.Errorf("%w: token missing, expired or rejected; pair again", ErrNotConnected)
	}
	if c.valid(tok) && !stale {
		c.mu.Lock()
		recent := !c.lastFailAt.IsZero() && c.now().Sub(c.lastFailAt) < lazyRetryAfter
		c.mu.Unlock()
		if recent {
			return tok.Token, nil
		}
	}
	newTok, err := c.renewLocked(ctx)
	if err != nil {
		if c.valid(tok) && !stale {
			return tok.Token, nil // keep using the still-valid token
		}
		return "", fmt.Errorf("%w: %v", ErrNotConnected, err)
	}
	return newTok.Token, nil
}

// renewLocked runs the OAuth flow with a minted pairing code. authMu held.
func (c *MCPClient) renewLocked(ctx context.Context) (StoredToken, error) {
	tok, err := c.oauth.authorize(ctx, c.opts.AdminToken, "")
	if err != nil {
		c.recordError(ctx, err)
		return StoredToken{}, err
	}
	if err := c.adoptLocked(ctx, tok); err != nil {
		return StoredToken{}, err
	}
	c.log.Info("t3 token issued", "expiresAt", tok.ExpiresAt)
	return tok, nil
}

// adoptLocked persists a new token and clears errors. The MCP session is
// kept: authTransport sends the current token on every request and T3
// authenticates each request, not the session.
func (c *MCPClient) adoptLocked(ctx context.Context, tok StoredToken) error {
	if err := c.opts.Store.SaveToken(ctx, tok); err != nil {
		err = fmt.Errorf("t3: save token: %w", err)
		c.recordError(ctx, err)
		return err
	}
	c.mu.Lock()
	c.tok, c.loaded = tok, true
	c.rejected = false
	c.lastFailAt = time.Time{}
	hadErr := c.lastErr != ""
	c.lastErr = ""
	c.mu.Unlock()
	if hadErr {
		if err := c.opts.Store.SetLastError(ctx, ""); err != nil {
			c.log.Warn("t3 clear last error failed", "err", err)
		}
	}
	return nil
}

func (c *MCPClient) recordError(ctx context.Context, err error) {
	msg := err.Error()
	c.mu.Lock()
	changed := c.lastErr != msg
	c.lastErr = msg
	c.lastFailAt = c.now()
	c.mu.Unlock()
	if changed {
		if serr := c.opts.Store.SetLastError(ctx, msg); serr != nil {
			c.log.Warn("t3 record last error failed", "err", serr)
		}
	}
}

func (c *MCPClient) clearError(ctx context.Context) {
	c.mu.Lock()
	hadErr := c.lastErr != ""
	c.lastErr = ""
	c.rejected = false
	c.mu.Unlock()
	if hadErr {
		if err := c.opts.Store.SetLastError(ctx, ""); err != nil {
			c.log.Warn("t3 clear last error failed", "err", err)
		}
	}
}

// ---- environment id ----

func (c *MCPClient) environmentID(ctx context.Context) string {
	c.mu.Lock()
	env, failAt := c.envID, c.envFailAt
	c.mu.Unlock()
	if env != "" || (!failAt.IsZero() && c.now().Sub(failAt) < 30*time.Second) {
		return env
	}
	env = c.fetchEnvironmentID(ctx)
	if env == "" {
		c.mu.Lock()
		c.envFailAt = c.now()
		c.mu.Unlock()
		return ""
	}
	c.noteEnvironment(env)
	return env
}

// fetchEnvironmentID reads GET /.well-known/t3/environment (unauthenticated
// ExecutionEnvironmentDescriptor).
func (c *MCPClient) fetchEnvironmentID(ctx context.Context) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.opts.BaseURL+"/.well-known/t3/environment", nil)
	if err != nil {
		return ""
	}
	resp, err := c.envHC.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var d struct {
		EnvironmentID string `json:"environmentId"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&d) != nil {
		return ""
	}
	return d.EnvironmentID
}

func (c *MCPClient) noteEnvironment(env string) {
	if env == "" {
		return
	}
	c.mu.Lock()
	if c.envID == "" {
		c.envID = env
	}
	c.mu.Unlock()
}

// ---- MCP session ----

// authTransport injects the current bearer token and reports 401s to the
// call that sent the request (authRecord in the request context).
type authTransport struct {
	base http.RoundTripper
	c    *MCPClient
}

// authRecord holds the token T3 answered 401 to during one call attempt.
type authRecord struct {
	mu       sync.Mutex
	rejected string
	seen     bool
}

type authRecordKey struct{}

func (r *authRecord) note(tok string) {
	r.mu.Lock()
	r.rejected, r.seen = tok, true
	r.mu.Unlock()
}

func (r *authRecord) get() (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rejected, r.seen
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.c.mu.Lock()
	tok := t.c.tok.Token
	t.c.mu.Unlock()
	r := req.Clone(req.Context())
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := t.base.RoundTrip(r)
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		if rec, ok := req.Context().Value(authRecordKey{}).(*authRecord); ok {
			rec.note(tok)
		}
	}
	return resp, err
}

func (c *MCPClient) sessionFor(ctx context.Context) (*mcp.ClientSession, error) {
	if _, err := c.accessToken(ctx, ""); err != nil {
		return nil, err
	}
	c.sessMu.Lock()
	defer c.sessMu.Unlock()
	if c.session != nil {
		return c.session, nil
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "t3-stage", Version: "0.1.0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             c.opts.BaseURL + "/mcp",
		HTTPClient:           c.mcpHC,
		DisableStandaloneSSE: true, // request/response only
		MaxRetries:           -1,   // we reconnect ourselves
	}
	cs, err := client.Connect(ctx, transport, &mcp.ClientSessionOptions{ProtocolVersion: mcpProtocolVersion})
	if err != nil {
		return nil, fmt.Errorf("t3 mcp connect: %w", err)
	}
	c.session = cs
	return cs, nil
}

func (c *MCPClient) dropSession(cs *mcp.ClientSession) {
	c.sessMu.Lock()
	defer c.sessMu.Unlock()
	if c.session == cs && cs != nil {
		_ = cs.Close()
		c.session = nil
	}
}

// call invokes a tool and decodes its result into out. On 401 it renews the
// token once and retries (T3 rejected the request before running the tool).
// A JSON-RPC error (tool/protocol error, or a transient transport rejection
// the SDK reports as one) leaves the shared session open. Any other error
// means the session is broken: it is dropped (next call reconnects) and the
// call is retried once only when that cannot run the tool twice: the
// server lost the session (ErrSessionMissing: request refused), or the
// connection closed and the tool is idempotent (retrySafe).
func (c *MCPClient) call(ctx context.Context, tool string, args map[string]any, out any) error {
	for attempt := 0; ; attempt++ {
		rec := &authRecord{}
		actx := context.WithValue(ctx, authRecordKey{}, rec)
		cs, err := c.sessionFor(actx)
		var res *mcp.CallToolResult
		if err == nil {
			res, err = cs.CallTool(actx, &mcp.CallToolParams{Name: tool, Arguments: args})
		}
		if err == nil {
			c.clearRejected(ctx)
			return decodeToolResult(tool, res, out)
		}
		if errors.Is(err, ErrNotConnected) || ctx.Err() != nil {
			return err
		}
		if rejected, ok := rec.get(); ok {
			c.dropSession(cs) // the SDK fails the connection on 401
			c.markRejected(rejected)
			if attempt == 0 {
				if _, rerr := c.accessToken(ctx, rejected); rerr != nil {
					return rerr
				}
				continue
			}
			return fmt.Errorf("%w: t3 rejected the token", ErrNotConnected)
		}
		var wire *jsonrpc.Error
		if errors.As(err, &wire) {
			return fmt.Errorf("t3 %s: %w", tool, err)
		}
		c.dropSession(cs)
		if attempt == 0 && retrySafe(tool, args) && (errors.Is(err, mcp.ErrSessionMissing) ||
			errors.Is(err, mcp.ErrConnectionClosed)) {
			continue
		}
		return fmt.Errorf("t3 %s: %w", tool, err)
	}
}

// retrySafe reports whether running tool twice is harmless: reads, and
// launches deduplicated by clientRequestId.
func retrySafe(tool string, args map[string]any) bool {
	switch tool {
	case "t3_thread_read", "t3_thread_list", "t3_project_list", "orchestrator_capabilities",
		"t3_attachment_prepare_upload": // a duplicate pending upload just expires
		return true
	case "t3_thread_launch":
		id, ok := args["clientRequestId"].(string)
		return ok && id != ""
	}
	return false
}

// markRejected records a 401 against tok; ignored when tok is no longer the
// current token (another call already replaced it).
func (c *MCPClient) markRejected(tok string) {
	c.mu.Lock()
	if c.tok.Token == tok {
		c.rejected = true
	}
	c.mu.Unlock()
}

// IsTransportError reports whether a Client call failed without a definite
// answer from T3 (network failure, timeout, dropped session), so the tool
// may or may not have run. T3 tool/protocol errors, ErrNotFound and
// ErrNotConnected (nothing sent) are definite.
func IsTransportError(err error) bool {
	if err == nil || errors.Is(err, ErrNotConnected) || errors.Is(err, ErrNotFound) {
		return false
	}
	var te *ToolError
	if errors.As(err, &te) {
		return false
	}
	var wire *jsonrpc.Error
	if errors.As(err, &wire) {
		return wire.Code == errCodeTransportRejected
	}
	return true
}

// errCodeTransportRejected is the go-sdk's JSON-RPC code for requests the
// transport rejected (network failure, 429/502/503/504).
const errCodeTransportRejected = -32005

func (c *MCPClient) clearRejected(ctx context.Context) {
	c.mu.Lock()
	rejected := c.rejected
	c.mu.Unlock()
	if rejected {
		c.clearError(ctx)
	}
}

// ---- in-memory TokenStore ----

// MemoryTokenStore is a TokenStore kept in memory (tests, dev tools).
type MemoryTokenStore struct {
	mu      sync.Mutex
	tok     StoredToken
	lastErr string
	saves   int
}

func NewMemoryTokenStore() *MemoryTokenStore { return &MemoryTokenStore{} }

func (s *MemoryTokenStore) LoadToken(context.Context) (StoredToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tok, nil
}

func (s *MemoryTokenStore) SaveToken(_ context.Context, tok StoredToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tok = tok
	s.saves++
	return nil
}

func (s *MemoryTokenStore) SetLastError(_ context.Context, msg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastErr = msg
	return nil
}

// LastError returns the last recorded error ("" if cleared).
func (s *MemoryTokenStore) LastError() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

// Saves counts SaveToken calls.
func (s *MemoryTokenStore) Saves() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saves
}
