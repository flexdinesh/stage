// Package t3setup owns how this Stage server reaches its one T3 backend:
// the effective T3 URL (the saved setting, else the default), the browser
// URL and the admin token (Settings; never the environment),
// the environment binding, pairing (pasted code or auto-pair through the
// local t3 CLI) and the status the setup UI shows (docs/api.md "T3
// connection").
//
// Instances: each T3 environment is its own harness connection. Pairing
// stamps the active T3 connection with the environment it reached; pairing
// a different environment retires that connection (its tasks, sessions and
// bindings stay on it, read-only) and makes a new one active, or revives the
// retired connection of that environment. While paired, the URL may change
// only to an address serving the same environment. Unpair (disconnect)
// forgets the token and the saved settings (URL, browser URL, admin token);
// the connection keeps its environment, so pairing it again resumes it.
package t3setup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// Where the T3 URL comes from.
const (
	SourceSettings = "settings" // saved through the url setting
	SourceDefault  = "default"  // config.DefaultT3URL
)

// StatusProbeTimeout bounds the probe refresh of a status read.
const StatusProbeTimeout = 3 * time.Second

// Endpoint is the T3 address in use.
type Endpoint struct {
	URL       string // server-side MCP/OAuth calls
	PublicURL string // browser "Open in T3" links
	Source    string // SourceSettings | SourceDefault
	// PublicSet: PublicURL is the saved browser URL (else the T3 URL).
	PublicSet bool
}

// Settings is the machine's saved T3 setup (non-secret: the URL and
// browser URL settings and the active connection's environment). The T3
// token is the client's credential store (t3.TokenStore).
type Settings interface {
	GetConnection(ctx context.Context) (db.Connection, error)
	SetT3URL(ctx context.Context, u string) error
	SetT3PublicURL(ctx context.Context, u string) error
}

// AdminTokens stores the machine's T3 admin token (a secret: where the
// harness runs, like the T3 token).
type AdminTokens interface {
	LoadT3AdminToken(ctx context.Context) (string, error)
	SaveT3AdminToken(ctx context.Context, tok string) error
}

// Identity is the main server's record of the machine's T3 connections.
type Identity interface {
	SwitchInstance(ctx context.Context, kind, identity, label string, revive int64) (db.Switch, error)
	GetHarnessConnection(ctx context.Context, id int64) (db.HarnessConnection, error)
}

// Resolve is the endpoint of the saved settings (conn.T3URL, else the
// default; conn.T3PublicURL, else the T3 URL).
func Resolve(conn db.Connection) Endpoint {
	ep := Endpoint{URL: config.DefaultT3URL, Source: SourceDefault}
	if conn.T3URL != nil && *conn.T3URL != "" {
		ep.URL, ep.Source = *conn.T3URL, SourceSettings
	}
	ep.URL = strings.TrimRight(ep.URL, "/")
	ep.PublicURL = ep.URL
	if conn.T3PublicURL != nil && *conn.T3PublicURL != "" {
		ep.PublicURL, ep.PublicSet = strings.TrimRight(*conn.T3PublicURL, "/"), true
	}
	return ep
}

// NormalizeURL validates a T3 URL setting: http or https, a host, no
// credentials, query or fragment, no path besides "/". It returns it
// without the trailing slash.
func NormalizeURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	u, err := url.Parse(s)
	if err != nil || s == "" {
		return "", errors.New("url must be an http(s) URL like http://127.0.0.1:3773")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("url must start with http:// or https://")
	}
	if u.Host == "" || u.Hostname() == "" {
		return "", errors.New("url needs a host")
	}
	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("url must be only scheme://host[:port], with no path, query or credentials")
	}
	return strings.ToLower(u.Scheme) + "://" + u.Host, nil
}

