// Package workersetup composes a worker's connection setup (ADR 0002,
// docs/execution.md "Remote machines"): the real T3 and OpenCode setups,
// connectors and adapters of the worker's machine, built by the same
// constructors the main server's own machine uses, over ports that reach
// the main server through the worker channel (Ports). Harness credentials
// stay in the worker's credentials file. Harness settings set in the
// worker's environment lock those settings for this machine.
package workersetup

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
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
	T3     t3.Client
	Logger *slog.Logger
}

// Setup composes the connection setup once the main server is reachable.
type Setup struct {
	o   Options
	log *slog.Logger

	mu       sync.Mutex
	composed bool
}

// Attach composes o.Host's connection setup each time a channel session
// goes online, until once succeeds (the setup needs the main server's
// record of the machine).
func Attach(o Options) *Setup {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	s := &Setup{o: o, log: o.Logger.With("component", "workersetup")}
	if w := o.Config.WarnRemovedEnv(); w != "" {
		s.log.Warn(w)
	}
	o.Host.OnAttached(s.compose)
	return s
}

// compose builds the setup (no-op once composed). ctx is the worker's: the
// setups' background work ends with it.
func (s *Setup) compose(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.composed || ctx.Err() != nil {
		return
	}
	if err := s.build(ctx); err != nil {
		s.log.Warn("compose harness setup; retrying when the channel reconnects", "err", err)
		return
	}
	s.composed = true
	s.o.Host.SetReady()
	s.log.Info("harness setup ready")
}

func (s *Setup) build(ctx context.Context) error {
	local, ports, cfg := s.o.Host.Local(), s.o.Ports, s.o.Config
	call, cancel := context.WithTimeout(ctx, remote.CallTimeout)
	defer cancel()
	conn, err := ports.GetConnection(call)
	if err != nil {
		return err
	}
	// The client's background loop ends with the worker, or now when the
	// setup is not composed.
	clientCtx, stopClient := context.WithCancel(ctx)
	ok := false
	defer func() {
		if !ok {
			stopClient()
		}
	}()
	endpoint := t3setup.Resolve(conn)
	admin, err := ports.LoadT3AdminToken(call)
	if err != nil {
		return err
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
			mc.Run(clientCtx)
			mc.Close()
		}()
		client = mc
		s.log.Info("using T3 MCP client", "url", endpoint.URL, "source", endpoint.Source)
	}
	openCode, err := opencodesetup.New(ctx, opencodesetup.Ports{
		Settings: ports, Identity: ports, Install: local.Install,
		// The machine's OpenCode connection is created by the first service
		// that connects.
		Active: func(ctx context.Context) (int64, error) { return ports.Active(ctx, opencode.Kind) },
	}, s.log)
	if err != nil {
		return err
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
	local.SetConnector(t3.Kind, t3setup.NewConnector(t3Setup, nil))
	local.SetConnector(opencode.Kind, opencodesetup.NewConnector(openCode))
	ok = true
	return nil
}
