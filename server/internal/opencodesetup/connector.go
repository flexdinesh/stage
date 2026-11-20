package opencodesetup

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
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
	field := func(id, value string, envKey string) harness.FieldState {
		locked := slices.Contains(st.EnvironmentFields, envKey)
		src := "saved"
		switch {
		case locked:
			src = "env"
		case value == "":
			src = "default"
		}
		return harness.FieldState{ID: id, Value: value, Set: value != "", Source: src, Locked: locked}
	}
	creds := field("password", "", "credentials")
	creds.Set = st.HasCredentials
	user := field("username", "", "credentials")
	local := harness.ActionState{ID: opencode.ActionConnectLocal, Available: st.Local.Available, Reason: st.Local.Reason}
	pair := harness.ActionState{ID: opencode.ActionPair, Available: true}
	if slices.Contains(st.EnvironmentFields, "endpoint") {
		local.Available, local.Reason = false, "environment"
		pair.Available, pair.Reason = false, "environment"
	}
	return harness.Status{
		Summary: sum,
		State:   st.State,
		Reason:  st.Reason,
		Fields: []harness.FieldState{
			field("endpoint", st.Endpoint, "endpoint"),
			field("browserEndpoint", st.BrowserEndpoint, "browserEndpoint"),
			field("webClient", st.WebClient, "webClient"),
			user, creds,
		},
		Actions: []harness.ActionState{local, pair},
		Details: st,
	}, nil
}

// Configure takes config.OpenCodeEdit.
func (c *Connector) Configure(ctx context.Context, settings json.RawMessage) (harness.Result, error) {
	var in config.OpenCodeEdit
	if err := db.DecodeStrict(settings, &in); err != nil {
		return harness.Result{}, err
	}
	return harness.Result{}, c.svc.Save(ctx, in)
}

// Run runs connect-local {"newInstance"} or pair {"link","newInstance"}.
func (c *Connector) Run(ctx context.Context, action string, input json.RawMessage) (harness.Result, error) {
	switch action {
	case opencode.ActionConnectLocal:
		var in struct {
			NewInstance bool `json:"newInstance"`
		}
		if err := db.DecodeStrict(input, &in); err != nil {
			return harness.Result{}, err
		}
		return harness.Result{}, c.svc.ConnectLocal(ctx, in.NewInstance)
	case opencode.ActionPair:
		var in struct {
			Link        string `json:"link"`
			NewInstance bool   `json:"newInstance"`
		}
		if err := db.DecodeStrict(input, &in); err != nil {
			return harness.Result{}, err
		}
		return harness.Result{}, c.svc.Pair(ctx, in.Link, in.NewInstance)
	}
	return harness.Result{}, db.Invalid("%s", harness.UnknownAction(opencode.Descriptor, action).Error())
}

// Disconnect forgets the service address and credentials.
func (c *Connector) Disconnect(ctx context.Context) error { return c.svc.Disconnect(ctx) }
