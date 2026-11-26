package codexsetup

import (
	"context"
	"encoding/json"
	"time"

	"github.com/flexdinesh/stage/server/internal/codex"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
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
	phase := harness.PhaseDisconnected
	switch state {
	case StateConnected:
		phase = harness.PhaseConnected
	case StateSignedOut:
		phase = harness.PhaseNeedsSetup
		if sum.Configured {
			phase = harness.PhaseDegraded
		}
	case StateNotInstalled, StateUnreachable:
		phase = harness.PhaseUnavailable
	}
	var fix *harness.Fix
	if d.Fix != "" {
		note := "Start the Codex daemon, then check the connection again."
		if state == StateSignedOut {
			note = "Sign in to Codex, then connect again."
		}
		fix = &harness.Fix{Text: note, Command: d.Fix}
	}
	return harness.Status{
		Summary: sum,
		State:   state,
		Reason:  reason,
		Phase:   phase,
		Facts:   statusFacts(state, d),
		Fix:     fix,
		Fields:  []harness.FieldState{{ID: "binary", Value: d.Binary, Set: d.Binary != "", Source: src}},
		Actions: []harness.ActionState{{ID: codex.ActionConnect, Available: true}, {ID: codex.ActionCheck, Available: true}},
		Details: d,
	}, nil
}

func statusFacts(state string, d Details) []execution.Fact {
	var facts []execution.Fact
	for _, fact := range []execution.Fact{
		{ID: "cliVersion", Label: "Codex CLI version", Value: d.CLIVersion},
		{ID: "appServerVersion", Label: "App server version", Value: d.AppServerVersion},
		{ID: "codexHome", Label: "CODEX_HOME", Value: d.CodexHome, Copyable: true},
	} {
		if fact.Value != "" {
			facts = append(facts, fact)
		}
	}
	switch d.Daemon {
	case "running":
		facts = append(facts, execution.Fact{ID: "daemon", Label: "Daemon", Value: d.Daemon, Tone: execution.ToneOK})
	case "stopped":
		facts = append(facts, execution.Fact{ID: "daemon", Label: "Daemon", Value: d.Daemon, Tone: execution.ToneWarn})
	}
	if a := d.Account; a != nil {
		account := a.Type
		switch a.Type {
		case "chatgpt":
			account = "ChatGPT"
			if a.Email != nil && *a.Email != "" {
				account = *a.Email
			}
			if a.Plan != nil && *a.Plan != "" {
				account += " · " + *a.Plan
			}
		case "apiKey":
			account = "API key"
		case "amazonBedrock":
			account = "Amazon Bedrock"
		case "gcp":
			account = "Google Cloud"
		}
		facts = append(facts, execution.Fact{ID: "account", Label: "Account", Value: account, Tone: execution.ToneOK})
	} else if state == StateSignedOut {
		facts = append(facts, execution.Fact{ID: "account", Label: "Account", Value: "Not signed in", Tone: execution.ToneWarn})
	}
	if d.CheckedAt != nil {
		facts = append(facts, execution.Fact{ID: "checkedAt", Label: "Last checked", Value: d.CheckedAt.Format(time.RFC3339)})
	}
	return facts
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

// Run runs connect or check.
func (c *Connector) Run(ctx context.Context, action string, input json.RawMessage) (harness.Result, error) {
	if action != codex.ActionConnect && action != codex.ActionCheck {
		return harness.Result{}, db.Invalid("%s", harness.UnknownAction(codex.Descriptor, action).Error())
	}
	var in struct{}
	if len(input) > 0 {
		if err := db.DecodeStrict(input, &in); err != nil {
			return harness.Result{}, err
		}
	}
	if action == codex.ActionCheck {
		status, err := c.Status(ctx, true)
		return status.CheckResult(), err
	}
	res, err := c.svc.Connect(ctx)
	if err == nil {
		res.Message = "Connected to Codex."
	}
	return res, err
}

func (c *Connector) Disconnect(ctx context.Context) error { return c.svc.Disconnect(ctx) }
