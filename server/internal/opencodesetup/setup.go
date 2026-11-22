// Package opencodesetup binds an existing OpenCode service to its Stage identity.
package opencodesetup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/opencode"
)

// Error codes (409 body "code").
const (
	CodeLocalUnavailable = "opencode_local_unavailable"
	CodePairingExpired   = "opencode_pairing_expired"
	CodeAuthRequired     = "opencode_auth_required"
)

const (
	renewAfter = 7 * 24 * time.Hour // a paired token this old is renewed
	renewRetry = time.Hour          // after a failed renewal
	localCache = 10 * time.Second   // Status's local registration probe
)

var errManaged = errors.New("This connection is managed by this machine's Stage environment.")

type Service struct {
	mu         sync.Mutex
	ctx        context.Context // background refresh and renewal end with it
	log        *slog.Logger
	id         int64
	settings   Settings // the machine this OpenCode setup runs on
	identity   Identity
	local      Local
	env, saved config.OpenCodeSettings
	verified   string
	problem    string
	active     *opencode.Adapter
	current    config.OpenCodeSettings // active's resolved settings
	// install moves the OpenCode adapter from connection old to next
	// (equal unless the service was replaced); a nil adapter disconnects.
	install func(old, next int64, a execution.Adapter)
	now     func() time.Time
	// renewFailed throttles token renewal; skipLocal is a local registration
	// (url, password) that failed continuity and is not retried.
	renewFailed time.Time
	skipLocal   string
	// redeemed keeps a pairing link's token while the user confirms a
	// changed service: the link works once.
	redeemed pairing
	busy     atomic.Bool // one background refresh or renewal at a time

	localMu    sync.Mutex
	localAt    time.Time
	localState config.OpenCodeLocal
}

type pairing struct {
	link, endpoint, token string
	at                    time.Time
}

// Settings persists the machine's OpenCode setup and credential, saved
// together in one call. On the local machine both live in the store; a
// worker keeps the credential in its own file (ADR 0002).
type Settings interface {
	// OpenCodeSettings is the saved setup and credential, and the address
	// the active connection's service was verified at ("" before the first).
	OpenCodeSettings(ctx context.Context) (config.OpenCodeSetup, config.OpenCodeCredential, string, error)
	SaveOpenCodeSettings(ctx context.Context, setup config.OpenCodeSetup, cred config.OpenCodeCredential) error
}

// Identity is the main server's record of the machine's OpenCode
// connections: their identities, instance switches and the stored evidence
// a service must prove it serves.
type Identity interface {
	// EnsureOpenCode is the machine's active OpenCode connection.
	EnsureOpenCode(ctx context.Context) (int64, error)
	SetConnectionIdentity(ctx context.Context, id int64, identity, label string) error
	SwitchInstance(ctx context.Context, kind, identity, label string, revive int64) (db.Switch, error)
	RetiredConnections(ctx context.Context, kind string) ([]int64, error)
	ConnectionEvidence(ctx context.Context, id int64) ([]db.ProjectBinding, []execution.Observation, bool, error)
}

// Local is the machine-local work: the OpenCode service registered on the
// machine (service.json) and its version probe.
type Local interface {
	Registration() (opencode.Registration, error)
	Probe(ctx context.Context, r opencode.Registration) (string, error)
}

// LocalService reads this process's machine's registration.
type LocalService struct{}

func (LocalService) Registration() (opencode.Registration, error) { return opencode.ReadRegistration() }
func (LocalService) Probe(ctx context.Context, r opencode.Registration) (string, error) {
	return opencode.Probe(ctx, r)
}

// Ports are what the OpenCode setup needs from its machine and the main
// server; never the store itself.
type Ports struct {
	Settings Settings
	Identity Identity
	// Install moves the OpenCode adapter from connection old to next (equal
	// unless the service was replaced); a nil adapter disconnects.
	Install func(old, next int64, a execution.Adapter)
	// Local: nil is LocalService.
	Local Local
	// Active, when set, is the machine's active OpenCode connection (0:
	// none) and replaces EnsureOpenCode: the connection is then created by
	// the first service that connects (a remote machine has no OpenCode
	// connection until then).
	Active func(ctx context.Context) (int64, error)
}

