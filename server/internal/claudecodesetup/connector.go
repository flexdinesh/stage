package claudecodesetup

import (
	"context"
	"encoding/json"

	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// Connector is the Claude Code harness.Connector over a Service.
type Connector struct{ svc *Service }

// NewConnector wraps s.
func NewConnector(s *Service) *Connector { return &Connector{svc: s} }

var _ harness.Connector = (*Connector)(nil)

func (c *Connector) Summary(context.Context) harness.Summary { return c.svc.Summary() }

func (c *Connector) Status(ctx context.Context, refresh bool) (harness.Status, error) {
	return c.svc.Status(ctx, refresh), nil
}

// Configure takes {"binary": string|null}.
func (c *Connector) Configure(ctx context.Context, settings json.RawMessage) (harness.Result, error) {
	return harness.Result{}, c.svc.Configure(ctx, settings)
}

// Run runs connect or check.
func (c *Connector) Run(ctx context.Context, action string, input json.RawMessage) (harness.Result, error) {
	if action != claudecode.ActionConnect && action != claudecode.ActionCheck {
		return harness.Result{}, db.Invalid("%s", harness.UnknownAction(claudecode.Descriptor, action).Error())
	}
	var in struct{}
	if err := db.DecodeStrict(input, &in); err != nil {
		return harness.Result{}, err
	}
	if action == claudecode.ActionCheck {
		c.svc.probed.Forget() // an explicit check probes the facts again
		return c.svc.Status(ctx, true).CheckResult(), nil
	}
	res, err := c.svc.Connect(ctx)
	if err == nil {
		res.Message = "Connected to Claude Code."
	}
	return res, err
}

func (c *Connector) Disconnect(ctx context.Context) error { return c.svc.Disconnect(ctx) }
