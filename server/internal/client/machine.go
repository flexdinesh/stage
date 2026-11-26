package client

import (
	"context"
	"strconv"
	"time"
)

// Machine is an entry of GET /api/machines (docs/api.md "Machines").
type Machine struct {
	ID           int64      `json:"id"`
	Label        string     `json:"label"`
	Kind         string     `json:"kind"` // local | remote
	Online       bool       `json:"online"`
	Status       string     `json:"status"` // online | offline | negotiating | needs_update
	StatusReason string     `json:"statusReason,omitempty"`
	LastSeenAt   *time.Time `json:"lastSeenAt"`
	Version      *string    `json:"version"`
	CreatedBy    int64      `json:"createdBy"`
	CreatedAt    time.Time  `json:"createdAt"`
	Connections  []struct {
		ID      int64  `json:"id"`
		Harness string `json:"harness"`
		Label   string `json:"label"`
	} `json:"connections"`
}

// Pairing is POST /api/machines/pairings.
type Pairing struct {
	Code      string    `json:"code"`
	Label     string    `json:"label"`
	ExpiresAt time.Time `json:"expiresAt"`
	Link      string    `json:"link"`
	Command   string    `json:"command"`
}

func machinePath(id int64) string { return "/api/machines/" + strconv.FormatInt(id, 10) }

// Machines lists the machines that run harnesses for this server.
func (c *Client) Machines(ctx context.Context) ([]Machine, error) {
	var out struct {
		Machines []Machine `json:"machines"`
	}
	err := c.do(ctx, "GET", "/api/machines", nil, &out)
	return out.Machines, err
}

// RenameMachine sets a machine's label.
func (c *Client) RenameMachine(ctx context.Context, id int64, label string) (Machine, error) {
	var out Machine
	err := c.do(ctx, "PATCH", machinePath(id), map[string]string{"label": label}, &out)
	return out, err
}

// RemoveMachine removes a remote machine and revokes its token.
func (c *Client) RemoveMachine(ctx context.Context, id int64) error {
	return c.do(ctx, "DELETE", machinePath(id), nil, nil)
}

// PairMachine creates a one-time pairing code (label optional).
func (c *Client) PairMachine(ctx context.Context, label string) (Pairing, error) {
	var out Pairing
	err := c.do(ctx, "POST", "/api/machines/pairings", map[string]string{"label": label}, &out)
	return out, err
}

// Ping is POST /api/machines/{id}/ping.
type Ping struct {
	MachineID int64  `json:"machineId"`
	LatencyMs int64  `json:"latencyMs"`
	Version   string `json:"version"`
}

// PingMachine round-trips through a machine's worker channel.
func (c *Client) PingMachine(ctx context.Context, id int64) (Ping, error) {
	var out Ping
	err := c.do(ctx, "POST", machinePath(id)+"/ping", nil, &out)
	return out, err
}