func New(ctx context.Context, p Ports, env config.OpenCodeSettings, log *slog.Logger) (*Service, error) {
	if p.Local == nil {
		p.Local = LocalService{}
	}
	var id int64
	var err error
	if p.Active != nil {
		id, err = p.Active(ctx)
	} else {
		id, err = p.Identity.EnsureOpenCode(ctx)
	}
	if err != nil {
		return nil, err
	}
	setup, cred, verified, err := p.Settings.OpenCodeSettings(ctx)
	if err != nil {
		return nil, err
	}
	saved := config.JoinOpenCode(setup, cred)
	if log == nil {
		log = slog.Default()
	}
	s := &Service{ctx: ctx, log: log, id: id, settings: p.Settings, identity: p.Identity, local: p.Local, env: env, saved: saved, verified: verified, install: p.Install, now: time.Now}
	cfg, _, err := s.resolve(saved)
	switch {
	case err != nil:
		s.problem = err.Error()
	case cfg.Endpoint == "":
	case verified != "" && verified != cfg.Endpoint:
		s.problem = "Service address changed. Verify this connection in Settings."
	default:
		// A moved local service is reconciled by the first health check.
		if cfg.Auth == config.OpenCodeAuthLocal {
			if reg, err := s.local.Registration(); err == nil {
				cfg.Username, cfg.Password = localCredentials(reg)
			}
		}
		a, err := opencode.New(checking(id), cfg)
		if err != nil {
			s.problem = err.Error()
			break
		}
		// Record the original address before any task can acquire its adapter.
		if verified == "" {
			if s.id, err = s.stamp(ctx, id, cfg.Endpoint); err != nil {
				return nil, err
			}
			s.verified = cfg.Endpoint
			if s.id != id {
				if a, err = opencode.New(s.id, cfg); err != nil {
					return nil, err
				}
			}
		}
		s.activate(s.id, a, cfg)
	}
	return s, nil
}

// unstamped is the connection a machine's first service is checked as
// before its connection exists (a remote machine); an adapter on it is
// never installed: stamp creates the connection and the adapter is rebuilt
// on it.
const unstamped int64 = math.MaxInt64

// checking is the connection to build a candidate adapter on: id, or
// unstamped while the machine has none.
func checking(id int64) int64 {
	if id == 0 {
		return unstamped
	}
	return id
}

// stamp records endpoint as connection id's identity, or creates the
// machine's first OpenCode connection with it when id is 0; it returns the
// connection.
func (s *Service) stamp(ctx context.Context, id int64, endpoint string) (int64, error) {
	if id != 0 {
		return id, s.identity.SetConnectionIdentity(ctx, id, endpoint, publicAddress(endpoint))
	}
	sw, err := s.identity.SwitchInstance(ctx, opencode.Kind, endpoint, publicAddress(endpoint), 0)
	return sw.New, err
}

// ID is the active OpenCode connection (0 before a remote machine's first
// service connected); it changes when the service is replaced.
func (s *Service) ID() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

// activate installs a on s.id, moving it from connection old (s.mu held, or
// during New).
func (s *Service) activate(old int64, a *opencode.Adapter, cfg config.OpenCodeSettings) {
	a.OnHealth(s.checked)
	s.active, s.current = a, cfg
	s.install(old, s.id, a)
}

// save persists next's setup and credential together.
func (s *Service) save(ctx context.Context, next config.OpenCodeSettings) error {
	setup, cred := next.Split()
	return s.settings.SaveOpenCodeSettings(ctx, setup, cred)
}

func localCredentials(reg opencode.Registration) (string, string) {
	if reg.Password == "" {
		return "", ""
	}
	return opencode.PairUser, reg.Password
}

// checked runs after each health check of the active adapter: it follows a
// local service's registration and renews a paired token when due.
func (s *Service) checked() {
	if s.ctx.Err() != nil || !s.busy.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer s.busy.Store(false)
		s.refreshLocal(s.ctx)
		s.renewIfDue(s.ctx)
	}()
}

