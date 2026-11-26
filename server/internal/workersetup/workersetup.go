// Package workersetup composes a worker's connection setup (ADR 0002,
// docs/execution.md "Remote machines"): the real T3, OpenCode and (where
// the machine allows local harnesses) Claude Code and Codex setups,
// connectors and adapters of the worker's machine, built by the same
// constructors the main server's own machine uses, over ports that reach
// the main server through the worker channel (Ports). Harness credentials
// stay in the worker's credentials file. Harness settings set in the
// worker's environment lock those settings for this machine.
package workersetup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/claudecodesetup"
	"github.com/flexdinesh/stage/server/internal/codex"
	"github.com/flexdinesh/stage/server/internal/codexsetup"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/opencode"
	"github.com/flexdinesh/stage/server/internal/opencodesetup"
	"github.com/flexdinesh/stage/server/internal/remote"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/t3setup"
)

// Options configure Attach.
type Options struct {
	// Host is the worker's machine host; its Local receives the adapters
	// and connectors.
	Host *remote.Host
	// Ports reach the main server and the credentials file.
	Ports Ports
	// Config is the worker's harness settings (config.HarnessFrom).
	Config config.Config
	// T3 overrides the T3 client (tests); nil builds it from Config.
	T3 t3.Client
	// Claude overrides Claude Code's machine-local environment (tests);
	// nil: this process's, with a systemd scope where one works.
	Claude *claudecodesetup.Options
	// Codex overrides Codex's machine-local options (tests: the daemon);
	// nil: the codex CLI on this machine. Its ports are always the worker's.
	Codex *codexsetup.Options
	// StateDir is the worker's own folder: harness setups keep machine-local
	// files under it (Claude Code's hook settings in <StateDir>/claude,
	// unless Claude sets its own StateDir).
	StateDir string
	Logger   *slog.Logger
}

// Setup composes the connection setup once the main server is reachable.
type Setup struct {
	o   Options
	log *slog.Logger

	mu       sync.Mutex
	composed map[string]bool
}

// Attach composes o.Host's connection setup each time a channel session
// goes online, until once succeeds (the setup needs the main server's
// record of the machine).
func Attach(o Options) *Setup {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	s := &Setup{o: o, log: o.Logger.With("component", "workersetup"), composed: map[string]bool{}}
	if w := o.Config.WarnRemovedEnv(); w != "" {
		s.log.Warn(w)
	}
	o.Host.OnAttached(s.compose)
	return s
}

// compose installs the allowed connectors supported by this server before ready.
// The worker owns the lifetime of each installed connector.
func (s *Setup) compose(ctx context.Context, serverKinds []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	local, ports := s.o.Host.Local(), s.o.Ports
	for _, setup := range []struct {
		descriptor harness.Descriptor
		build      func() (harness.Connector, error)
	}{
		{t3.Descriptor, func() (harness.Connector, error) { return s.t3(ctx) }},
		{opencode.Descriptor, func() (harness.Connector, error) {
			service, err := opencodesetup.New(ctx, opencodesetup.Ports{
				Settings: ports, Identity: ports, Install: local.Install,
				Active: func(ctx context.Context) (int64, error) { return ports.Active(ctx, opencode.Kind) },
			}, s.log)
			if err != nil {
				return nil, err
			}
			return opencodesetup.NewConnector(service), nil
		}},
		{claudecode.Descriptor, func() (harness.Connector, error) {
			service, err := s.claude(ctx, ports, local.Install)
			if err != nil {
				return nil, err
			}
			return claudecodesetup.NewConnector(service), nil
		}},
		{codex.Descriptor, func() (harness.Connector, error) {
			service, err := s.codex(ctx, ports, local.Install)
			if err != nil {
				return nil, err
			}
			return codexsetup.NewConnector(service), nil
		}},
	} {
		d := setup.descriptor
		if s.composed[d.Kind] || !local.Allows(d).Available || !slices.Contains(serverKinds, d.Kind) {
			continue
		}
		connector, err := setup.build()
		if errors.Is(err, errNotServed) {
			s.log.Warn("main machine does not serve harness setup", "kind", d.Kind, "err", err)
			continue
		}
		if err != nil {
			return fmt.Errorf("%s: %w", d.Name, err)
		}
		local.SetConnector(d.Kind, connector)
		s.composed[d.Kind] = true
	}
	return nil
}

func (s *Setup) t3(ctx context.Context) (harness.Connector, error) {
	local, ports, cfg := s.o.Host.Local(), s.o.Ports, s.o.Config
	call, cancel := context.WithTimeout(ctx, remote.CallTimeout)
	defer cancel()
	conn, err := ports.GetConnection(call)
	if err != nil {
		return nil, err
	}
	endpoint := t3setup.Resolve(conn)
	admin, err := ports.LoadT3AdminToken(call)
	if err != nil {
		return nil, err
	}
	client := s.o.T3
	switch {
	case client != nil:
	case cfg.T3Fake:
		fake := t3.NewFake()
		if cfg.T3FakeSeed {
			fake.SeedDemo()
		}
		client = fake
		s.log.Info("using fake T3 client")
	default:
		mc := t3.NewMCPClient(t3.Options{
			BaseURL: endpoint.URL, AdminToken: admin, PublicURL: endpoint.PublicURL, ClientName: cfg.T3ClientName,
			Store: ports, HTTPClient: &http.Client{Timeout: 60 * time.Second}, Logger: s.log,
		})
		go func() {
			mc.Run(ctx)
			mc.Close()
		}()
		client = mc
		s.log.Info("using T3 MCP client", "url", endpoint.URL, "source", endpoint.Source)
	}
	t3Setup := t3setup.New(t3setup.Options{
		Config: cfg, Settings: ports, Identity: ports, Client: client, Endpoint: endpoint, Fake: t3setup.IsFake(client), Logger: s.log,
		AdminTokens: ports, AdminToken: admin,
		// Pairing created the machine's T3 connection, or moved T3 to
		// another one.
		OnSwitched: func(sw db.Switch) { local.Install(sw.Old, sw.New, t3.NewConnectionAdapter(client, sw.New)) },
	})
	// No T3 connection until the first pairing.
	if conn.ConnectionID != 0 {
		local.Install(conn.ConnectionID, conn.ConnectionID, t3.NewConnectionAdapter(client, conn.ConnectionID))
	}
	return t3setup.NewConnector(t3Setup, nil), nil
}

func (s *Setup) claude(ctx context.Context, ports Ports, install machine.Install) (*claudecodesetup.Service, error) {
	o := claudecodesetup.Options{Scope: claudecode.ScopeAvailable()}
	if s.o.Claude != nil {
		o = *s.o.Claude
	}
	o.Logger = s.log
	if o.StateDir == "" && s.o.StateDir != "" {
		o.StateDir = filepath.Join(s.o.StateDir, "claude")
	}
	return claudecodesetup.New(ctx, claudecodesetup.Ports{Settings: ports, Identity: ports, Install: install}, o)
}

// codex uses the worker's own daemon and keeps it running when Stage exits.
func (s *Setup) codex(ctx context.Context, ports Ports, install machine.Install) (*codexsetup.Service, error) {
	var o codexsetup.Options
	if s.o.Codex != nil {
		o = *s.o.Codex
	}
	o.Ports = codexsetup.Ports{Settings: ports, Identity: ports, Install: install}
	if o.Version == "" {
		o.Version = buildinfo.Version
	}
	o.Logger = s.log
	c, err := codexsetup.New(ctx, o)
	if err != nil {
		return nil, err
	}
	context.AfterFunc(ctx, c.Close)
	return c, nil
}
