package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/flexdinesh/stage/server/internal/auth"
	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/guard"
	"github.com/flexdinesh/stage/server/internal/principal"
)

const sessionCookie = "stage_session"

// Access owns the runtime access policy. A nil Access is personal mode.
type Access struct {
	service *auth.Service
	origin  string
	host    string
	secure  bool
	id      string
	claimed atomic.Bool
	onClaim func(time.Time)
}

func NewAccess(svc *auth.Service, origin string, state store.InstallationAccess, onClaim func(time.Time)) *Access {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		panic("api.NewAccess requires a canonical origin")
	}
	a := &Access{service: svc, origin: origin, host: u.Host, secure: u.Scheme == "https", id: state.ID, onClaim: onClaim}
	a.claimed.Store(state.Mode == store.AccessClaimed)
	return a
}

func (a *Access) Pending() bool { return a != nil && !a.claimed.Load() }

func (a *Access) markClaimed(at time.Time) {
	if a.onClaim != nil {
		a.onClaim(at)
	}
	a.claimed.Store(true)
}

type routeScope uint8

const (
	humanRoute routeScope = iota
	agentRoute
	adminRoute
	passiveHumanRoute
)

type credential struct {
	token    string
	identity auth.Identity
}

type credentialKey struct{}
type gatewayCheckKey struct{}
type localAgentKey struct{}

type credentialCheck func(context.Context) error

func withGatewayCheck(ctx context.Context, check credentialCheck) context.Context {
	return context.WithValue(ctx, gatewayCheckKey{}, check)
}

func gatewayCheck(ctx context.Context) credentialCheck {
	check, _ := ctx.Value(gatewayCheckKey{}).(credentialCheck)
	return check
}

func credentialFrom(ctx context.Context) (credential, bool) {
	c, ok := ctx.Value(credentialKey{}).(credential)
	return c, ok
}

// authRequest authenticates the incoming credential and preserves its
// audience when a tool makes an in-process API request.
func (a *Access) authRequest(r *http.Request, touch bool) (context.Context, error) {
	ctx := r.Context()
	actor, admitted := principal.From(ctx)
	if admitted {
		if c, ok := credentialFrom(ctx); ok {
			_, err := a.service.Authenticate(ctx, c.token, c.identity.Session.Audience, false)
			return ctx, err
		}
		if check := gatewayCheck(ctx); actor.Kind == principal.Agent && actor.MachineID != 0 && check != nil {
			return ctx, check(ctx)
		}
		if _, local := ctx.Value(localAgentKey{}).(struct{}); local && actor.Kind == principal.Agent {
			return ctx, nil
		}
		return ctx, auth.ErrSession
	}
	if !guard.PrivatePeer(r) {
		return ctx, auth.ErrSession
	}
	var token string
	var audiences []store.SessionAudience
	if r.Header.Get("Authorization") != "" {
		token, audiences = bearer(r), []store.SessionAudience{store.SessionCLI, store.SessionAgent}
		if raw, scoped := strings.CutPrefix(token, principal.AgentTokenPrefix); scoped {
			token, audiences = raw, []store.SessionAudience{store.SessionAgent}
		}
	} else if cookie, err := r.Cookie(sessionCookie); err == nil {
		if !guard.CanonicalBrowser(r, a.origin, a.host) {
			return ctx, errBrowserOrigin
		}
		token, audiences = cookie.Value, []store.SessionAudience{store.SessionBrowser}
	}
	for _, audience := range audiences {
		id, err := a.service.Authenticate(ctx, token, audience, touch)
		if errors.Is(err, auth.ErrSession) {
			continue
		}
		if err != nil {
			return ctx, err
		}
		kind := principal.Human
		switch {
		case audience == store.SessionAgent:
			kind = principal.Agent
		case id.Account.Role == store.RoleAdmin:
			kind = principal.Admin
		}
		ctx = context.WithValue(ctx, credentialKey{}, credential{token, id})
		ctx = db.WithUser(ctx, id.Account.ID)
		return principal.With(ctx, principal.Actor{Kind: kind, AccountID: id.Account.ID}), nil
	}
	return ctx, auth.ErrSession
}