// refreshLocal rebuilds a local connection when the registration's address
// or password changed. A new address must pass the same continuity check as
// Save.
func (s *Service) refreshLocal(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil || s.current.Auth != config.OpenCodeAuthLocal {
		return
	}
	reg, err := s.local.Registration()
	if err != nil {
		return
	}
	user, password := localCredentials(reg)
	key := reg.URL + "\x00" + password
	if (reg.URL == s.current.Endpoint && password == s.current.Password) || key == s.skipLocal {
		return
	}
	next := s.saved
	next.Endpoint = reg.URL
	cfg, _, err := s.resolve(next)
	if err != nil {
		return
	}
	cfg.Username, cfg.Password = user, password
	a, err := opencode.New(s.id, cfg)
	if err != nil {
		s.skipLocal = key
		return
	}
	if cfg.Endpoint != s.verified {
		call, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if !a.Health(call).Available {
			return // not serving yet; the next check retries
		}
		// Only the same service follows silently; another (or an earlier
		// one) needs the user's confirmation through Connect.
		err := s.verifyContinuity(call, a, s.id)
		if err == nil {
			if held, herr := s.holds(call, s.id); herr != nil || !held {
				if revive, perr := s.previous(call, a); perr != nil || revive != 0 {
					err = errors.New("it may be an earlier OpenCode service")
				}
			}
		}
		if err != nil {
			s.skipLocal = key
			s.problem = "The OpenCode service on this machine moved to " + reg.URL + ". Connect to it again to verify it."
			s.log.Warn("opencode local service moved", "url", reg.URL, "err", err)
			return
		}
		if err := s.identity.SetConnectionIdentity(ctx, s.id, cfg.Endpoint, publicAddress(cfg.Endpoint)); err != nil {
			s.log.Warn("opencode local service moved", "url", reg.URL, "err", err)
			return
		}
		s.verified = cfg.Endpoint
	}
	if next.Endpoint != s.saved.Endpoint {
		if err := s.save(ctx, next); err != nil {
			s.log.Warn("opencode local service moved", "url", reg.URL, "err", err)
			return
		}
		s.saved = next
	}
	s.activate(s.id, a, cfg)
}

// renewIfDue replaces a paired token older than renewAfter. A failure keeps
// the current token and waits renewRetry before the next attempt.
func (s *Service) renewIfDue(ctx context.Context) {
	s.mu.Lock()
	cfg, now := s.current, s.now()
	due := s.active != nil && cfg.Auth == config.OpenCodeAuthPaired && cfg.TokenIssuedAt != nil &&
		now.Sub(*cfg.TokenIssuedAt) >= renewAfter && now.Sub(s.renewFailed) >= renewRetry
	s.mu.Unlock()
	if !due {
		return
	}
	call, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	token, err := opencode.Renew(call, cfg.Endpoint, cfg.Password)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current.Auth != config.OpenCodeAuthPaired || s.current.Endpoint != cfg.Endpoint || s.current.Password != cfg.Password {
		return // reconnected meanwhile
	}
	if err == nil {
		err = s.useToken(ctx, token)
	}
	if err != nil {
		s.renewFailed = s.now()
		s.log.Warn("opencode token renewal failed; keeping the current token", "err", err)
	}
}

func (s *Service) useToken(ctx context.Context, token string) error {
	now := s.now().UTC()
	next := s.saved
	next.Password, next.TokenIssuedAt = token, &now
	cfg, _, err := s.resolve(next)
	if err != nil {
		return err
	}
	a, err := opencode.New(s.id, cfg)
	if err != nil {
		return err
	}
	if err := s.save(ctx, next); err != nil {
		return err
	}
	s.saved = next
	s.activate(s.id, a, cfg)
	return nil
}

func (s *Service) resolve(saved config.OpenCodeSettings) (config.OpenCodeSettings, []string, error) {
	c := saved
	locked := []string{}
	overlay := func(key, value string, dest *string) {
		if value != "" {
			locked = append(locked, key)
			*dest = value
		}
	}
	overlay("endpoint", s.env.Endpoint, &c.Endpoint)
	overlay("browserEndpoint", s.env.BrowserEndpoint, &c.BrowserEndpoint)
	overlay("webClient", s.env.WebClient, &c.WebClient)
	c.Endpoint = strings.TrimRight(strings.TrimSpace(c.Endpoint), "/")
	if c.Endpoint != saved.Endpoint {
		c.Username, c.Password = "", ""
		c.Auth, c.TokenIssuedAt = config.OpenCodeAuthNone, nil
	}
	if c.Auth == "" || (c.Auth == config.OpenCodeAuthLocal && s.env.Endpoint != "") {
		c.Auth, c.TokenIssuedAt = config.OpenCodeAuthNone, nil
	}
	if s.env.Username != "" || s.env.Password != "" {
		locked = append(locked, "credentials")
		if s.env.Endpoint == "" || s.env.Username == "" || s.env.Password == "" {
			return c, locked, db.Invalid("Set OPENCODE_URL, OPENCODE_USERNAME and OPENCODE_PASSWORD together to scope environment credentials to the service.")
		}
		c.Username, c.Password = s.env.Username, s.env.Password
		c.Auth, c.TokenIssuedAt = config.OpenCodeAuthBasic, nil
	}
	return c, locked, nil
}

