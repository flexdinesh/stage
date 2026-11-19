package client

import (
	"context"
	"time"
)

// OpenCodeStatus is GET /api/opencode/status (docs/api.md).
type OpenCodeStatus struct {
	ConnectionID      int64      `json:"connectionId"`
	State             string     `json:"state"` // unconfigured | connected | unavailable | rejected
	Reason            string     `json:"reason,omitempty"`
	CheckedAt         *time.Time `json:"checkedAt,omitempty"`
	Endpoint          string     `json:"endpoint"`
	BrowserEndpoint   string     `json:"browserEndpoint"`
	WebClient         string     `json:"webClient"`
	HasCredentials    bool       `json:"hasCredentials"`
	EnvironmentFields []string   `json:"environmentFields"`
	Auth              string     `json:"auth"` // none | local | paired | basic
	ExpiresAt         *time.Time `json:"expiresAt,omitempty"`
	Local             struct {
		Available bool   `json:"available"`
		URL       string `json:"url"`
		Version   string `json:"version"`
		Reason    string `json:"reason"` // "" | missing | invalid | unreachable | environment
	} `json:"local"`
}

// OpenCodeStatus reads the server's OpenCode connection.
func (c *Client) OpenCodeStatus(ctx context.Context) (OpenCodeStatus, error) {
	var out OpenCodeStatus
	err := c.do(ctx, "GET", "/api/opencode/status", nil, &out)
	return out, err
}

// ConnectLocalOpenCode connects the OpenCode service on the server's machine.
// newInstance confirms a different (or earlier) service (409 instance_changed
// / instance_returned without it).
func (c *Client) ConnectLocalOpenCode(ctx context.Context, newInstance bool) error {
	return c.do(ctx, "POST", "/api/opencode/connect-local", map[string]bool{"newInstance": newInstance}, nil)
}

// PairOpenCode connects the service an `opencode pair` link names. After a
// 409 instance_changed, resend the same link with newInstance: the server
// kept the redeemed token.
func (c *Client) PairOpenCode(ctx context.Context, link string, newInstance bool) error {
	return c.do(ctx, "POST", "/api/opencode/pair", map[string]any{"link": link, "newInstance": newInstance}, nil)
}

// DisconnectOpenCode forgets the server's OpenCode address and credentials.
func (c *Client) DisconnectOpenCode(ctx context.Context) error {
	return c.do(ctx, "DELETE", "/api/opencode/settings", nil, nil)
}
