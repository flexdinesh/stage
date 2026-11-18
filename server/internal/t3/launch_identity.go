package t3

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

type pinnedTokenKey struct{}

func tokenScope(token, request string) (string, string) {
	fingerprint := sha256.Sum256([]byte(token))
	scope := hex.EncodeToString(fingerprint[:])
	payload, _, ok := strings.Cut(token, ".")
	if !ok {
		return scope, ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return scope, ""
	}
	var claims struct {
		SID  string `json:"sid"`
		Kind string `json:"kind"`
	}
	if json.Unmarshal(raw, &claims) != nil || claims.SID == "" || claims.Kind != "session" {
		return scope, ""
	}
	// The token is still authenticated by T3. Its non-secret session ID is only
	// an idempotency namespace, never a local authorization decision.
	scope = "client:" + claims.SID
	pair, _ := json.Marshal([2]string{scope, request})
	hash := sha256.Sum256(pair)
	return scope, "mcp:launch:" + hex.EncodeToString(hash[:])
}
func (c *MCPClient) LaunchScope(ctx context.Context, request string) (string, string, string, error) {
	token, err := c.accessToken(ctx, "")
	if err != nil {
		return "", "", "", err
	}
	env := c.environmentID(ctx)
	if env == "" {
		return "", "", "", errors.New("T3 identity unavailable; reconnect in Settings")
	}
	scope, expected := tokenScope(token, request)
	return scope, env, expected, nil
}
func (c *MCPClient) LaunchPinned(ctx context.Context, in LaunchInput, scope, env string) (LaunchResult, error) {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	tok := c.current(ctx)
	actual, _ := tokenScope(tok.Token, in.ClientRequestID)
	if actual != scope || c.environmentID(ctx) != env {
		return LaunchResult{}, errors.New("T3 launch identity changed; link the existing session")
	}
	if !c.valid(tok) {
		return LaunchResult{}, ErrNotConnected
	}
	return c.LaunchThread(context.WithValue(ctx, pinnedTokenKey{}, tok.Token), in)
}
func (f *Fake) LaunchScope(_ context.Context, request string) (string, string, string, error) {
	return "fake", FakeEnvironmentID, "", nil
}
func (f *Fake) LaunchPinned(ctx context.Context, in LaunchInput, scope, env string) (LaunchResult, error) {
	if scope != "fake" || env != FakeEnvironmentID {
		return LaunchResult{}, errors.New("fake identity changed")
	}
	return f.LaunchThread(ctx, in)
}