// Invalid environment input may itself contain credentials. Status must never
// echo a userinfo, query string or fragment, even when configuration failed.
func publicAddress(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	return raw
}
func (s *Service) Status(ctx context.Context) config.OpenCodeStatus {
	s.mu.Lock()
	c, locked, _ := s.resolve(s.saved)
	a, problem := s.active, s.problem
	id := s.id
	s.mu.Unlock()
	out := config.OpenCodeStatus{ConnectionID: id, Endpoint: publicAddress(c.Endpoint), BrowserEndpoint: publicAddress(c.BrowserEndpoint), WebClient: publicAddress(c.WebClient), HasCredentials: c.Password != "", EnvironmentFields: locked, State: "unconfigured", Auth: c.Auth}
	if c.Auth == config.OpenCodeAuthPaired && c.TokenIssuedAt != nil {
		at := c.TokenIssuedAt.Add(opencode.TokenTTL).UTC().Truncate(time.Second)
		out.ExpiresAt = &at
	}
	out.Local = s.localStatus(ctx)
	if problem != "" {
		out.State, out.Reason = "unavailable", problem
		return out
	}
	if a != nil {
		call, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		h := a.Health(call)
		out.CheckedAt = &h.CheckedAt
		out.State = "connected"
		switch {
		case opencode.Rejected(h):
			out.State, out.Reason = "rejected", h.Reason
		case !h.Available:
			out.State, out.Reason = "unavailable", h.Reason
		}
	}
	return out
}

// localStatus describes the registration on this machine (cached briefly).
func (s *Service) localStatus(ctx context.Context) config.OpenCodeLocal {
	if s.env.Endpoint != "" {
		return config.OpenCodeLocal{Reason: "environment"}
	}
	s.localMu.Lock()
	defer s.localMu.Unlock()
	if !s.localAt.IsZero() && time.Since(s.localAt) < localCache {
		return s.localState
	}
	out := config.OpenCodeLocal{}
	reg, err := s.local.Registration()
	var unusable *opencode.RegistrationError
	switch {
	case errors.As(err, &unusable):
		out.Reason = unusable.Reason
	case err != nil:
		out.Reason = "invalid"
	default:
		out.URL, out.Version = reg.URL, reg.Version
		call, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		version, err := s.local.Probe(call, reg)
		cancel()
		if err != nil {
			out.Reason = "unreachable"
			break
		}
		out.Available = true
		if version != "" {
			out.Version = version
		}
	}
	s.localAt, s.localState = time.Now(), out
	return out
}

func (s *Service) forgetLocalStatus() {
	s.localMu.Lock()
	s.localAt = time.Time{}
	s.localMu.Unlock()
}

func (s *Service) Save(ctx context.Context, in config.OpenCodeEdit) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.saved
	edit := func(field *string, env string, dest *string) error {
		if field == nil {
			return nil
		}
		if env != "" {
			return db.Conflict("This field is managed by this machine's Stage environment.")
		}
		*dest = strings.TrimRight(strings.TrimSpace(*field), "/")
		return nil
	}
	if err := edit(in.Endpoint, s.env.Endpoint, &next.Endpoint); err != nil {
		return err
	}
	if err := edit(in.BrowserEndpoint, s.env.BrowserEndpoint, &next.BrowserEndpoint); err != nil {
		return err
	}
	if err := edit(in.WebClient, s.env.WebClient, &next.WebClient); err != nil {
		return err
	}
	if next.Endpoint != s.saved.Endpoint {
		next.Username, next.Password = "", ""
		next.Auth, next.TokenIssuedAt = config.OpenCodeAuthNone, nil
	}
	if in.Password != nil || in.Username != nil {
		if s.env.Username != "" || s.env.Password != "" {
			return db.Conflict("Credentials are managed by this machine's Stage environment.")
		}
		if in.Username == nil || in.Password == nil {
			return db.Invalid("Supply both username and password to replace or clear credentials.")
		}
		next.Username, next.Password = *in.Username, *in.Password
		next.Auth, next.TokenIssuedAt = config.OpenCodeAuthBasic, nil
		if next.Username == "" && next.Password == "" {
			next.Auth = config.OpenCodeAuthNone
		}
		if s.env.Endpoint != "" {
			next.Endpoint = strings.TrimRight(s.env.Endpoint, "/")
		}
	}
	return s.commit(ctx, next, "", in.NewInstance)
}

