// Package opencodesetup binds an existing OpenCode service to its Stage identity.
package opencodesetup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
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

type Service struct {
	mu       sync.Mutex
	ctx      context.Context // background refresh and renewal end with it
	log      *slog.Logger
	id       int64
	store    settingsStore
	identity harness.Identity
	local    Local
	saved    settings
	verified string
	problem  string
	active   *opencode.Adapter
	current  settings // active's resolved settings
	// install moves the OpenCode adapter from connection old to next
	// (equal unless the service was replaced); a nil adapter disconnects.
	install func(old, next int64, a execution.Adapter)
	now     func() time.Time
	// renewFailed throttles token renewal; skipLocal is a local registration
	// (url, password) that failed continuity and is not retried.
	renewFailed time.Time
	skipLocal   string
	// redeemed keeps a pairing link's token until setup succeeds.
	// The link works once.
	redeemed pairing
	busy     atomic.Bool // one background refresh or renewal at a time

	localMu    sync.Mutex
	localAt    time.Time
	localState LocalStatus
}

type pairing struct {
	link, endpoint, token string
	at                    time.Time
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

func newService(ctx context.Context, p harness.Ports, local Local) (*Service, error) {
	if local == nil {
		local = LocalService{}
	}
	id, verified, err := p.Identity.ActiveIdentity(ctx, opencode.Kind)
	if err != nil {
		return nil, err
	}
	store := settingsStore{public: p.Settings, secret: p.Secrets, atomic: p.SaveSettingsWithSecret}
	saved, err := store.load(ctx)
	if err != nil {
		return nil, err
	}
	log := p.Logger
	if log == nil {
		log = slog.Default()
	}
	s := &Service{ctx: ctx, log: log, id: id, store: store, identity: p.Identity, local: local, saved: saved, verified: verified, install: p.Install, now: time.Now}
	cfg := resolve(saved)
	switch {
	case cfg.Endpoint == "":
	case id == 0:
		s.problem = "Verify this OpenCode connection in Settings."
	case verified != "" && verified != cfg.Endpoint:
		s.problem = "Service address changed. Verify this connection in Settings."
	case (cfg.Auth == AuthBasic || cfg.Auth == AuthPaired) && cfg.Credential == (Credential{}):
		s.problem = "OpenCode credentials are missing. Verify this connection in Settings."
	default:
		// A moved local service is reconciled by the first health check.
		if cfg.Auth == AuthLocal {
			if reg, err := s.local.Registration(); err == nil {
				cfg.Username, cfg.Password = localCredentials(reg)
			}
		}
		a, err := opencode.New(id, cfg.transport())
		if err != nil {
			s.problem = err.Error()
			break
		}
		// Record the original address before any task can acquire its adapter.
		if verified == "" {
			if err := p.Identity.SetConnectionIdentity(ctx, id, cfg.Endpoint, publicAddress(cfg.Endpoint)); err != nil {
				return nil, err
			}
			s.verified = cfg.Endpoint
		}
		s.activate(id, a, cfg)
	}
	return s, nil
}

// unstamped is the connection a machine's first service is checked as
// before its connection exists; an adapter on it is
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

// ID is the active OpenCode connection (0 before a machine's first
// service connected); it changes when the service is replaced.
func (s *Service) ID() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

// activate installs a on s.id, moving it from connection old (s.mu held, or
// during composition).
func (s *Service) activate(old int64, a *opencode.Adapter, cfg settings) {
	a.OnHealth(s.checked)
	s.active, s.current = a, cfg
	s.install(old, s.id, a)
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
	if s.active == nil || s.current.Auth != AuthLocal {
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
	cfg := resolve(next)
	cfg.Username, cfg.Password = user, password
	a, err := opencode.New(s.id, cfg.transport())
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
	}
	if next.Endpoint != s.saved.Endpoint {
		if err := s.store.save(ctx, s.saved, next); err != nil {
			s.log.Warn("opencode local service moved", "url", reg.URL, "err", err)
			return
		}
	}
	if cfg.Endpoint != s.verified {
		if _, err := s.stamp(ctx, s.id, cfg.Endpoint); err != nil {
			s.log.Warn("opencode local service moved", "url", reg.URL, "err", s.restoreSettings(ctx, next, err))
			return
		}
	}
	s.saved, s.verified = next, cfg.Endpoint
	s.activate(s.id, a, cfg)
}

// renewIfDue replaces a paired token older than renewAfter. A failure keeps
// the current token and waits renewRetry before the next attempt.
func (s *Service) renewIfDue(ctx context.Context) {
	s.mu.Lock()
	cfg, now := s.current, s.now()
	due := s.active != nil && cfg.Auth == AuthPaired && cfg.TokenIssuedAt != nil &&
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
	if s.current.Auth != AuthPaired || s.current.Endpoint != cfg.Endpoint || s.current.Password != cfg.Password {
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
	cfg := resolve(next)
	a, err := opencode.New(s.id, cfg.transport())
	if err != nil {
		return err
	}
	if err := s.store.save(ctx, s.saved, next); err != nil {
		return err
	}
	s.saved = next
	s.activate(s.id, a, cfg)
	return nil
}

func resolve(saved settings) settings {
	c := saved
	c.Endpoint = strings.TrimRight(strings.TrimSpace(c.Endpoint), "/")
	if c.Endpoint != saved.Endpoint {
		c.Username, c.Password = "", ""
		c.Auth, c.TokenIssuedAt = AuthNone, nil
	}
	if c.Auth == "" {
		c.Auth, c.TokenIssuedAt = AuthNone, nil
	}
	return c
}

// A saved address may itself contain credentials. Status must never
// echo a userinfo, query string or fragment, even when configuration failed.
func publicAddress(raw string) string {
	if _, err := opencode.ValidateURL("OpenCode URL", raw); err != nil {
		return ""
	}
	return raw
}
func (s *Service) Status(ctx context.Context) Status {
	s.mu.Lock()
	c := resolve(s.saved)
	a, problem := s.active, s.problem
	id := s.id
	s.mu.Unlock()
	out := Status{ConnectionID: id, Endpoint: publicAddress(c.Endpoint), BrowserEndpoint: publicAddress(c.BrowserEndpoint), WebClient: publicAddress(c.WebClient), HasCredentials: c.Password != "", State: "unconfigured", Auth: c.Auth}
	if c.Auth == AuthPaired && c.TokenIssuedAt != nil {
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
func (s *Service) localStatus(ctx context.Context) LocalStatus {
	s.localMu.Lock()
	defer s.localMu.Unlock()
	if !s.localAt.IsZero() && time.Since(s.localAt) < localCache {
		return s.localState
	}
	out := LocalStatus{}
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

func (s *Service) Save(ctx context.Context, in Edit) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.saved
	edit := func(field *string, dest *string) {
		if field == nil {
			return
		}
		*dest = strings.TrimRight(strings.TrimSpace(*field), "/")
	}
	edit(in.Endpoint, &next.Endpoint)
	edit(in.BrowserEndpoint, &next.BrowserEndpoint)
	edit(in.WebClient, &next.WebClient)
	if next.Endpoint != s.saved.Endpoint {
		next.Username, next.Password = "", ""
		next.Auth, next.TokenIssuedAt = AuthNone, nil
	}
	if in.Password != nil || in.Username != nil {
		if in.Username == nil || in.Password == nil {
			return db.Invalid("Supply both username and password to replace or clear credentials.")
		}
		next.Username, next.Password = *in.Username, *in.Password
		next.Auth, next.TokenIssuedAt = AuthBasic, nil
		if next.Username == "" && next.Password == "" {
			next.Auth = AuthNone
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
	next := s.saved
	next.Username, next.Password = "", ""
	next.Auth, next.TokenIssuedAt = AuthLocal, nil
	return s.commit(ctx, next, CodeLocalUnavailable, newInstance)
}

// Pair redeems an `opencode pair` link and connects its service with the
// session token. Neither the code nor the token is logged. If setup fails
// after redemption, a retry can reuse the token for up to 10 minutes.
func (s *Service) Pair(ctx context.Context, link string, newInstance bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	next.Auth, next.TokenIssuedAt = AuthPaired, &now
	err = s.commit(ctx, next, "", newInstance)
	if err != nil {
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
	next := settings{SetupSettings: SetupSettings{Auth: AuthNone}}
	if err := s.store.save(ctx, s.saved, next); err != nil {
		return err
	}
	s.saved, s.problem, s.skipLocal, s.renewFailed, s.redeemed = next, "", "", time.Time{}, pairing{}
	if s.active != nil {
		s.active, s.current = nil, settings{}
		s.install(s.id, s.id, nil)
	}
	return nil
}

// commit tests next and makes it current (s.mu held). failCode is the
// conflict code for a service that does not answer; newInstance confirms a
// different or earlier service (instance_changed / instance_returned).
func (s *Service) commit(ctx context.Context, next settings, failCode string, newInstance bool) error {
	cfg := resolve(next)
	if cfg.Auth == AuthLocal {
		reg, err := s.local.Registration()
		if err != nil {
			return &db.ConflictError{Code: CodeLocalUnavailable, Msg: err.Error()}
		}
		next.Endpoint, cfg.Endpoint = reg.URL, reg.URL
		cfg.Username, cfg.Password = localCredentials(reg)
		failCode = CodeLocalUnavailable
	}
	a, err := opencode.New(checking(s.id), cfg.transport())
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
	id := s.id
	var replace bool
	var revive int64
	if s.verified != "" && cfg.Endpoint != s.verified {
		replace, revive, err = s.replacement(call, a, newInstance)
		if err != nil {
			return err
		}
	}
	if err := s.store.save(ctx, s.saved, next); err != nil {
		return err
	}
	if cfg.Endpoint != s.verified {
		if replace {
			var sw harness.Switch
			sw, err = s.identity.SwitchInstance(ctx, opencode.Kind, cfg.Endpoint, publicAddress(cfg.Endpoint), revive)
			id = sw.New
		} else {
			id, err = s.stamp(ctx, id, cfg.Endpoint)
		}
		if err != nil {
			return s.restoreSettings(ctx, next, err)
		}
	}
	if id != s.id {
		if a, err = opencode.New(id, cfg.transport()); err != nil {
			return err
		}
	}
	old := s.id
	s.saved, s.verified, s.problem, s.skipLocal, s.renewFailed, s.id = next, cfg.Endpoint, "", "", time.Time{}, id
	s.activate(old, a, cfg)
	return nil
}

// restoreSettings is best effort after an identity write fails. The saved
// endpoint must still match the verified address before a restart can use it.
func (s *Service) restoreSettings(ctx context.Context, next settings, cause error) error {
	rollback, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return errors.Join(cause, s.store.save(rollback, next, s.saved))
}

// replacement identifies same-service moves, confirmed replacements, and
// confirmed returns to retired connections. It does not mutate identity.
func (s *Service) replacement(ctx context.Context, a *opencode.Adapter, confirm bool) (replace bool, revive int64, err error) {
	continuity := s.verifyContinuity(ctx, a, s.id)
	held, err := s.holds(ctx, s.id)
	if err != nil {
		return false, 0, err
	}
	if continuity == nil && held {
		return false, 0, nil
	}
	revive, err = s.previous(ctx, a)
	if err != nil {
		return false, 0, err
	}
	if continuity == nil && revive == 0 {
		return false, 0, nil
	}
	if confirm {
		return true, revive, nil
	}
	if revive != 0 {
		return false, 0, &db.ConflictError{Code: "instance_returned", Msg: "This address serves the OpenCode service Stage used before. Connect it again: its tasks and bindings come back, and the current connection becomes read-only."}
	}
	msg := "This address serves a different OpenCode service. Connect it as a new service: the current connection becomes read-only and keeps its tasks."
	if continuity != nil {
		msg = "This address serves a different OpenCode service (" + continuity.Error() + "). Connect it as a new service: the current connection becomes read-only and keeps its tasks."
	}
	return false, 0, &db.ConflictError{Code: "instance_changed", Msg: msg}
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
