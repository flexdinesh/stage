package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/flexdinesh/stage/server/internal/auth"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/listen"
	"github.com/flexdinesh/stage/server/internal/principal"
)

// Credential is the record stored in an OS-private session or agent file.
type Credential struct {
	Target         string                `json:"target"`
	InstallationID string                `json:"installationId"`
	Audience       store.SessionAudience `json:"audience"`
	ExpiresAt      time.Time             `json:"expiresAt"`
	Token          string                `json:"token"`
}

func (Credential) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte("[redacted credential]")) }

func (c Credential) Validate(audience store.SessionAudience) error {
	if audience != store.SessionCLI && audience != store.SessionAgent ||
		c.Audience != audience || c.InstallationID == "" || c.ExpiresAt.IsZero() {
		return errors.New("invalid Stage credential metadata")
	}
	raw := c.Token
	if audience == store.SessionAgent {
		var scoped bool
		raw, scoped = strings.CutPrefix(raw, principal.AgentTokenPrefix)
		if !scoped {
			return errors.New("use a Stage agent credential in agent configuration")
		}
	}
	value, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || len(value) != 32 {
		return errors.New("invalid Stage credential")
	}
	return nil
}

// CredentialTarget canonicalizes a credential's destination. Shared
// credentials travel only over HTTPS, loopback HTTP, or an exact Unix path.
func CredentialTarget(raw string) (string, error) {
	if strings.HasPrefix(raw, "unix:") {
		addr, err := listen.Parse(raw)
		if err != nil {
			return "", err
		}
		return addr.URL(), nil
	}
	return auth.ParsePublicOrigin(raw)
}

// WithCredential gives this client an explicit, purpose-specific source.
// Loading per request lets a running stdio MCP process see rotations.
func (c *Client) WithCredential(load func() (Credential, error)) *Client {
	copy := *c
	copy.credential = load
	return &copy
}

func rejectRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func (c *Client) roundTrip(req *http.Request) (*http.Response, error) {
	if c.credential == nil {
		return c.HTTP.Do(req)
	}
	cred, err := c.credential()
	if err != nil {
		return nil, err
	}
	if cred == (Credential{}) {
		return c.HTTP.Do(req)
	}
	if err := cred.Validate(cred.Audience); err != nil {
		return nil, err
	}
	if cred.Audience == store.SessionAgent && !time.Now().Before(cred.ExpiresAt) {
		return nil, errors.New("Stage agent credential expired. Rotate it and update the credential file")
	}
	target, err := CredentialTarget(c.BaseURL)
	if err != nil || target != cred.Target {
		return nil, errors.New("Stage credential is for a different address. Select its configured target")
	}
	return c.sendBound(req, target, cred.InstallationID, cred.Token)
}

// sendBound first probes the target without credentials. The sensitive
// request must reuse that connection and may neither redial nor redirect.
func (c *Client) sendBound(req *http.Request, target, installationID, token string) (*http.Response, error) {
	transport, ok := c.HTTP.Transport.(*http.Transport)
	if c.HTTP.Transport == nil {
		transport, ok = http.DefaultTransport.(*http.Transport)
	}
	if !ok {
		return nil, errors.New("Stage credentials require a direct HTTP transport")
	}
	tr := transport.Clone()
	tr.Proxy = nil
	tr.DialTLSContext, tr.DialTLS = nil, nil
	tr.DisableKeepAlives = false
	tr.MaxIdleConnsPerHost = 1
	dial := tr.DialContext
	if dial == nil {
		dial = (&net.Dialer{Timeout: 30 * time.Second}).DialContext
	}
	var used atomic.Bool
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if !used.CompareAndSwap(false, true) {
			return nil, errors.New("Stage closed the verified connection before credentials could be sent")
		}
		conn, err := dial(ctx, network, addr)
		if err == nil && strings.HasPrefix(target, "http://") {
			if remote, ok := conn.RemoteAddr().(*net.TCPAddr); !ok || !remote.IP.IsLoopback() {
				conn.Close()
				return nil, errors.New("Stage credentials require HTTPS or a loopback connection")
			}
		}
		return conn, err
	}
	h := &http.Client{Transport: tr, Timeout: c.HTTP.Timeout, CheckRedirect: rejectRedirect}
	defer tr.CloseIdleConnections()

	base, err := url.Parse(c.base)
	if err != nil || req.URL.Scheme != base.Scheme || req.URL.Host != base.Host || req.Host != base.Host {
		return nil, errors.New("credential request does not match the Stage address")
	}
	probe, err := http.NewRequestWithContext(req.Context(), http.MethodGet, c.base+"/api/auth/status", nil)
	if err != nil {
		return nil, err
	}
	res, err := h.Do(probe)
	if err != nil {
		return nil, err
	}
	var status AccessStatus
	if _, err := c.readResponse(res, &status); err != nil {
		return nil, err
	}
	switch {
	case status.AgentOnly:
		return nil, errors.New("the Stage agent socket accepts agent traffic without a bearer. Use the product address to sign in")
	case status.Mode != string(store.AccessClaimed):
		return nil, errors.New("this Stage server has no active shared accounts")
	case status.InstallationID == "" || status.InstallationID != installationID:
		return nil, errors.New("Stage installation changed at this address. Verify the address before signing in or configuring a credential")
	}
	req = req.Clone(req.Context())
	req.Close = true
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return h.Do(req)
}

func (c *Client) Login(ctx context.Context, installationID, email, password string) (Credential, error) {
	target, err := CredentialTarget(c.BaseURL)
	if err != nil {
		return Credential{}, err
	}
	body, err := json.Marshal(struct{ Email, Password string }{email, password})
	if err != nil {
		return Credential{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/auth/cli/login", bytes.NewReader(body))
	if err != nil {
		return Credential{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.sendBound(req, target, installationID, "")
	if err != nil {
		return Credential{}, err
	}
	cred := Credential{Target: target, Audience: store.SessionCLI}
	_, err = c.readResponse(res, &cred)
	if err != nil {
		return Credential{}, err
	}
	if cred.InstallationID != installationID {
		return Credential{}, errors.New("Stage installation changed during sign-in")
	}
	return cred, cred.Validate(store.SessionCLI)
}

func (c *Client) Logout(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/api/auth/logout", nil, nil)
}

type Account struct {
	store.User
	Email  *string             `json:"email"`
	Role   store.AccountRole   `json:"role"`
	Status store.AccountStatus `json:"status"`
}

func (c *Client) Me(ctx context.Context) (Account, error) {
	var account Account
	err := c.do(ctx, http.MethodGet, "/api/me", nil, &account)
	return account, err
}

func (c *Client) RotateAgent(ctx context.Context) (Credential, error) {
	target, err := CredentialTarget(c.BaseURL)
	if err != nil {
		return Credential{}, err
	}
	cred := Credential{Target: target, Audience: store.SessionAgent}
	if err := c.do(ctx, http.MethodPost, "/api/account/agent-credential", nil, &cred); err != nil {
		return Credential{}, err
	}
	return cred, cred.Validate(store.SessionAgent)
}

func (c *Client) RevokeAgent(ctx context.Context) error {
	return c.do(ctx, http.MethodDelete, "/api/account/agent-credential", nil, nil)
}
