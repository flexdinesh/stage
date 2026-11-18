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
	install    func(int64, execution.Adapter)
}

func New(ctx context.Context, store *db.Store, env config.OpenCodeSettings, install func(int64, execution.Adapter)) (*Service, error) {
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
			if err := store.SaveOpenCodeSettings(ctx, saved, cfg.Endpoint); err != nil {
				return nil, err
			}
			s.verified = cfg.Endpoint
		}
		s.active = a
		install(id, a)
	}
	return s, nil
}

func (s *Service) ID() int64 { return s.id }

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
	s.mu.Unlock()
	out := config.OpenCodeStatus{ConnectionID: s.id, Endpoint: publicAddress(c.Endpoint), BrowserEndpoint: publicAddress(c.BrowserEndpoint), WebClient: publicAddress(c.WebClient), HasCredentials: c.Password != "", EnvironmentFields: locked, State: "unconfigured"}
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
	if s.verified != "" && cfg.Endpoint != s.verified {
		if err := s.verifyContinuity(call, a); err != nil {
			return err
		}
	}
	if err := s.store.SaveOpenCodeSettings(ctx, next, cfg.Endpoint); err != nil {
		return err
	}
	s.saved, s.verified, s.active, s.problem = next, cfg.Endpoint, a, ""
	s.install(s.id, a)
	return nil
}

func (s *Service) verifyContinuity(ctx context.Context, a *opencode.Adapter) error {
	bindings, sessions, unknown, err := s.store.ConnectionEvidence(ctx, s.id)
	if err != nil {
		return err
	}
	if unknown || (len(bindings) > 0 && len(sessions) == 0) {
		return db.Conflict("A stored workspace or launch lacks a verifiable session identity. Restore the previous address and resolve any pending launch before changing services.")
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
			return db.Conflict("The candidate service does not match a stored workspace. Restore the previous service address.")
		}
	}
	for _, old := range sessions {
		o, err := a.Observe(ctx, old.Session)
		if err != nil || old.CreatedAt.IsZero() || !o.CreatedAt.Equal(old.CreatedAt) || o.Project != old.Project || o.Directory != old.Directory {
			return db.Conflict("The candidate service could not verify stored session %s. Check the service address.", old.Session.ExternalSessionID)
		}
	}
	return nil
}