// Options configure New.
type Options struct {
	Config   config.Config
	Settings Settings
	Identity Identity
	Client   t3.Client
	Endpoint Endpoint // the endpoint the client was built with (Resolve)
	// AdminTokens stores the admin token; AdminToken is the one the client
	// was built with ("" when none).
	AdminTokens AdminTokens
	AdminToken  string
	// Fake: Client is the in-memory fake (no auto-pair; any URL is it).
	Fake bool
	// OnPaired runs after every successful pairing (full sync now). Must
	// not block.
	OnPaired func()
	// OnSwitched runs when pairing moved T3 to another connection, or
	// created the machine's first one (the adapter must follow before
	// OnPaired). Must not block.
	OnSwitched func(db.Switch)
	Logger     *slog.Logger
	// Local finds and runs the t3 CLI on the machine T3 runs on; nil is
	// LocalCLI with Config.T3Bin.
	Local Local
	// ProbeURL probes a candidate URL; nil = t3.ProbeURL with a 3s client.
	ProbeURL func(ctx context.Context, base string) t3.Probe
}

// Service is the T3 setup of this server. Safe for concurrent use.
type Service struct {
	o   Options
	log *slog.Logger

	opMu sync.Mutex // serializes URL changes and pairings

	mu    sync.Mutex
	ep    Endpoint
	admin bool // an admin token is saved
}

// New builds the service around a client already configured for
// o.Endpoint.
func New(o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Endpoint.URL == "" {
		o.Endpoint = Resolve(db.Connection{})
	}
	if o.Local == nil {
		o.Local = &LocalCLI{Bin: o.Config.T3Bin}
	}
	if o.ProbeURL == nil {
		hc := &http.Client{Timeout: StatusProbeTimeout}
		o.ProbeURL = func(ctx context.Context, base string) t3.Probe { return t3.ProbeURL(ctx, hc, base) }
	}
	return &Service{o: o, log: o.Logger.With("component", "t3setup"), ep: o.Endpoint, admin: o.AdminToken != ""}
}

// AdminTokenSet reports whether an admin token is saved.
func (s *Service) AdminTokenSet() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.admin
}

// Endpoint is the T3 address in use now.
func (s *Service) Endpoint() Endpoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ep
}

// Status is the setup state (Connector.Status details).
type Status struct {
	Conn     t3.ConnectionStatus
	State    t3.State
	Endpoint Endpoint
	AutoPair AutoPair
}

// Status reports the setup state. refresh: refresh a stale probe first
// (bounded by StatusProbeTimeout); otherwise memory only.
func (s *Service) Status(ctx context.Context, refresh bool) Status {
	if refresh {
		pctx, cancel := context.WithTimeout(ctx, StatusProbeTimeout)
		s.o.Client.Probe(pctx)
		cancel()
	}
	st := s.o.Client.Status(ctx)
	if refresh {
		s.bindLegacy(ctx, st)
	}
	return Status{Conn: st, State: st.State(), Endpoint: s.Endpoint(), AutoPair: s.autoPair()}
}