// LocalAgent is installed only on the owner-private Unix listener. Routes
// still pass through the normal shared principal policy, including MCP.
func (a *Access) LocalAgent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		if !guard.Unix(r) || r.Host != "stage" ||
			r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" ||
			r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			accessError(w, http.StatusForbidden, "private_agent_required", "Connect to the private Stage agent socket without credentials.")
			return
		}
		path := r.URL.Path
		if path != "/mcp" && (!strings.HasPrefix(path, "/api/") ||
			strings.HasPrefix(path, WorkerPrefix) || strings.HasPrefix(path, "/api/worker/")) {
			http.NotFound(w, r)
			return
		}
		ctx := db.WithMachine(r.Context(), db.LocalMachine)
		ctx = principal.With(ctx, principal.Actor{Kind: principal.Agent, MachineID: db.LocalMachine})
		ctx = context.WithValue(ctx, localAgentKey{}, struct{}{})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

var errBrowserOrigin = errors.New("browser origin rejected")

func pending(w http.ResponseWriter) {
	accessError(w, http.StatusForbidden, "setup_pending", "Waiting for the installation owner to finish setup.")
}

func accessError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"code": code, "error": msg})
}

func writeAccessError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrSession), errors.Is(err, db.ErrInvalidMachineToken):
		accessError(w, http.StatusUnauthorized, "authentication_required", auth.PublicMessage(auth.ErrSession))
	case errors.Is(err, errBrowserOrigin):
		accessError(w, http.StatusForbidden, "origin_forbidden", "Open Stage at its configured browser address and try again.")
	default:
		accessError(w, http.StatusServiceUnavailable, "authentication_unavailable", auth.PublicMessage(err))
	}
}

func (s *server) authorize(scope routeScope, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Access == nil {
			if _, ok := principal.From(r.Context()); !ok {
				ctx := principal.With(r.Context(), principal.Actor{Kind: principal.Personal, AccountID: db.ActingUser(r.Context())})
				if r.Header.Get(principal.ToolTransportHeader) == "mcp" {
					stamp := db.WorkAttribution(ctx)
					stamp.Actor.Kind = "agent"
					ctx = db.WithAttribution(ctx, stamp)
				}
				r = r.WithContext(ctx)
			}
			next(w, r)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		if s.Access.Pending() {
			pending(w)
			return
		}
		ctx, err := s.Access.authRequest(r, scope != passiveHumanRoute)
		if err != nil {
			writeAccessError(w, err)
			return
		}
		actor, _ := principal.From(ctx)
		allowed := actor.IsHuman()
		switch scope {
		case agentRoute:
			allowed = allowed || actor.Kind == principal.Agent
		case adminRoute:
			if actor.Kind != principal.Admin {
				accessError(w, http.StatusForbidden, "principal_forbidden", "This operation requires a Stage admin.")
				return
			}
		}
		if !allowed {
			accessError(w, http.StatusForbidden, "principal_forbidden", "Your credential does not allow this operation.")
			return
		}
		next(w, r.WithContext(ctx))
	}
}

