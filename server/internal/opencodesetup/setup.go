// Package opencodesetup binds an existing OpenCode service to its Stage identity.
package opencodesetup

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/opencode"
)

type Service struct {
	mu         sync.Mutex
	id         int64
	store      *db.Store
	env, saved config.OpenCodeSettings
	verified   string
	problem    string
	active     *opencode.Adapter
	// install moves the OpenCode adapter from connection old to next
	// (equal unless the service was replaced).
	install func(old, next int64, a execution.Adapter)
}

func New(ctx context.Context, store *db.Store, env config.OpenCodeSettings, install func(old, next int64, a execution.Adapter)) (*Service, error) {
	id, err := store.EnsureOpenCode(ctx)
	if err != nil {
		return nil, err
	}
	saved, verified, err := store.OpenCodeSettings(ctx)
	if err != nil {
		return nil, err
	}
	s := &Service{id: id, store: store, env: env, saved: saved, verified: verified, install: install}
	cfg, _, err := s.resolve(saved)
	switch {
	case err != nil:
		s.problem = err.Error()
	case cfg.Endpoint == "":
	case verified != "" && verified != cfg.Endpoint:
		s.problem = "Service address changed. Verify this connection in Settings."
	default:
		a, err := opencode.New(id, cfg)
		if err != nil {
			s.problem = err.Error()
			break
		}
		// Record the original address before any task can acquire its adapter.
		if verified == "" {
			if err := store.SetConnectionIdentity(ctx, id, cfg.Endpoint, publicAddress(cfg.Endpoint)); err != nil {
				return nil, err
			}
			s.verified = cfg.Endpoint
		}
		s.active = a
		install(id, id, a)
	}
	return s, nil
}

// ID is the active OpenCode connection; it changes when the service is
// replaced.
func (s *Service) ID() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
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
	}
	if s.env.Username != "" || s.env.Password != "" {
		locked = append(locked, "credentials")
		if s.env.Endpoint == "" || s.env.Username == "" || s.env.Password == "" {
			return c, locked, db.Invalid("Set OPENCODE_URL, OPENCODE_USERNAME and OPENCODE_PASSWORD together to scope environment credentials to the service.")
		}
		c.Username, c.Password = s.env.Username, s.env.Password
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
	out := config.OpenCodeStatus{ConnectionID: id, Endpoint: publicAddress(c.Endpoint), BrowserEndpoint: publicAddress(c.BrowserEndpoint), WebClient: publicAddress(c.WebClient), HasCredentials: c.Password != "", EnvironmentFields: locked, State: "unconfigured"}
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
		if !h.Available {
			out.State, out.Reason = "unavailable", h.Reason
		}
	}
	return out
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
			return db.Conflict("This field is managed by the Stage server environment.")
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
	}
	if in.Password != nil || in.Username != nil {
		if s.env.Username != "" || s.env.Password != "" {
			return db.Conflict("Credentials are managed by the Stage server environment.")
		}
		if in.Username == nil || in.Password == nil {
			return db.Invalid("Supply both username and password to replace or clear credentials.")
		}
		next.Username, next.Password = *in.Username, *in.Password
		if s.env.Endpoint != "" {
			next.Endpoint = strings.TrimRight(s.env.Endpoint, "/")
		}
	}
	cfg, _, err := s.resolve(next)
	if err != nil {
		return err
	}
	a, err := opencode.New(s.id, cfg)
	if err != nil {
		return db.Invalid("%s", err)
	}
	call, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if h := a.Health(call); !h.Available {
		return db.Conflict("%s", h.Reason)
	}
	id, label := s.id, publicAddress(cfg.Endpoint)
	switch {
	case s.verified == "":
		if err := s.store.SetConnectionIdentity(ctx, id, cfg.Endpoint, label); err != nil {
			return err
		}
	case cfg.Endpoint != s.verified:
		continuity := s.verifyContinuity(call, a, id)
		held, err := s.holds(call, id)
		if err != nil {
			return err
		}
		if continuity == nil && held {
			// The same service at a new address.
			if err := s.store.SetConnectionIdentity(ctx, id, cfg.Endpoint, label); err != nil {
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
			if err := s.store.SetConnectionIdentity(ctx, id, cfg.Endpoint, label); err != nil {
				return err
			}
			break
		}
		if !in.NewInstance {
			if revive != 0 {
				return &db.ConflictError{Code: "instance_returned", Msg: "This address serves the OpenCode service Stage used before. Connect it again: its tasks and bindings come back, and the current connection becomes read-only."}
			}
			msg := "This address serves a different OpenCode service. Connect it as a new service: the current connection becomes read-only and keeps its tasks."
			if continuity != nil {
				msg = "This address serves a different OpenCode service (" + continuity.Error() + "). Connect it as a new service: the current connection becomes read-only and keeps its tasks."
			}
			return &db.ConflictError{Code: "instance_changed", Msg: msg}
		}
		sw, err := s.store.SwitchInstance(ctx, db.HarnessOpenCode, cfg.Endpoint, label, revive)
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
	if err := s.store.SaveOpenCodeSettings(ctx, next); err != nil {
		return err
	}
	old := s.id
	s.saved, s.verified, s.active, s.problem, s.id = next, cfg.Endpoint, a, "", id
	s.install(old, id, a)
	return nil
}

// holds reports whether a connection stores anything a service must prove
// it serves (bindings, sessions, or a launch without a known session).
func (s *Service) holds(ctx context.Context, id int64) (bool, error) {
	bindings, sessions, unknown, err := s.store.ConnectionEvidence(ctx, id)
	return unknown || len(bindings)+len(sessions) > 0, err
}

// previous is the replaced OpenCode connection the candidate service proves
// to be (0 when none): it serves that connection's workspaces and sessions.
func (s *Service) previous(ctx context.Context, a *opencode.Adapter) (int64, error) {
	ids, err := s.store.RetiredConnections(ctx, db.HarnessOpenCode)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		bindings, sessions, unknown, err := s.store.ConnectionEvidence(ctx, id)
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
	bindings, sessions, unknown, err := s.store.ConnectionEvidence(ctx, id)
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