// ConnectLocal connects the OpenCode service registered on this machine,
// authenticating with its registration's password (never stored).
// newInstance confirms a different (or earlier) service, as in Save.
func (s *Service) ConnectLocal(ctx context.Context, newInstance bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.forgetLocalStatus()
	if s.env.Endpoint != "" {
		return db.Conflict("%s", errManaged)
	}
	next := s.saved
	next.Username, next.Password = "", ""
	next.Auth, next.TokenIssuedAt = config.OpenCodeAuthLocal, nil
	return s.commit(ctx, next, CodeLocalUnavailable, newInstance)
}

// Pair redeems an `opencode pair` link and connects its service with the
// session token. Neither the code nor the token is logged. A link whose
// service needs confirmation (instance_changed) keeps its token for 10
// minutes, so the confirming retry with newInstance reuses it.
func (s *Service) Pair(ctx context.Context, link string, newInstance bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.env.Endpoint != "" {
		return db.Conflict("%s", errManaged)
	}
	link = strings.TrimSpace(link)
	endpoint, code, err := opencode.ParsePairLink(link)
	if err != nil {
		return db.Invalid("%s", err)
	}
	token := ""
	if r := s.redeemed; r.link == link && r.endpoint == endpoint && s.now().Sub(r.at) < 10*time.Minute {
		token = r.token
	} else {
		call, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		token, err = opencode.Redeem(call, endpoint, code)
		switch {
		case errors.Is(err, opencode.ErrPairingExpired):
			return &db.ConflictError{Code: CodePairingExpired, Msg: err.Error()}
		case err != nil:
			return &db.HarnessError{Err: err}
		}
	}
	s.redeemed = pairing{}
	now := s.now().UTC()
	next := s.saved
	next.Endpoint, next.Username, next.Password = endpoint, opencode.PairUser, token
	next.Auth, next.TokenIssuedAt = config.OpenCodeAuthPaired, &now
	err = s.commit(ctx, next, "", newInstance)
	var conflict *db.ConflictError
	if errors.As(err, &conflict) && (conflict.Code == "instance_changed" || conflict.Code == "instance_returned") {
		s.redeemed = pairing{link: link, endpoint: endpoint, token: token, at: s.now()}
	}
	return err
}

// Disconnect forgets the service address and credentials and deactivates
// the adapter. The connection keeps its identity, so connecting another
// service later still asks to confirm it as new. Tasks and history stay.
func (s *Service) Disconnect(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.env.Endpoint != "" {
		return db.Conflict("%s", errManaged)
	}
	next := config.OpenCodeSettings{Auth: config.OpenCodeAuthNone}
	if err := s.save(ctx, next); err != nil {
		return err
	}
	s.saved, s.problem, s.skipLocal, s.renewFailed, s.redeemed = next, "", "", time.Time{}, pairing{}
	if s.active != nil {
		s.active, s.current = nil, config.OpenCodeSettings{}
		s.install(s.id, s.id, nil)
	}
	return nil
}

