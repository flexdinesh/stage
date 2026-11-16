// Package t3setup owns how this Stage server reaches its one T3 backend:
// the effective T3 URL (T3_URL, else the saved setting, else the default),
// the environment binding, pairing (pasted code or auto-pair through the
// local t3 CLI) and the status the setup UI shows (docs/api.md "T3
// connection").
//
// Binding: the first successful pairing binds the server to that T3
// environment; its token and mirror belong to it. The URL may then change
// only to an address serving the same environment.
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
	SourceEnv      = "env"      // T3_URL; locks the setting
	SourceSettings = "settings" // saved with PUT /api/t3/url
	SourceDefault  = "default"  // config.DefaultT3URL
)

// StatusProbeTimeout bounds the probe refresh of a status read.
const StatusProbeTimeout = 3 * time.Second

// Endpoint is the T3 address in use.
type Endpoint struct {
	URL       string // server-side MCP/OAuth calls
	PublicURL string // browser "Open in T3" links
	Source    string // SourceEnv | SourceSettings | SourceDefault
}

// Store is the persisted part (db.Store).
type Store interface {
	GetConnection(ctx context.Context) (db.Connection, error)
	SetT3URL(ctx context.Context, u string) error
	SetTokenEnvironment(ctx context.Context, envID string) error
}

// Resolve is the endpoint at startup: T3_URL wins, then the saved setting
// (saved, nil or "" when none), then the default. The public URL is
// T3_PUBLIC_URL when set, else the T3 URL.
func Resolve(cfg config.Config, saved *string) Endpoint {
	ep := Endpoint{URL: cfg.T3URL, Source: SourceDefault}
	if ep.URL == "" {
		ep.URL = config.DefaultT3URL
	}
	switch {
	case cfg.T3URLFromEnv:
		ep.Source = SourceEnv
	case saved != nil && *saved != "":
		ep.URL, ep.Source = *saved, SourceSettings
	}
	ep.URL = strings.TrimRight(ep.URL, "/")
	ep.PublicURL = publicURL(cfg, ep.URL)
	return ep
}

// publicURL: T3_PUBLIC_URL when set (also a Config whose public URL differs
// from its T3 URL, as tests build them), else the T3 URL.
func publicURL(cfg config.Config, base string) string {
	if cfg.T3PublicURLFromEnv || (cfg.T3PublicURL != "" && cfg.T3PublicURL != cfg.T3URL) {
		return cfg.T3PublicURL
	}
	return base
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
	Store    Store
	Client   t3.Client
	Endpoint Endpoint // the endpoint the client was built with (Resolve)
	// Fake: Client is the in-memory fake (no auto-pair; any URL is it).
	Fake bool
	// OnPaired runs after every successful pairing (full sync and catalog
	// refresh now). Must not block.
	OnPaired func()
	Logger   *slog.Logger
	// Run executes the t3 CLI; nil = os/exec.
	Run Runner
	// LookPath resolves a command name or path to an executable; nil =
	// exec.LookPath.
	LookPath func(file string) (string, error)
	// Home is the user's home for the t3 CLI search; "" = os.UserHomeDir.
	Home string
	// ProbeURL probes a candidate URL; nil = t3.ProbeURL with a 3s client.
	ProbeURL func(ctx context.Context, base string) t3.Probe
}

// Service is the T3 setup of this server. Safe for concurrent use.
type Service struct {
	o   Options
	log *slog.Logger

	opMu sync.Mutex // serializes URL changes and pairings

	mu sync.Mutex
	ep Endpoint
}

// New builds the service around a client already configured for
// o.Endpoint.
func New(o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Endpoint.URL == "" {
		o.Endpoint = Resolve(o.Config, nil)
	}
	if o.Run == nil {
		o.Run = execRunner
	}
	if o.LookPath == nil {
		o.LookPath = execLookPath
	}
	if o.ProbeURL == nil {
		hc := &http.Client{Timeout: StatusProbeTimeout}
		o.ProbeURL = func(ctx context.Context, base string) t3.Probe { return t3.ProbeURL(ctx, hc, base) }
	}
	return &Service{o: o, log: o.Logger.With("component", "t3setup"), ep: o.Endpoint}
}

