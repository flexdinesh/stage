package opencodesetup

import (
	"context"
	"encoding/json"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/opencode"
)

// Connector is the OpenCode harness.Connector over this Service.
type Connector struct{ svc *Service }

// NewConnector wraps s.
func NewConnector(s *Service) *Connector { return &Connector{svc: s} }

var _ harness.Connector = (*Connector)(nil)

// Summary: configured while an adapter is installed (memory only; the
// last health check is not repeated).
func (c *Connector) Summary(context.Context) harness.Summary {
	s := c.svc
	s.mu.Lock()
	defer s.mu.Unlock()
	return harness.Summary{ConnectionID: s.id, Configured: s.active != nil, Connected: s.active != nil && s.problem == "", LastError: s.problem}
}

// Status probes the active service (bounded) whatever refresh says: the
// OpenCode panel always shows a fresh health check.
func (c *Connector) Status(ctx context.Context, _ bool) (harness.Status, error) {
	st := c.svc.Status(ctx)
	sum := c.Summary(ctx)
	sum.Connected = st.State == "connected"
	if st.Reason != "" {
		sum.LastError = st.Reason
	}
	field := func(id, value string) harness.FieldState {
		src := "saved"
		if value == "" {
			src = "default"
		}
		return harness.FieldState{ID: id, Value: value, Set: value != "", Source: src}
	}
	creds := field("password", "")
	creds.Set = st.HasCredentials
	user := field("username", "")
	local := harness.ActionState{ID: opencode.ActionConnectLocal, Available: st.Local.Available, Reason: st.Local.problem}
	if !local.Available {
		local.Code = CodeLocalUnavailable
	}
	pair := harness.ActionState{ID: opencode.ActionPair, Available: true}
	phase := harness.PhaseNeedsSetup
	var fix *harness.Fix
	switch st.State {
	case "connected":
		phase = harness.PhaseConnected
	case "rejected":
		phase = harness.PhaseDegraded
		switch st.Auth {
		case AuthLocal:
			fix = &harness.Fix{Text: "Connect the local OpenCode service again to refresh its credentials."}
		case AuthPaired:
			fix = &harness.Fix{Text: "Generate a fresh pairing link on the OpenCode host and pair again.", Command: "opencode pair"}
		default:
			fix = &harness.Fix{Text: "Update the OpenCode credentials or pair with the service again.", Command: "opencode pair"}
		}
	case "unavailable":
		phase = harness.PhaseUnavailable
		fix = &harness.Fix{Text: "Check that the OpenCode service is running and that its address is correct."}
	}
	return harness.Status{
		Summary: sum,
		State:   st.State,
		Reason:  st.Reason,
		Phase:   phase,
		Facts:   statusFacts(st),
		Fix:     fix,
		Fields: []harness.FieldState{
			field("endpoint", st.Endpoint),
			field("browserEndpoint", st.BrowserEndpoint),
			field("webClient", st.WebClient),
			user, creds,
		},
		Actions: []harness.ActionState{local, pair, {ID: opencode.ActionCheck, Available: true}},
		Details: st,
	}, nil
}

func statusFacts(st Status) []execution.Fact {
	var facts []execution.Fact
	browser := st.BrowserEndpoint
	if browser == "" {
		browser = st.Endpoint
	}
	for _, fact := range []execution.Fact{
		{ID: "endpoint", Label: "Service URL", Value: st.Endpoint, Copyable: true},
		{ID: "browserEndpoint", Label: "Browser URL", Value: browser, Link: browser, Copyable: true},
		{ID: "webClient", Label: "Web client URL", Value: st.WebClient, Link: st.WebClient, Copyable: true},
		{ID: "localVersion", Label: "Local service version", Value: st.Local.Version},
	} {
		if fact.Value != "" {
			facts = append(facts, fact)
		}
	}
	if st.Endpoint != "" {
		signIn := map[string]string{
			AuthNone: "None", AuthLocal: "Local service", AuthPaired: "Paired", AuthBasic: "Password",
		}[st.Auth]
		if signIn != "" {
			facts = append(facts, execution.Fact{ID: "auth", Label: "Sign-in", Value: signIn})
		}
	}
	if st.ExpiresAt != nil {
		facts = append(facts, execution.Fact{ID: "expiresAt", Label: "Token expires", Value: st.ExpiresAt.Format(time.RFC3339)})
	}
	if st.CheckedAt != nil {
		facts = append(facts, execution.Fact{ID: "checkedAt", Label: "Last checked", Value: st.CheckedAt.Format(time.RFC3339)})
	}
	return facts
}

// Configure takes Edit.
func (c *Connector) Configure(ctx context.Context, settings json.RawMessage) (harness.Result, error) {
	var in Edit
	if err := db.DecodeStrict(settings, &in); err != nil {
		return harness.Result{}, err
	}
	old := c.svc.ID()
	if err := c.svc.Save(ctx, in); err != nil {
		return harness.Result{}, err
	}
	if c.svc.ID() != old {
		return harness.Result{Replaced: old}, nil
	}
	return harness.Result{}, nil
}

// Run runs connect-local, pair or check.
func (c *Connector) Run(ctx context.Context, action string, input json.RawMessage) (harness.Result, error) {
	old := c.svc.ID()
	var result harness.Result
	switch action {
	case opencode.ActionConnectLocal:
		var in struct {
			NewInstance bool `json:"newInstance"`
		}
		if err := db.DecodeStrict(input, &in); err != nil {
			return harness.Result{}, err
		}
		if err := c.svc.ConnectLocal(ctx, in.NewInstance); err != nil {
			return harness.Result{}, err
		}
		result.Message = "Connected to the local OpenCode service."
	case opencode.ActionPair:
		var in struct {
			Link        string `json:"link"`
			NewInstance bool   `json:"newInstance"`
		}
		if err := db.DecodeStrict(input, &in); err != nil {
			return harness.Result{}, err
		}
		if err := c.svc.Pair(ctx, in.Link, in.NewInstance); err != nil {
			return harness.Result{}, err
		}
		result.Message = "Paired with OpenCode."
	case opencode.ActionCheck:
		if err := db.DecodeStrict(input, &struct{}{}); err != nil {
			return harness.Result{}, err
		}
		status, err := c.Status(ctx, true)
		return status.CheckResult(), err
	default:
		return harness.Result{}, db.Invalid("%s", harness.UnknownAction(opencode.Descriptor, action).Error())
	}
	if c.svc.ID() != old {
		result.Replaced = old
	}
	return result, nil
}

// Disconnect forgets the service address and credentials.
func (c *Connector) Disconnect(ctx context.Context) error { return c.svc.Disconnect(ctx) }