// commit tests next and makes it current (s.mu held). failCode is the
// conflict code for a service that does not answer; newInstance confirms a
// different or earlier service (instance_changed / instance_returned).
func (s *Service) commit(ctx context.Context, next config.OpenCodeSettings, failCode string, newInstance bool) error {
	cfg, _, err := s.resolve(next)
	if err != nil {
		return err
	}
	if cfg.Auth == config.OpenCodeAuthLocal {
		reg, err := s.local.Registration()
		if err != nil {
			return &db.ConflictError{Code: CodeLocalUnavailable, Msg: err.Error()}
		}
		next.Endpoint, cfg.Endpoint = reg.URL, reg.URL
		cfg.Username, cfg.Password = localCredentials(reg)
		failCode = CodeLocalUnavailable
	}
	a, err := opencode.New(checking(s.id), cfg)
	if err != nil {
		return db.Invalid("%s", err)
	}
	call, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if h := a.Health(call); !h.Available {
		switch {
		case opencode.Rejected(h):
			return &db.ConflictError{Code: CodeAuthRequired, Msg: h.Reason}
		case failCode != "":
			return &db.ConflictError{Code: failCode, Msg: fmt.Sprintf("The OpenCode service at %s is not answering: %s", cfg.Endpoint, h.Reason)}
		}
		return db.Conflict("%s", h.Reason)
	}
	id, label := s.id, publicAddress(cfg.Endpoint)
	switch {
	case s.verified == "":
		first, err := s.stamp(ctx, id, cfg.Endpoint)
		if err != nil {
			return err
		}
		if first != id {
			if a, err = opencode.New(first, cfg); err != nil {
				return db.Invalid("%s", err)
			}
			id = first
		}
	case cfg.Endpoint != s.verified:
		continuity := s.verifyContinuity(call, a, id)
		held, err := s.holds(call, id)
		if err != nil {
			return err
		}
		if continuity == nil && held {
			// The same service at a new address.
			if err := s.identity.SetConnectionIdentity(ctx, id, cfg.Endpoint, label); err != nil {
				return err
			}
			break
		}
		revive, err := s.previous(call, a)
		if err != nil {
			return err
		}
		if continuity == nil && revive == 0 {
			// Nothing stored to prove either way, and no earlier service.
			if err := s.identity.SetConnectionIdentity(ctx, id, cfg.Endpoint, label); err != nil {
				return err
			}
			break
		}
		if !newInstance {
			if revive != 0 {
				return &db.ConflictError{Code: "instance_returned", Msg: "This address serves the OpenCode service Stage used before. Connect it again: its tasks and bindings come back, and the current connection becomes read-only."}
			}
			msg := "This address serves a different OpenCode service. Connect it as a new service: the current connection becomes read-only and keeps its tasks."
			if continuity != nil {
				msg = "This address serves a different OpenCode service (" + continuity.Error() + "). Connect it as a new service: the current connection becomes read-only and keeps its tasks."
			}
			return &db.ConflictError{Code: "instance_changed", Msg: msg}
		}
		sw, err := s.identity.SwitchInstance(ctx, opencode.Kind, cfg.Endpoint, label, revive)
		if err != nil {
			return err
		}
		if sw.New != id {
			if a, err = opencode.New(sw.New, cfg); err != nil {
				return db.Invalid("%s", err)
			}
			id = sw.New
		}
	}
	if err := s.save(ctx, next); err != nil {
		return err
	}
	old := s.id
	s.saved, s.verified, s.problem, s.skipLocal, s.renewFailed, s.id = next, cfg.Endpoint, "", "", time.Time{}, id
	s.activate(old, a, cfg)
	return nil
}

// holds reports whether a connection stores anything a service must prove
// it serves (bindings, sessions, or a launch without a known session).
func (s *Service) holds(ctx context.Context, id int64) (bool, error) {
	bindings, sessions, unknown, err := s.identity.ConnectionEvidence(ctx, id)
	return unknown || len(bindings)+len(sessions) > 0, err
}

// previous is the replaced OpenCode connection the candidate service proves
// to be (0 when none): it serves that connection's workspaces and sessions.
func (s *Service) previous(ctx context.Context, a *opencode.Adapter) (int64, error) {
	ids, err := s.identity.RetiredConnections(ctx, opencode.Kind)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		bindings, sessions, unknown, err := s.identity.ConnectionEvidence(ctx, id)
		if err != nil {
			return 0, err
		}
		if unknown || len(bindings)+len(sessions) == 0 {
			continue
		}
		if s.verifyContinuity(ctx, a, id) == nil {
			return id, nil
		}
	}
	return 0, nil
}

func (s *Service) verifyContinuity(ctx context.Context, a *opencode.Adapter, id int64) error {
	bindings, sessions, unknown, err := s.identity.ConnectionEvidence(ctx, id)
	if err != nil {
		return err
	}
	if unknown || (len(bindings) > 0 && len(sessions) == 0) {
		return db.Conflict("a stored workspace or launch lacks a verifiable session identity")
	}
	projects, err := a.Workspaces(ctx)
	if err != nil {
		return &db.HarnessError{Err: err}
	}
	for _, b := range bindings {
		matched := false
		for _, p := range projects {
			if p.ID == b.ExternalProjectID && p.WorkspaceRoot == b.Workspace {
				matched = true
				break
			}
		}
		if !matched {
			return db.Conflict("it does not serve stored workspace %s", b.Workspace)
		}
	}
	for _, old := range sessions {
		// a is scoped to the active connection; the session is compared on
		// the candidate service whatever connection recorded it.
		ref := old.Session
		ref.ConnectionID = s.id
		o, err := a.Observe(ctx, ref)
		if err != nil || old.CreatedAt.IsZero() || !o.CreatedAt.Equal(old.CreatedAt) || o.Project != old.Project || o.Directory != old.Directory {
			return db.Conflict("it could not verify stored session %s", old.Session.ExternalSessionID)
		}
	}
	return nil
}