// Endpoint is the T3 address in use now.
func (s *Service) Endpoint() Endpoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ep
}

// Status is the setup state (GET /api/t3/status).
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
// client at it. 409 when T3_URL is set, or when the server is bound to a T3
// environment the new URL does not serve; 400 for an invalid URL.
func (s *Service) SetURL(ctx context.Context, raw *string) error {
	if s.o.Config.T3URLFromEnv {
		return db.Conflict("the T3 URL is set by T3_URL in this stage server's environment; change it there")
	}
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
	if err := s.o.Store.SetT3URL(ctx, saved); err != nil {
		return err
	}
	ep := Endpoint{URL: target, PublicURL: publicURL(s.o.Config, target), Source: source}
	s.mu.Lock()
	s.ep = ep
	s.mu.Unlock()
	s.o.Client.Reconfigure(ep.URL, ep.PublicURL)
	s.log.Info("t3 url set", "url", ep.URL, "source", ep.Source)
	return nil
}

// Pair redeems a pairing code (`t3 pair`), binds the server to the T3
// environment on the first pairing, and kicks a sync. 409 when the T3 at
// the URL is not the bound one; 502 when pairing fails.
func (s *Service) Pair(ctx context.Context, code string) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	bound, label, err := s.bound(ctx)
	if err != nil {
		return err
	}
	if bound != "" {
		if p := s.o.Client.Probe(ctx); p.Identity != nil && p.Identity.EnvironmentID != bound {
			return boundConflict(label)
		}
	}
	if err := s.o.Client.Pair(ctx, code); err != nil {
		// Always 502 "pairing failed" (drop the chain so ErrNotConnected can't map to 503).
		return &db.T3Error{Err: errors.New("pairing failed: " + err.Error())}
	}
	if bound == "" {
		if p := s.o.Client.Probe(ctx); p.Identity != nil {
			if err := s.o.Store.SetTokenEnvironment(ctx, p.Identity.EnvironmentID); err != nil {
				s.log.Warn("bind t3 environment", "err", err)
			} else {
				s.log.Info("bound to t3 environment", "environment", p.Identity.EnvironmentID, "label", p.Identity.Label)
			}
		}
	}
	if s.o.OnPaired != nil {
		s.o.OnPaired()
	}
	return nil
}

// bound is the bound environment id ("" when unbound) and its label (the
// probed T3's label when it is that environment, else the id).
func (s *Service) bound(ctx context.Context) (string, string, error) {
	conn, err := s.o.Store.GetConnection(ctx)
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

// bindLegacy binds a server paired before binding existed: it holds a
// valid token T3 has not rejected and the URL answers with an identity.
func (s *Service) bindLegacy(ctx context.Context, st t3.ConnectionStatus) {
	if !st.Connected || st.Probe.Identity == nil {
		return
	}
	conn, err := s.o.Store.GetConnection(ctx)
	if err != nil || (conn.EnvironmentID != nil && *conn.EnvironmentID != "") {
		return
	}
	if err := s.o.Store.SetTokenEnvironment(ctx, st.Probe.Identity.EnvironmentID); err != nil {
		s.log.Warn("bind t3 environment", "err", err)
	}
}

// checkBinding allows target when unbound, or when it serves the bound
// environment. opMu held.
func (s *Service) checkBinding(ctx context.Context, target string) error {
	s.bindLegacy(ctx, s.o.Client.Status(ctx))
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
		return db.Conflict("This stage server is bound to T3 '%s'. No T3 answered at %s (%s), so it cannot be checked; start it and try again.", label, target, orNoAnswer(p.Err))
	}
	if p.Identity.EnvironmentID != bound {
		return boundConflict(label)
	}
	return nil
}

func boundConflict(label string) error {
	return db.Conflict("This stage server is bound to T3 '%s'. Run another stage server for a different T3.", label)
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
