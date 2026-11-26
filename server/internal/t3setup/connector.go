package t3setup

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// Details is the T3 setup panel's state (harness.Status.Details; docs/api.md
// "T3 connection").
type Details struct {
	Connected            bool         `json:"connected"`
	State                t3.State     `json:"state"`
	T3URL                string       `json:"t3Url"`
	PublicURL            string       `json:"publicUrl"`
	T3URLSource          string       `json:"t3UrlSource"`
	EnvironmentID        *string      `json:"environmentId"`
	ClientID             *string      `json:"clientId"`
	TokenExpiresAt       *time.Time   `json:"tokenExpiresAt"`
	AdminTokenConfigured bool         `json:"adminTokenConfigured"`
	Reachable            bool         `json:"reachable"`
	CheckedAt            *time.Time   `json:"checkedAt"`
	ProbeError           *string      `json:"probeError"`
	T3                   *IdentityRef `json:"t3"`
	AutoPair             AutoPairRef  `json:"autoPair"`
	ConnectionID         int64        `json:"connectionId"`
}

// IdentityRef is the probed T3 server.
type IdentityRef struct {
	EnvironmentID string `json:"environmentId"`
	Label         string `json:"label"`
	ServerVersion string `json:"serverVersion"`
}

// AutoPairRef is whether auto-pair can work.
type AutoPairRef struct {
	Available bool    `json:"available"`
	Reason    *string `json:"reason"`
}

// Connector is the T3 harness.Connector over this Service.
type Connector struct {
	svc *Service
}

// NewConnector wraps s.
func NewConnector(s *Service) *Connector {
	return &Connector{svc: s}
}

var _ harness.Connector = (*Connector)(nil)

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// configured: paired (a stored token, or an admin token that mints one).
// An unpaired T3 is not a connection, whatever URL is saved.
func (c *Connector) summary(active int64, st t3.ConnectionStatus) harness.Summary {
	return harness.Summary{
		ConnectionID: active,
		Configured:   st.HasToken || st.Connected || c.svc.AdminTokenSet(),
		Connected:    st.Connected,
		LastError:    st.LastError,
	}
}

func (c *Connector) Summary(ctx context.Context) harness.Summary {
	st := c.svc.o.Client.Status(ctx)
	active, _ := c.svc.o.Identity.Active(ctx, t3.Kind)
	return c.summary(active, st)
}

func (c *Connector) Status(ctx context.Context, refresh bool) (harness.Status, error) {
	st := c.svc.Status(ctx, refresh)
	active, err := c.svc.o.Identity.Active(ctx, t3.Kind)
	if err != nil {
		return harness.Status{}, err
	}
	d := Details{
		Connected:            st.Conn.Connected,
		State:                st.State,
		T3URL:                st.Endpoint.URL,
		PublicURL:            st.Endpoint.PublicURL,
		T3URLSource:          st.Endpoint.Source,
		EnvironmentID:        strPtr(st.Conn.EnvironmentID),
		ClientID:             strPtr(st.Conn.ClientID),
		AdminTokenConfigured: c.svc.AdminTokenSet(),
		ConnectionID:         active,
		Reachable:            st.Conn.Probe.Reachable,
		ProbeError:           strPtr(st.Conn.Probe.Err),
		AutoPair:             AutoPairRef{Available: st.AutoPair.Available, Reason: strPtr(st.AutoPair.Reason)},
	}
	if st.Conn.ExpiresAt != nil {
		exp := st.Conn.ExpiresAt.UTC()
		d.TokenExpiresAt = &exp
	}
	if !st.Conn.Probe.CheckedAt.IsZero() {
		at := st.Conn.Probe.CheckedAt.UTC()
		d.CheckedAt = &at
	}
	if id := st.Conn.Probe.Identity; id != nil {
		d.T3 = &IdentityRef{EnvironmentID: id.EnvironmentID, Label: id.Label, ServerVersion: id.ServerVersion}
	}
	out := harness.Status{
		Summary: c.summary(active, st.Conn),
		State:   string(st.State),
		Details: d,
		Fields: []harness.FieldState{
			{ID: "url", Value: st.Endpoint.URL, Set: st.Endpoint.Source != SourceDefault, Source: urlSource(st.Endpoint.Source)},
			{ID: "publicUrl", Value: publicValue(st.Endpoint), Set: st.Endpoint.PublicSet, Source: setSource(st.Endpoint.PublicSet)},
			{ID: "adminToken", Set: d.AdminTokenConfigured, Source: setSource(d.AdminTokenConfigured)},
		},
		Actions: []harness.ActionState{
			{ID: t3.ActionPair, Available: true},
			{ID: t3.ActionAutoPair, Available: st.AutoPair.Available, Reason: st.AutoPair.Reason},
		},
	}
	if st.State != t3.StateConnected {
		out.Reason = out.LastError
	}
	return out, nil
}

