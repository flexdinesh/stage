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

// SyncProgress is the full sync shown in the T3 setup panel.
type SyncProgress struct {
	Running   bool       `json:"running"`
	StartedAt *time.Time `json:"startedAt"`
	Projects  int        `json:"projects"`
	Threads   int        `json:"threads"`
}

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
	LastError            *string      `json:"lastError"`
	LastFullSyncAt       *time.Time   `json:"lastFullSyncAt"`
	Reachable            bool         `json:"reachable"`
	CheckedAt            *time.Time   `json:"checkedAt"`
	ProbeError           *string      `json:"probeError"`
	T3                   *IdentityRef `json:"t3"`
	AutoPair             AutoPairRef  `json:"autoPair"`
	Sync                 SyncProgress `json:"sync"`
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
	// Progress reports the server's full sync (nil: none).
	progress func(connection int64) SyncProgress
}

// NewConnector wraps s; progress (the full sync of a connection) may be nil.
func NewConnector(s *Service, progress func(connection int64) SyncProgress) *Connector {
	return &Connector{svc: s, progress: progress}
}

var _ harness.Connector = (*Connector)(nil)

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// configured: paired, connected or pointed at a chosen URL.
func configured(st Status) bool {
	return st.Conn.HasToken || st.Conn.Connected || st.Endpoint.Source != SourceDefault
}

func (c *Connector) Summary(ctx context.Context) harness.Summary {
	st := c.svc.Status(ctx, false)
	conn, _ := c.svc.o.Settings.GetConnection(ctx)
	out := harness.Summary{ConnectionID: conn.ConnectionID, Configured: configured(st), Connected: st.Conn.Connected, LastError: st.Conn.LastError}
	if out.LastError == "" && conn.LastError != nil {
		out.LastError = *conn.LastError
	}
	return out
}

func (c *Connector) Status(ctx context.Context, refresh bool) (harness.Status, error) {
	st := c.svc.Status(ctx, refresh)
	conn, err := c.svc.o.Settings.GetConnection(ctx)
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
		AdminTokenConfigured: c.svc.o.Config.T3AdminToken != "",
		ConnectionID:         conn.ConnectionID,
		LastError:            strPtr(st.Conn.LastError),
		LastFullSyncAt:       conn.LastFullSyncAt,
		Reachable:            st.Conn.Probe.Reachable,
		ProbeError:           strPtr(st.Conn.Probe.Err),
		AutoPair:             AutoPairRef{Available: st.AutoPair.Available, Reason: strPtr(st.AutoPair.Reason)},
	}
	if st.Conn.ExpiresAt != nil {
		exp := st.Conn.ExpiresAt.UTC()
		d.TokenExpiresAt = &exp
	}
	if d.LastError == nil {
		d.LastError = conn.LastError
	}
	if !st.Conn.Probe.CheckedAt.IsZero() {
		at := st.Conn.Probe.CheckedAt.UTC()
		d.CheckedAt = &at
	}
	if id := st.Conn.Probe.Identity; id != nil {
		d.T3 = &IdentityRef{EnvironmentID: id.EnvironmentID, Label: id.Label, ServerVersion: id.ServerVersion}
	}
	if c.progress != nil {
		d.Sync = c.progress(conn.ConnectionID)
	}
	out := harness.Status{
		Summary: harness.Summary{ConnectionID: conn.ConnectionID, Configured: configured(st), Connected: st.Conn.Connected},
		State:   string(st.State),
		Details: d,
		Fields: []harness.FieldState{{
			ID: "url", Value: st.Endpoint.URL, Set: st.Endpoint.Source != SourceDefault,
			Source: urlSource(st.Endpoint.Source), Locked: st.Endpoint.Source == SourceEnv,
		}},
		Actions: []harness.ActionState{
			{ID: t3.ActionPair, Available: true},
			{ID: t3.ActionAutoPair, Available: st.AutoPair.Available, Reason: st.AutoPair.Reason},
		},
	}
	if d.LastError != nil {
		out.LastError = *d.LastError
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

// Configure takes {"url": string|null}; null goes back to the default.
func (c *Connector) Configure(ctx context.Context, settings json.RawMessage) (harness.Result, error) {
	var body struct {
		URL db.Optional[string] `json:"url"`
	}
	if err := db.DecodeStrict(settings, &body); err != nil {
		return harness.Result{}, err
	}
	if !body.URL.Set {
		return harness.Result{}, db.Invalid("url is required (null resets to the default)")
	}
	return harness.Result{}, c.svc.SetURL(ctx, body.URL.Ptr())
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
	var out harness.Result
	if paired.Replaced != nil {
		out.Replaced = paired.Replaced.ID
	}
	return out, nil
}

// Disconnect forgets the token (Unpair).
func (c *Connector) Disconnect(ctx context.Context) error { return c.svc.Unpair(ctx) }
