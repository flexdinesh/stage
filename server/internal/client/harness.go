package client

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/flexdinesh/stage/server/internal/harness"
)

// SetupField is a harness setup setting or action input (docs/api.md
// "Harness connections").
type SetupField struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Kind  string `json:"kind"` // text | url | secret | bool
	Env   string `json:"env,omitempty"`
	Help  string `json:"help,omitempty"`
}

// SetupAction is a named setup operation.
type SetupAction struct {
	ID    string       `json:"id"`
	Label string       `json:"label"`
	Input []SetupField `json:"input,omitempty"`
}

// HarnessDescriptor is one entry of GET /api/harnesses.
type HarnessDescriptor struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Hosting     string `json:"hosting"`
	Available   bool   `json:"available"`
	Reason      string `json:"reason,omitempty"`
	Connectable bool   `json:"connectable"`
	SessionHint string `json:"sessionHint,omitempty"`
	Attribution struct {
		MetaKeys   []string            `json:"metaKeys"`
		Env        string              `json:"env,omitempty"`
		Precedence *harness.Precedence `json:"precedence,omitempty"`
	} `json:"attribution"`
	Setup struct {
		Fields     []SetupField  `json:"fields"`
		Actions    []SetupAction `json:"actions"`
		Disconnect bool          `json:"disconnect"`
	} `json:"setup"`
}

// Harnesses is GET /api/harnesses.
type Harnesses struct {
	Machine struct {
		ID      int64  `json:"id"`
		Label   string `json:"label"`
		Profile struct {
			Kind           string `json:"kind"`
			LocalProcesses bool   `json:"localProcesses"`
		} `json:"profile"`
	} `json:"machine"`
	Harnesses []HarnessDescriptor `json:"harnesses"`
}

// Lookup finds kind.
func (h Harnesses) Lookup(kind string) (HarnessDescriptor, bool) {
	for _, d := range h.Harnesses {
		if d.Kind == kind {
			return d, true
		}
	}
	return HarnessDescriptor{}, false
}

// HarnessStatus is GET /api/harnesses/{kind}/status.
type HarnessStatus struct {
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	ConnectionID int64  `json:"connectionId"`
	Configured   bool   `json:"configured"`
	Connected    bool   `json:"connected"`
	LastError    string `json:"lastError,omitempty"`
	State        string `json:"state"`
	Reason       string `json:"reason,omitempty"`
	Fields       []struct {
		ID     string `json:"id"`
		Value  string `json:"value"`
		Set    bool   `json:"set"`
		Source string `json:"source"`
		Locked bool   `json:"locked"`
	} `json:"fields"`
	Actions []struct {
		ID        string `json:"id"`
		Available bool   `json:"available"`
		Reason    string `json:"reason,omitempty"`
	} `json:"actions"`
	// Details is the harness's own setup state (see the harness's docs).
	Details  json.RawMessage `json:"details,omitempty"`
	Replaced json.RawMessage `json:"replaced,omitempty"`
}

// Harnesses lists the harness kinds and this machine's profile.
func (c *Client) Harnesses(ctx context.Context) (Harnesses, error) {
	var out Harnesses
	err := c.do(ctx, "GET", "/api/harnesses", nil, &out)
	return out, err
}

func harnessPath(kind, rest string) string { return "/api/harnesses/" + url.PathEscape(kind) + rest }

// HarnessStatus reads kind's connection setup (refreshing a stale probe).
func (c *Client) HarnessStatus(ctx context.Context, kind string) (HarnessStatus, error) {
	var out HarnessStatus
	err := c.do(ctx, "GET", harnessPath(kind, "/status"), nil, &out)
	return out, err
}

// ConfigureHarness saves settings (field id: value, nil resets).
func (c *Client) ConfigureHarness(ctx context.Context, kind string, settings map[string]any) (HarnessStatus, error) {
	var out HarnessStatus
	err := c.do(ctx, "PUT", harnessPath(kind, "/settings"), settings, &out)
	return out, err
}

// RunHarnessAction runs a setup action with its input.
func (c *Client) RunHarnessAction(ctx context.Context, kind, action string, input map[string]any) (HarnessStatus, error) {
	var out HarnessStatus
	err := c.do(ctx, "POST", harnessPath(kind, "/actions/"+url.PathEscape(action)), input, &out)
	return out, err
}

// DisconnectHarness forgets kind's connection credentials (tasks stay).
func (c *Client) DisconnectHarness(ctx context.Context, kind string) (HarnessStatus, error) {
	var out HarnessStatus
	err := c.do(ctx, "DELETE", harnessPath(kind, "/connection"), nil, &out)
	return out, err
}