// urlSource maps the endpoint source to a field source.
func urlSource(s string) string {
	if s == SourceSettings {
		return "saved"
	}
	return s
}

// setSource is a field's source from whether it is saved.
func setSource(set bool) string {
	if set {
		return "saved"
	}
	return "default"
}

// publicValue is the saved browser URL ("" when it follows the T3 URL).
func publicValue(ep Endpoint) string {
	if ep.PublicSet {
		return ep.PublicURL
	}
	return ""
}

// Configure takes any of {"url", "publicUrl", "adminToken"} (at least one):
// a string sets it, null clears it (url: the default; publicUrl: the T3
// URL; adminToken: none). They apply in that order; the first failure
// stops.
func (c *Connector) Configure(ctx context.Context, settings json.RawMessage) (harness.Result, error) {
	var body struct {
		URL        db.Optional[string] `json:"url"`
		PublicURL  db.Optional[string] `json:"publicUrl"`
		AdminToken db.Optional[string] `json:"adminToken"`
	}
	if err := db.DecodeStrict(settings, &body); err != nil {
		return harness.Result{}, err
	}
	if !body.URL.Set && !body.PublicURL.Set && !body.AdminToken.Set {
		return harness.Result{}, db.Invalid("set url, publicUrl or adminToken (null clears it)")
	}
	if body.URL.Set {
		if err := c.svc.SetURL(ctx, body.URL.Ptr()); err != nil {
			return harness.Result{}, err
		}
	}
	if body.PublicURL.Set {
		if err := c.svc.SetPublicURL(ctx, body.PublicURL.Ptr()); err != nil {
			return harness.Result{}, err
		}
	}
	if body.AdminToken.Set {
		tok := ""
		if p := body.AdminToken.Ptr(); p != nil {
			tok = *p
		}
		if err := c.svc.SetAdminToken(ctx, tok); err != nil {
			return harness.Result{}, err
		}
	}
	return harness.Result{}, nil
}

// Run runs pair {"code"} or auto-pair {}.
func (c *Connector) Run(ctx context.Context, action string, input json.RawMessage) (harness.Result, error) {
	var paired Paired
	var err error
	switch action {
	case t3.ActionPair:
		var body struct {
			Code string `json:"code"`
		}
		if err = db.DecodeStrict(input, &body); err != nil {
			return harness.Result{}, err
		}
		code := strings.TrimSpace(body.Code)
		if code == "" {
			return harness.Result{}, db.Invalid("code is required")
		}
		paired, err = c.svc.Pair(ctx, code)
	case t3.ActionAutoPair:
		if err = db.DecodeStrict(input, &struct{}{}); err != nil {
			return harness.Result{}, err
		}
		paired, err = c.svc.AutoPair(ctx)
	default:
		return harness.Result{}, db.Invalid("%s", harness.UnknownAction(t3.Descriptor, action).Error())
	}
	if err != nil {
		return harness.Result{}, err
	}
	return harness.Result{Replaced: paired.Replaced}, nil
}

// Disconnect forgets the token and the saved settings (Unpair).
func (c *Connector) Disconnect(ctx context.Context) error { return c.svc.Unpair(ctx) }