// MCP authorizes the outer request and gives tool calls agent authority.
// A CLI credential is deliberately excluded from MCP.
func (a *Access) MCP(next http.Handler) http.Handler {
	return RejectMachineTokens(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		actor, _ := principal.From(ctx)
		if a != nil {
			w.Header().Set("Cache-Control", "private, no-store")
			if a.Pending() {
				pending(w)
				return
			}
			var err error
			ctx, err = a.authRequest(r, true)
			if err != nil {
				writeAccessError(w, err)
				return
			}
			actor, _ = principal.From(ctx)
			if c, ok := credentialFrom(ctx); ok && c.identity.Session.Audience == store.SessionCLI {
				accessError(w, http.StatusForbidden, "principal_forbidden", "Use an agent credential to connect to Stage MCP.")
				return
			}
			// guard.Policy already checked the Host and Origin on the
			// configured proxy upstream. The SDK should not reapply its
			// loopback Host rule to the canonical public Host.
			ctx = context.WithValue(ctx, http.LocalAddrContextKey, nil)
			w = privateMCPResponse{w}
		}
		if actor.IsHuman() {
			actor.InitiatorID = actor.AccountID
		}
		actor.Kind = principal.Agent
		ctx, stop := a.watchSession(principal.With(ctx, actor))
		defer stop()
		next.ServeHTTP(w, r.WithContext(ctx))
	}))
}

// The MCP SDK sets its own cache policy when starting an event stream.
// Apply the shared response policy at the point the headers are committed.
type privateMCPResponse struct{ http.ResponseWriter }

func (w privateMCPResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w privateMCPResponse) WriteHeader(status int) {
	w.Header().Set("Cache-Control", "private, no-store, no-transform")
	w.ResponseWriter.WriteHeader(status)
}

func (w privateMCPResponse) Write(p []byte) (int, error) {
	w.Header().Set("Cache-Control", "private, no-store, no-transform")
	return w.ResponseWriter.Write(p)
}

func (w privateMCPResponse) FlushError() error {
	w.Header().Set("Cache-Control", "private, no-store, no-transform")
	return http.NewResponseController(w.ResponseWriter).Flush()
}

// watchSession ends an authenticated stream on expiry or revocation.
// Passive checks do not extend browser idle time.
func (a *Access) watchSession(ctx context.Context) (context.Context, func()) {
	check := gatewayCheck(ctx)
	if c, ok := credentialFrom(ctx); ok {
		check = func(ctx context.Context) error {
			_, err := a.service.Authenticate(ctx, c.token, c.identity.Session.Audience, false)
			return err
		}
	}
	if check == nil {
		return ctx, func() {}
	}
	ctx, cancel := context.WithCancelCause(ctx)
	go func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if err := check(ctx); err != nil {
					cancel(err)
					return
				}
			}
		}
	}()
	return ctx, func() { cancel(context.Canceled) }
}

func (s *server) accessHealth(w http.ResponseWriter, r *http.Request) {
	if s.Access == nil {
		s.health(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	ctx, err := s.Access.authRequest(r, false)
	if actor, _ := principal.From(ctx); !s.Access.Pending() && err == nil && actor.IsHuman() {
		s.health(w, r.WithContext(ctx))
		return
	}
	status := http.StatusOK
	health := map[string]any{
		"status": "ok", "db": "ok", "service": buildinfo.Service,
		"version": buildinfo.Version, "apiVersion": buildinfo.APIVersion, "sharedAccess": true, "setupPending": s.Access.Pending(),
	}
	// The managed service checks its process against the data-dir holder
	// over the local product socket. Proxy browsers keep their public Host.
	if guard.Unix(r) && r.Host == "stage" {
		health["instanceId"] = s.Instance.InstanceID
	}
	if _, err := s.Store.SchemaVersion(r.Context()); err != nil {
		health["status"], health["db"], status = "error", "error", http.StatusServiceUnavailable
	}
	writeJSON(w, status, health)
}

func validBrowserWrite(a *Access, r *http.Request) bool {
	return guard.PrivatePeer(r) && guard.CanonicalBrowser(r, a.origin, a.host)
}

func nativeLogin(a *Access, r *http.Request) bool {
	return guard.PrivatePeer(r) && len(r.Header.Values("Origin")) == 0 &&
		r.Header.Get("Sec-Fetch-Site") == "" && r.Header.Get("Cookie") == "" &&
		r.Header.Get("Authorization") == "" &&
		(strings.EqualFold(r.Host, a.host) || guard.Unix(r))
}
