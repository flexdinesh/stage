package t3

import (
	"context"
	"errors"
	"fmt"
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
	BaseURL    string // the T3 URL, e.g. http://127.0.0.1:3773 (OAuth + /mcp)
	AdminToken string // the saved admin token; "" disables auto-renew (Pair only)
	PublicURL  string // browser base for "Open in T3" links
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

	// authMu serializes token acquisition, Reconfigure and SetAdminToken,
	// so an OAuth flow never straddles two base URLs; it guards
	// opts.AdminToken.
	authMu sync.Mutex

	mu         sync.Mutex // guards fields below
	baseURL    string     // T3 URL, no trailing slash (base())
	publicURL  string     // browser base for T3 web links
	tok        StoredToken
	loaded     bool
	lastErr    string
	lastFailAt time.Time
	rejected   bool // T3 answered 401 to the current token
	envID      string

	// probeMu guards the probe cache (lock order: probeMu, then mu).
	probeMu  sync.Mutex
	probe    Probe
	probeGen uint64 // bumped by Reconfigure; older results are dropped
	probeRun *probeRun

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
	c := &MCPClient{opts: opts, log: log, now: time.Now, baseURL: opts.BaseURL, publicURL: opts.PublicURL}
	oauthHC := *base
	if oauthHC.Timeout == 0 {
		oauthHC.Timeout = oauthTimeout
	}
	// OAuth responses are JSON; T3 redirects are read, never followed.
	oauthHC.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c.oauth = &oauthFlow{http: &oauthHC, base: c.base, redirectURI: opts.RedirectURI, clientName: opts.ClientName, now: c.now}
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
	if c.credentialed(ctx) {
		c.startProbe() // learn reachability and the environment id early
	}
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

// Unpair implements Client. It waits for a running OAuth flow.
func (c *MCPClient) Unpair(ctx context.Context) error {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	if err := c.opts.Store.SaveToken(ctx, StoredToken{}); err != nil {
		return fmt.Errorf("t3: clear token: %w", err)
	}
	c.mu.Lock()
	c.tok, c.loaded = StoredToken{}, true
	c.rejected = false
	c.lastFailAt = time.Time{}
	c.envID = ""
	c.mu.Unlock()
	c.clearError(ctx)
	c.Close()
	c.log.Info("t3 token forgotten")
	return nil
}

// Status reports connection state from memory and the probe cache. Never
// fails and never calls T3 (health polls it).
func (c *MCPClient) Status(ctx context.Context) ConnectionStatus {
	tok := c.current(ctx)
	p := c.cachedProbe()
	c.mu.Lock()
	defer c.mu.Unlock()
	env := c.envID
	if env == "" && p.Identity != nil {
		env = p.Identity.EnvironmentID
	}
	st := ConnectionStatus{
		Connected:     c.valid(tok) && !c.rejected,
		ClientID:      tok.ClientID,
		LastError:     c.lastErr,
		EnvironmentID: env,
		HasToken:      tok.Token != "",
		Expired:       tok.Token != "" && !c.valid(tok),
		Rejected:      c.rejected,
		Probe:         p,
	}
	if !tok.ExpiresAt.IsZero() {
		exp := tok.ExpiresAt
		st.ExpiresAt = &exp
	}
	return st
}

// PublicURL is the browser base for T3 web links.
func (c *MCPClient) PublicURL() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.publicURL
}

// base is the current T3 URL (no trailing slash).
func (c *MCPClient) base() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.baseURL
}

// credentialed: a token is stored or an admin token can mint one. An
// uncredentialed client (not paired) is never contacted in the background.
func (c *MCPClient) credentialed(ctx context.Context) bool {
	c.authMu.Lock()
	admin := c.opts.AdminToken
	c.authMu.Unlock()
	return admin != "" || c.current(ctx).Token != ""
}

// SetAdminToken implements Client. It waits for a running OAuth flow.
func (c *MCPClient) SetAdminToken(ctx context.Context, token string) error {
	c.authMu.Lock()
	c.opts.AdminToken = token
	c.authMu.Unlock()
	if token == "" {
		return nil
	}
	return c.refreshIfNeeded(ctx)
}