// SetURL saves the T3 URL setting (nil: back to the default) and points the
// client at it. 409 when the server is bound to a T3 environment the new
// URL does not serve; 400 for an invalid URL.
func (s *Service) SetURL(ctx context.Context, raw *string) error {
	target, saved, source := config.DefaultT3URL, "", SourceDefault
	if raw != nil {
		u, err := NormalizeURL(*raw)
		if err != nil {
			return db.Invalid("%s", err.Error())
		}
		target, saved, source = u, u, SourceSettings
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if err := s.checkBinding(ctx, target); err != nil {
		return err
	}
	if err := s.o.Settings.SetT3URL(ctx, saved); err != nil {
		return err
	}
	s.mu.Lock()
	ep := s.ep
	ep.URL, ep.Source = target, source
	if !ep.PublicSet {
		ep.PublicURL = target
	}
	s.ep = ep
	s.mu.Unlock()
	s.o.Client.Reconfigure(ep.URL, ep.PublicURL)
	s.log.Info("t3 url set", "url", ep.URL, "source", ep.Source)
	return nil
}

// SetPublicURL saves the browser URL setting (nil: the T3 URL). 400 for an
// invalid URL.
func (s *Service) SetPublicURL(ctx context.Context, raw *string) error {
	saved := ""
	if raw != nil {
		u, err := NormalizeURL(*raw)
		if err != nil {
			return db.Invalid("publicUrl: %s", err.Error())
		}
		saved = u
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if err := s.o.Settings.SetT3PublicURL(ctx, saved); err != nil {
		return err
	}
	s.mu.Lock()
	ep := s.ep
	ep.PublicURL, ep.PublicSet = saved, saved != ""
	if saved == "" {
		ep.PublicURL = ep.URL
	}
	s.ep = ep
	s.mu.Unlock()
	s.o.Client.Reconfigure(ep.URL, ep.PublicURL)
	s.log.Info("t3 browser url set", "url", ep.PublicURL)
	return nil
}

// SetAdminToken saves the admin token ("" clears it) and hands it to the
// client, which obtains a token with it now when it has no valid one (502
// when that fails; the admin token stays saved, so renewal retries).
func (s *Service) SetAdminToken(ctx context.Context, tok string) error {
	tok = strings.TrimSpace(tok)
	if s.o.AdminTokens == nil {
		return errors.New("t3: no admin token store")
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if err := s.o.AdminTokens.SaveT3AdminToken(ctx, tok); err != nil {
		return err
	}
	s.mu.Lock()
	s.admin = tok != ""
	s.mu.Unlock()
	if err := s.o.Client.SetAdminToken(ctx, tok); err != nil {
		return &db.T3Error{Err: errors.New("the admin token did not work: " + err.Error())}
	}
	if tok != "" {
		if _, err := s.afterToken(ctx); err != nil {
			return err
		}
	}
	s.log.Info("t3 admin token set", "set", tok != "")
	return nil
}

// Paired is the outcome of a pairing.
type Paired struct {
	// Replaced is the T3 connection this pairing retired (nil when the
	// same environment was paired again, or the old one held nothing).
	Replaced *db.HarnessConnection
}

// Pair redeems a pairing code (`t3 pair`), records the T3 environment it
// reached on the T3 connection, and kicks a sync. A different environment
// than the connection's replaces it (see the package doc). 502 when pairing
// fails.
func (s *Service) Pair(ctx context.Context, code string) (Paired, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if err := s.o.Client.Pair(ctx, code); err != nil {
		// Always 502 "pairing failed" (drop the chain so ErrNotConnected can't map to 503).
		return Paired{}, &db.T3Error{Err: errors.New("pairing failed: " + err.Error())}
	}
	return s.afterToken(ctx)
}

// afterToken records the T3 environment a new token reached on the T3
// connection (see Pair) and kicks a sync. opMu held.
func (s *Service) afterToken(ctx context.Context) (Paired, error) {
	var out Paired
	if p := s.o.Client.Probe(ctx); p.Identity != nil {
		sw, err := s.o.Identity.SwitchInstance(ctx, t3.Kind, p.Identity.EnvironmentID, p.Identity.Label, 0)
		if err != nil {
			return out, err
		}
		switch {
		case sw.Changed:
			s.log.Info("t3 connection replaced", "from", sw.Old, "to", sw.New, "revived", sw.Revived, "environment", p.Identity.EnvironmentID)
			if s.o.OnSwitched != nil {
				s.o.OnSwitched(sw)
			}
			if old, err := s.o.Identity.GetHarnessConnection(ctx, sw.Old); err == nil {
				out.Replaced = &old
			}
		case sw.Old == 0 && sw.New != 0:
			// The machine's first T3 connection (a remote machine has none
			// until its first pairing).
			s.log.Info("t3 connection created", "connection", sw.New, "environment", p.Identity.EnvironmentID)
			if s.o.OnSwitched != nil {
				s.o.OnSwitched(sw)
			}
		}
	}
	if s.o.OnPaired != nil {
		s.o.OnPaired()
	}
	return out, nil
}

// Unpair disconnects: forgets the admin token, the token and the saved URL
// and browser URL (back to the defaults). The T3 connection keeps its
// environment, so pairing it again resumes it and pairing another T3
// replaces it. Tasks, projects and the mirror stay. Nothing is sent to T3
// (the token stays valid there until it expires). Idempotent.
func (s *Service) Unpair(ctx context.Context) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if s.o.AdminTokens != nil {
		if err := s.o.AdminTokens.SaveT3AdminToken(ctx, ""); err != nil {
			return err
		}
	}
	if err := s.o.Client.SetAdminToken(ctx, ""); err != nil {
		return err
	}
	if err := s.o.Client.Unpair(ctx); err != nil {
		return err
	}
	if err := s.o.Settings.SetT3URL(ctx, ""); err != nil {
		return err
	}
	if err := s.o.Settings.SetT3PublicURL(ctx, ""); err != nil {
		return err
	}
	ep := Resolve(db.Connection{})
	s.mu.Lock()
	s.ep, s.admin = ep, false
	s.mu.Unlock()
	s.o.Client.Reconfigure(ep.URL, ep.PublicURL)
	s.log.Info("t3 disconnected")
	return nil
}

// bound is the active T3 connection's environment ("" when none yet) and
// its label (the probed T3's label when it is that environment, else the id).
func (s *Service) bound(ctx context.Context) (string, string, error) {
	conn, err := s.o.Settings.GetConnection(ctx)
	if err != nil {
		return "", "", err
	}
	if conn.EnvironmentID == nil || *conn.EnvironmentID == "" {
		return "", "", nil
	}
	env := *conn.EnvironmentID
	label := env
	if p := s.o.Client.Status(ctx).Probe; p.Identity != nil && p.Identity.EnvironmentID == env && p.Identity.Label != "" {
		label = p.Identity.Label
	}
	return env, label, nil
}

// bindLegacy stamps a T3 connection paired before identities existed: it
// holds a valid token T3 has not rejected and the URL answers with an
// identity. It never replaces a stamped connection.
func (s *Service) bindLegacy(ctx context.Context, st t3.ConnectionStatus) {
	if !st.Connected || st.Probe.Identity == nil {
		return
	}
	conn, err := s.o.Settings.GetConnection(ctx)
	if err != nil || (conn.EnvironmentID != nil && *conn.EnvironmentID != "") {
		return
	}
	sw, err := s.o.Identity.SwitchInstance(ctx, t3.Kind, st.Probe.Identity.EnvironmentID, st.Probe.Identity.Label, 0)
	if err != nil {
		s.log.Warn("stamp t3 environment", "err", err)
		return
	}
	if sw.Old == 0 && sw.New != 0 && s.o.OnSwitched != nil {
		s.o.OnSwitched(sw)
	}
}

// checkBinding allows target when not paired, or when it serves the paired
// environment. A different T3 is paired only after Disconnect, so the
// replacement is explicit. opMu held.
func (s *Service) checkBinding(ctx context.Context, target string) error {
	st := s.o.Client.Status(ctx)
	s.bindLegacy(ctx, st)
	if !st.HasToken {
		return nil
	}
	bound, label, err := s.bound(ctx)
	if err != nil || bound == "" {
		return err
	}
	var p t3.Probe
	if s.o.Fake {
		p = s.o.Client.Probe(ctx) // the fake answers at every URL
	} else {
		p = s.o.ProbeURL(ctx, target)
	}
	if !p.Reachable || p.Identity == nil {
		return db.Conflict("This stage server is paired with T3 '%s'. No T3 answered at %s (%s), so it cannot be checked; start it and try again, or disconnect first.", label, target, orNoAnswer(p.Err))
	}
	if p.Identity.EnvironmentID != bound {
		return db.Conflict("This stage server is paired with T3 '%s'. Disconnect it first, then change the URL and pair the other T3; its tasks stay here, read-only.", label)
	}
	return nil
}

// pairErr wraps a CLI failure as a 502.
func pairErr(format string, a ...any) error {
	return &db.T3Error{Err: fmt.Errorf(format, a...)}
}

// IsFake reports whether c is the in-memory fake (or wraps it).
func IsFake(c t3.Client) bool {
	_, ok := c.(interface{ SetProbe(t3.Probe) })
	return ok
}
