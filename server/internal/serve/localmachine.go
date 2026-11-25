package serve

import (
	"context"
	"log/slog"
	"net/http"
	"path/filepath"
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

// localMachine is the machine in this process: its T3 client and adapter,
// and (after compose) its T3 and OpenCode setups and connectors. Setup code
// reaches the store only through the machine's store-backed ports.
type localMachine struct {
	*machine.Local
	client   t3.Client
	endpoint t3setup.Endpoint
	// adminToken is the saved T3 admin token the client was built with.
	adminToken string
	setup      *t3setup.Service
	// codex is the Codex setup, composed only when the machine allows it.
	codex *codexsetup.Service
	// close releases the T3 client (nil for an injected or fake client).
	close func()
}

// newLocalMachine builds the local machine's T3 client (injected, the fake
// with T3_FAKE=1, else the MCP client, run on wg until ctx ends) and
// installs its adapter on the machine's active T3 connection. The T3 URL,
// browser URL and admin token are the ones saved in Settings.
func newLocalMachine(ctx context.Context, wg *sync.WaitGroup, store *db.Store, cfg config.Config, client t3.Client, profile harness.Profile, logger *slog.Logger) (*localMachine, error) {
	on := store.OnMachine(db.LocalMachine)
	conn, err := on.GetConnection(ctx)
	if err != nil {
		return nil, err
	}
	admin, err := on.LoadT3AdminToken(ctx)
	if err != nil {
		return nil, err
	}
	m := &localMachine{Local: machine.NewLocal(db.LocalMachine, profile, machine.LocalOps{}), endpoint: t3setup.Resolve(conn), adminToken: admin}
	switch {
	case client != nil:
	case cfg.T3Fake:
		// T3_FAKE=1 forces the in-memory fake (dev/demo without a T3 server).
		fake := t3.NewFake()
		// T3_FAKE_SEED=1 seeds demo threads waiting on questions (UI work).
		if cfg.T3FakeSeed {
			fake.SeedDemo()
		}
		client = fake
		logger.Info("using fake T3 client")
	default:
		// The machine's T3 token is the client's credential store.
		mc := t3.NewMCPClient(t3.Options{
			BaseURL: m.endpoint.URL, AdminToken: admin, PublicURL: m.endpoint.PublicURL, ClientName: cfg.T3ClientName,
			Store: on, HTTPClient: &http.Client{Timeout: 60 * time.Second}, Logger: logger,
		})
		m.close = mc.Close
		wg.Add(1)
		go func() { defer wg.Done(); mc.Run(ctx) }()
		client = mc
		logger.Info("using T3 MCP client", "url", m.endpoint.URL, "source", m.endpoint.Source)
	}
	m.client = client
	t3ID := on.ActiveID(t3.Kind)
	m.Install(t3ID, t3ID, t3.NewConnectionAdapter(client, t3ID))
	return m, nil
}

// compose builds the machine's setups over its store-backed ports and
// registers their connectors. Set the machine's change hook first: the
// OpenCode setup installs its adapter here. A hosted harness is composed
// only when the machine allows it (m.Allows), so a profile without local
// processes never runs its processes; its connect action creates its
// connection through the identity port (machine.Connect).
func (m *localMachine) compose(ctx context.Context, store *db.Store, cfg config.Config, reg *harness.Registry, onPaired func(), progress func(connection int64) t3setup.SyncProgress, logger *slog.Logger) error {
	ports := machine.NewStorePorts(store.OnMachine(m.ID()), reg)
	openCode, err := opencodesetup.New(ctx, opencodesetup.Ports{Settings: ports, Identity: ports, Install: m.Install}, logger)
	if err != nil {
		return err
	}
	m.setup = t3setup.New(t3setup.Options{
		Config: cfg, Settings: ports, Identity: ports, Client: m.client, Endpoint: m.endpoint, Fake: t3setup.IsFake(m.client),
		AdminTokens: ports, AdminToken: m.adminToken,
		OnPaired: onPaired, Logger: logger,
		// A different T3 environment moved T3 to another connection.
		OnSwitched: func(sw db.Switch) { m.Install(sw.Old, sw.New, t3.NewConnectionAdapter(m.client, sw.New)) },
	})
	m.SetConnector(t3.Kind, t3setup.NewConnector(m.setup, progress))
	m.SetConnector(opencode.Kind, opencodesetup.NewConnector(openCode))
	// Claude Code runs as local processes: only where this machine allows.
	if m.Allows(claudecode.Descriptor).Available {
		claude, err := claudecodesetup.New(ctx, claudecodesetup.Ports{Settings: ports, Identity: ports, Install: m.Install}, claudecodesetup.Options{Scope: claudecode.ScopeAvailable(), StateDir: filepath.Join(cfg.DataDir, "claude"), Logger: logger})
		if err != nil {
			return err
		}
		m.SetConnector(claudecode.Kind, claudecodesetup.NewConnector(claude))
	}
	// Codex is hosted: its daemon runs on this machine, so it is composed
	// only where the profile allows local processes.
	if m.Allows(codex.Descriptor).Available {
		m.codex, err = codexsetup.New(ctx, codexsetup.Options{
			Ports:   codexsetup.Ports{Settings: ports, Identity: ports, Install: m.Install},
			Version: buildinfo.Version, Logger: logger,
		})
		if err != nil {
			return err
		}
		m.SetConnector(codex.Kind, codexsetup.NewConnector(m.codex))
	}
	return nil
}

// remoteSetupHooks connect remote machines' connectors to this server:
// every registered kind (a hosted one only where the worker's profile
// allows it, remote.Machine.Allows), the active connection from the
// store, the full sync on a T3 status, and a sync after a setup change
// (as onPaired does for the local machine).
func remoteSetupHooks(store *db.Store, reg *harness.Registry, progress func(connection int64) t3setup.SyncProgress, kick func()) remote.SetupHooks {
	var kinds []string
	for _, d := range reg.All() {
		kinds = append(kinds, d.Kind)
	}
	return remote.SetupHooks{
		Kinds:  kinds,
		Active: store.ActiveID,
		Decorate: func(_ int64, kind string, st *harness.Status) {
			if kind != t3.Kind || progress == nil {
				return
			}
			d, ok := st.Details.(map[string]any)
			if !ok {
				return
			}
			d["sync"] = progress(st.ConnectionID)
		},
		Changed: func(int64, string, string) { kick() },
	}
}