// Reconfigure implements Client. It waits for a running OAuth flow.
// In-flight tool calls on the old session may fail (transport error).
func (c *MCPClient) Reconfigure(base, public string) {
	base = strings.TrimRight(base, "/")
	c.authMu.Lock()
	defer c.authMu.Unlock()
	c.mu.Lock()
	changed := c.baseURL != base
	c.baseURL, c.publicURL = base, public
	if changed {
		c.envID = ""
	}
	c.mu.Unlock()
	if !changed {
		return
	}
	c.Close()
	c.probeMu.Lock()
	c.probeGen++
	c.probe, c.probeRun = Probe{}, nil
	c.probeMu.Unlock()
	c.log.Info("t3 url changed", "url", base)
}

// WebURL is the T3 web link for a thread, "" if the environment id is unknown.
func (c *MCPClient) WebURL(ctx context.Context, threadID string) string {
	return WebThreadURL(c.PublicURL(), c.environmentID(ctx), threadID)
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
	if token, ok := ctx.Value(pinnedTokenKey{}).(string); ok {
		if rejected != "" {
			return "", ErrNotConnected
		}
		return token, nil
	}
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

// environmentID is the known environment id, else the probe's (which may
// call T3, bounded by envTimeout).
func (c *MCPClient) environmentID(ctx context.Context) string {
	c.mu.Lock()
	env := c.envID
	c.mu.Unlock()
	if env != "" {
		return env
	}
	if p := c.Probe(ctx); p.Identity != nil {
		return p.Identity.EnvironmentID
	}
	return ""
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
	if pinned, ok := req.Context().Value(pinnedTokenKey{}).(string); ok {
		tok = pinned
	}
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
	client := mcp.NewClient(&mcp.Implementation{Name: "stage", Version: "0.1.0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             c.base() + "/mcp",
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
	var res *mcp.CallToolResult
	err := c.request(ctx, tool, retrySafe(tool, args), func(ctx context.Context, cs *mcp.ClientSession) error {
		var err error
		res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		return err
	})
	if err != nil {
		return err
	}
	return decodeToolResult(tool, res, out)
}

// request applies the transport and authentication retry policy to MCP
// requests. Decode tool errors after the transport succeeds.
func (c *MCPClient) request(ctx context.Context, method string, safe bool, invoke func(context.Context, *mcp.ClientSession) error) error {
	for attempt := 0; ; attempt++ {
		rec := &authRecord{}
		actx := context.WithValue(ctx, authRecordKey{}, rec)
		cs, err := c.sessionFor(actx)
		if err == nil {
			err = invoke(actx, cs)
			if err == nil {
				c.clearRejected(ctx)
				return nil
			}
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
			return fmt.Errorf("t3 %s: %w", method, err)
		}
		c.dropSession(cs)
		if attempt == 0 && safe && (errors.Is(err, mcp.ErrSessionMissing) ||
			errors.Is(err, mcp.ErrConnectionClosed)) {
			continue
		}
		return fmt.Errorf("t3 %s: %w", method, err)
	}
}

// retrySafe reports whether running tool twice is harmless: reads,
// launches, sends and updates deduplicated by clientRequestId, and project
// updates that only set a title or the archived flag.
func retrySafe(tool string, args map[string]any) bool {
	switch tool {
	case "t3_project_update":
		for key := range args {
			if key != "projectId" && key != "title" && key != "archived" {
				return false
			}
		}
		return true
	case "t3_thread_read", "t3_thread_list", "t3_thread_search", "t3_project_list", "orchestrator_capabilities",
		"t3_attachment_prepare_upload": // a duplicate pending upload just expires
		return true
	case "t3_thread_launch", "t3_thread_update", "t3_thread_send":
		id, ok := args["clientRequestId"].(string)
		return ok && id != ""
	case "t3_pending_request":
		return args["action"] == "list" || args["action"] == "read"
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
