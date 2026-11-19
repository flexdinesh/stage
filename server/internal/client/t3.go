package client

import (
	"context"
	"time"
)

// T3Status is GET /api/t3/status (docs/api.md).
type T3Status struct {
	Connected            bool       `json:"connected"`
	State                string     `json:"state"` // unreachable | unpaired | expired | rejected | connected
	T3URL                string     `json:"t3Url"`
	PublicURL            string     `json:"publicUrl"`
	T3URLSource          string     `json:"t3UrlSource"` // env | settings | default
	EnvironmentID        *string    `json:"environmentId"`
	ClientID             *string    `json:"clientId"`
	TokenExpiresAt       *time.Time `json:"tokenExpiresAt"`
	AdminTokenConfigured bool       `json:"adminTokenConfigured"`
	LastError            *string    `json:"lastError"`
	LastFullSyncAt       *time.Time `json:"lastFullSyncAt"`
	Reachable            bool       `json:"reachable"`
	CheckedAt            *time.Time `json:"checkedAt"`
	ProbeError           *string    `json:"probeError"`
	T3                   *struct {
		EnvironmentID string `json:"environmentId"`
		Label         string `json:"label"`
		ServerVersion string `json:"serverVersion"`
	} `json:"t3"`
	AutoPair struct {
		Available bool    `json:"available"`
		Reason    *string `json:"reason"`
	} `json:"autoPair"`
	Sync struct {
		Running   bool       `json:"running"`
		StartedAt *time.Time `json:"startedAt"`
		Projects  int        `json:"projects"`
		Threads   int        `json:"threads"`
	} `json:"sync"`
}

// T3Status reads the T3 connection (refreshing a stale reachability probe).
func (c *Client) T3Status(ctx context.Context) (T3Status, error) {
	var out T3Status
	err := c.do(ctx, "GET", "/api/t3/status", nil, &out)
	return out, err
}

// PairT3 redeems a pairing code from `t3 pair`.
func (c *Client) PairT3(ctx context.Context, code string) (T3Status, error) {
	var out T3Status
	err := c.do(ctx, "POST", "/api/t3/pair", map[string]string{"code": code}, &out)
	return out, err
}

// AutoPairT3 pairs through the t3 CLI on the server's machine.
func (c *Client) AutoPairT3(ctx context.Context) (T3Status, error) {
	var out T3Status
	err := c.do(ctx, "POST", "/api/t3/pair/auto", nil, &out)
	return out, err
}

// DisconnectT3 forgets the server's T3 token and environment binding.
func (c *Client) DisconnectT3(ctx context.Context) (T3Status, error) {
	var out T3Status
	err := c.do(ctx, "DELETE", "/api/t3/pair", nil, &out)
	return out, err
}

// SetT3URL saves the server's T3 URL; nil resets it to the default.
func (c *Client) SetT3URL(ctx context.Context, url *string) (T3Status, error) {
	var out T3Status
	err := c.do(ctx, "PUT", "/api/t3/url", map[string]*string{"url": url}, &out)
	return out, err
}
