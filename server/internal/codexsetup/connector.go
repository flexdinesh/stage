package codexsetup

import (
	"context"
	"encoding/json"

	"github.com/flexdinesh/stage/server/internal/codex"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// Connector is the Codex harness.Connector over a Service.
type Connector struct{ svc *Service }

// NewConnector wraps s.
func NewConnector(s *Service) *Connector { return &Connector{svc: s} }

var _ harness.Connector = (*Connector)(nil)

func (c *Connector) Summary(context.Context) harness.Summary { return c.svc.Summary() }

func (c *Connector) Status(ctx context.Context, refresh bool) (harness.Status, error) {
	state, reason, d := c.svc.Status(ctx, refresh)
	sum := c.svc.Summary()
	src := "saved"
	if d.Binary == "" {
		src = "default"
	}
	return harness.Status{
		Summary: sum,
		State:   state,
		Reason:  reason,
		Fields:  []harness.FieldState{{ID: "binary", Value: d.Binary, Set: d.Binary != "", Source: src}},
		Actions: []harness.ActionState{{ID: codex.ActionConnect, Available: true}},
		Details: d,
	}, nil
}

// Configure takes {"binary": string | null} (null: codex on PATH).
func (c *Connector) Configure(ctx context.Context, settings json.RawMessage) (harness.Result, error) {
	var in struct {
		Binary *string `json:"binary"`
	}
	if err := db.DecodeStrict(settings, &in); err != nil {
		return harness.Result{}, err
	}
	binary := ""
	if in.Binary != nil {
		binary = *in.Binary
	}
	return harness.Result{}, c.svc.SetBinary(ctx, binary)
}

// Run runs connect ({}).
func (c *Connector) Run(ctx context.Context, action string, input json.RawMessage) (harness.Result, error) {
	if action != codex.ActionConnect {
		return harness.Result{}, db.Invalid("%s", harness.UnknownAction(codex.Descriptor, action).Error())
	}
	var in struct{}
	if len(input) > 0 {
		if err := db.DecodeStrict(input, &in); err != nil {
			return harness.Result{}, err
		}
	}
	return c.svc.Connect(ctx)
}

func (c *Connector) Disconnect(ctx context.Context) error { return c.svc.Disconnect(ctx) }
