// Package serve runs the Stage server process: it owns the data dir, opens
// every configured listener, and serves one set of handlers, store and
// background runtime on all of them (docs/runtime.md).
package serve

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/auth"
	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/sqlite"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/engine"
	"github.com/flexdinesh/stage/server/internal/guard"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/harnesses"
	"github.com/flexdinesh/stage/server/internal/install"
	"github.com/flexdinesh/stage/server/internal/instance"
	"github.com/flexdinesh/stage/server/internal/listen"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/notify"
	"github.com/flexdinesh/stage/server/internal/ownercontrol"
	"github.com/flexdinesh/stage/server/internal/principal"
	"github.com/flexdinesh/stage/server/internal/release"
	"github.com/flexdinesh/stage/server/internal/remote"
	"github.com/flexdinesh/stage/server/internal/schedule"
	"github.com/flexdinesh/stage/server/internal/webui"
	"github.com/flexdinesh/stage/server/internal/workerchan"
	"github.com/flexdinesh/stage/server/internal/workevent"
)

// ShutdownTimeout lets in-flight launches (bounded at 90s) record their
// session: a kill mid-launch leaves the task failed with the outcome unknown.
// Service managers' stop timeouts (compose stop_grace_period) must exceed it.
const ShutdownTimeout = 100 * time.Second

// Options configure Run.
type Options struct {
	Config config.Config
	// BootstrapOutput designates private foreground output. When absent, and
	// always for managed services and containers, use a separate private file.
	BootstrapOutput io.Writer
	// HarnessOverrides injects machine-local dependencies keyed by kind in tests.
	HarnessOverrides map[string]any
	Logger           *slog.Logger
	// Ready, when set, is called once every listener serves.
	Ready func(Ready)
	// ShutdownTimeout overrides ShutdownTimeout (tests).
	ShutdownTimeout time.Duration
	// Service, when set, marks this process as the per-user service
	// (`stage serve --managed`) in the data dir's holder record.
	Service *Service
	// Profile is the machine profile (harness.ResolveProfile): which
	// harnesses this server may host. Zero is a foreground server that runs
	// no local processes.
	Profile harness.Profile
	// OnLocked, when set, runs once the data dir lock is held and before
	// anything else; a non-nil logger it returns replaces Logger (the macOS
	// managed log file, which a losing process must never rotate).
	OnLocked func() (*slog.Logger, error)
}

// Service names the manager supervising a managed server.
type Service struct {
	Manager string // "systemd" | "launchd"
	Name    string // unit name or launchd label
}

// Ready describes a serving instance.
type Ready struct {
	DataDir   string        // canonical
	Listeners []listen.Addr // resolved
	AgentURL  string        // owner-private local agent endpoint, when shared
	Meta      instance.Meta
}

type agentListenerKey struct{}

// Run serves until ctx ends, then shuts down gracefully. Order:
//  1. lock the data dir (a second server on it, on any address, exits here)
//  2. open every listener (a failed bind closes the others; the database
//     is not touched yet)
//  3. open the engine STAGE_DATABASE_URL selects (SQLite in the data dir,
//     or Postgres: opening and its ownership lock bounded at 30s, held
//     elsewhere 75), refuse a schema newer than this binary (78), back it
//     up when migrations are pending, migrate, compose harnesses, recover launching
//     tasks and start the background loops
//  4. serve all listeners, then notify READY=1 (systemd)
//
// Shutdown: STOPPING=1, background loops and notification streams end, the
// HTTP server drains for up to ShutdownTimeout, then harness clients and the database
// close, sockets are removed with their locks released, and the data dir
// lock is released last.
func Run(ctx context.Context, o Options) error {
	cfg := o.Config
	logger := o.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if len(cfg.Listen) == 0 {
		return errors.New("serve: no listen address")
	}
	owner, err := instance.Lock(cfg.DataDir)
	if err != nil {
		return err
	}
	defer owner.Release()
	if o.OnLocked != nil {
		l, err := o.OnLocked()
		if err != nil {
			return err
		}
		if l != nil {
			logger = l
		}
	}
	cfg.DataDir = owner.DataDir
	if err := cfg.ResolveSockets(); err != nil {
		return config.Invalid(err)
	}
	lns, err := listen.OpenAll(cfg.Listen)
	if err != nil {
		return err
	}
	defer func() {
		for _, l := range lns {
			_ = l.Close()
		}
	}()
	meta := instance.NewMeta()
	if o.Service != nil {
		meta.Managed, meta.Manager, meta.Service = true, o.Service.Manager, o.Service.Name
	}
	addrs := make([]listen.Addr, len(lns))
	for i, l := range lns {
		addrs[i] = l.Spec
		meta.Listeners = append(meta.Listeners, l.Spec.URL())
	}
	if err := owner.Record(meta); err != nil {
		return err
	}
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	pool, err := openEngine(ctx, cfg, meta.InstanceID, logger)
	if err != nil {
		return err
	}
	defer pool.Close()
	lease, err := acquireOwnership(ctx, cfg, pool)
	if err != nil {
		return err
	}
	defer lease.Release()
	errOwnershipLost := store.ErrOwnershipLost
	if err := migrate(ctx, pool, logger); err != nil {
		return err
	}
	database := db.NewStore(pool)
	sharedRequested := cfg.MultiUser != nil && *cfg.MultiUser
	intent := cfg.MultiUser
	if sharedRequested {
		// Only the account service can opt in, along with the first setup grant.
		intent = nil
	}
	state, err := database.ConfigureAccess(ctx, intent)
	if err != nil {
		if errors.Is(err, db.ErrSharedAccessRequired) {
			return config.Invalid(err)
		}
		return err
	}
	var access *api.Access
	var control *ownercontrol.Server
	var localAgent *listen.Listener
	var agentURL string
	var setup auth.Link
	var prepareWork func(time.Time)
	workReady := make(chan time.Time, 1)
	origins := cfg.AllowedOrigins
	if sharedRequested || state.Mode != store.AccessPersonal {
		origin, err := sharedOrigin(cfg.PublicURL, lns)
		if err != nil {
			return config.Invalid(err)
		}
		origins = append(append([]string(nil), origins...), origin)
		cfg.PublicURL = origin
		agentSocket, err := config.AgentSocket(cfg.Getenv, cfg.DataDir)
		if err != nil {
			return config.Invalid(err)
		}
		agentAddr := listen.Addr{Network: "unix", Address: agentSocket, Private: true}
		localAgent, err = agentAddr.Listen()
		if err != nil {
			return config.Invalid(fmt.Errorf("open private agent socket: %w", err))
		}
		defer localAgent.Close()
		agentURL = agentAddr.URL()
		svc := auth.New(database, origin, time.Now)
		control, err = ownercontrol.Open(cfg.DataDir, svc)
		if err != nil {
			return config.Invalid(fmt.Errorf("open private owner socket: %w", err))
		}
		defer control.Close()
		if err := control.DiscardBootstrap(); err != nil {
			return fmt.Errorf("clear private startup output: %w", err)
		}
		defer control.DiscardBootstrap()
		state, setup, err = svc.Start(ctx, cfg.MultiUser)
		if err != nil {
			return errors.New(auth.PublicMessage(err))
		}
		access = api.NewAccess(svc, origin, state, func(at time.Time) {
			if err := control.DiscardBootstrap(); err != nil {
				logger.Warn("Could not remove private setup output. Remove owner/bootstrap.txt from the data directory.")
			}
			prepareWork(at)
		})
	}
	policy, err := guard.New(origins, logger)
	if err != nil {
		return config.Invalid(err)
	}
	if w := cfg.WarnRemovedEnv(); w != "" {
		logger.Warn(w)
	}
	store := database

	var wg sync.WaitGroup
	var local *localMachine
	defer func() {
		stop()
		wg.Wait() // background loops exit once ctx ends
		if local != nil {
			local.close()
		}
	}()

	// Agent instructions name the CLI this identity installed on PATH.
	home, _ := os.UserHomeDir()
	cliName := install.CLIName(home, buildinfo.Identity)
	// The fixed harness composition: every kind this server knows, in order.
	registry := harnesses.Registry()
	profile := o.Profile
	if profile.Kind == "" {
		profile.Kind = harness.ProfileServer
	}
	nameLocalMachine(ctx, store, profile, cfg.PublicURL, logger)
	// The machine-runtime seam: the in-process local machine, plus a remote
	// machine per joined worker (below, ADR 0002).
	local = &localMachine{Local: machine.NewLocal(db.LocalMachine, profile, machine.LocalOps{})}
	machines := machine.MustSet(local)
	eng := engine.New(engine.Options{Store: store, Machines: machines, Harnesses: registry, Log: logger})
	eng.Coordinator.PublicURL = cfg.PublicURL
	eng.Coordinator.SharedAccess = access != nil
	eng.Coordinator.LocalAgentURL = agentURL
	eng.Coordinator.CLI = cliName
	// Retires connections whose instance moved to another machine (moved.go).
	moved := newMovedRetirer(store, eng.Discovery.FullSyncEvery, logger)
	eng.Discovery.OnFullSync = moved.Kick
	notifier := notify.New(store, cfg.PublicURL, logger)
	notifier.SharedAccess = access != nil
	// Holds members' needs_input notifications while a steerer can answer
	// (ADR 0013); kicked by transitions and tree or steering changes.
	holds := notify.NewHolds(notifier)
	holds.CanAnswer = eng.Coordinator.CanAnswer
	notifier.Holds = holds
	// One release and one schedule runner per active connection, on every
	// machine; the sets follow instance switches.
	releases := newPerConnection(store, func(id int64) *release.Runner {
		r := release.NewRunner(store, eng.Coordinator, logger)
		r.ConnectionID = id
		r.OnReady = notifier.OnReady
		return r
	})
	var scheduleSince time.Time
	schedules := newPerConnection(store, func(id int64) *schedule.Runner {
		sc := schedule.NewRunner(store, eng.Coordinator, logger)
		sc.ConnectionID = id
		sc.Since = scheduleSince
		return sc
	})
	kickTrees := func() {
		releases.Each(func(r *release.Runner) { r.Kick() })
		holds.Kick()
	}
	// The coordinator and runners follow the machine's adapters.
	local.OnChange(func(old, next int64) {
		eng.ConnectionsChanged(old, next)
		releases.Resync()
		schedules.Resync()
		moved.Kick()
	})
	// One inbox hub wakes notification streams on inserts (notifier), reads
	// (API), prunes and task transitions (badge). Streams end with ctx.
	inbox := notify.NewHub(ctx)
	notifier.Inbox = inbox
	// Hooks only queue work; set before RecoverLaunching emits transitions.
	eng.Coordinator.OnTransition = func(ctx context.Context, c workevent.TaskChange) {
		releases.Each(func(r *release.Runner) { r.OnTransition(ctx, c) })
		eng.Observe.Kick()
		notifier.OnTransition(ctx, c)
		holds.Kick()
		inbox.Signal()
	}
	eng.Coordinator.OnObservedTransition = func(ctx context.Context, c workevent.TaskChange) {
		releases.Each(func(r *release.Runner) { r.OnTransition(ctx, c) })
		eng.Observe.Kick()
		holds.Kick()
		inbox.Signal()
	}
	eng.Observe.RecordNotification = notifier.RecordObservation
	eng.Coordinator.OnAccepted = kickTrees
	if err := local.compose(ctx, store, cfg, registry, o.HarnessOverrides, eng.Kick, cliName, logger); err != nil {
		return err
	}
	// Worker channels end with ctx, so Shutdown does not wait on their
	// streams (docs/runtime.md "Worker channel").
	channels := workerchan.NewHub(ctx, workerchan.Options{
		Version: buildinfo.Version,
		Seen: api.TouchMachineFunc(store, func(err error) {
			logger.Warn("machine presence", "err", err)
		}),
		AllowUpcall: func(kind string) bool {
			if !access.Pending() {
				return true
			}
			switch kind {
			case remote.KindSetupSnapshot, remote.KindSettingsGet, remote.KindStatusError,
				remote.KindIdentityActive, remote.KindIdentityActiveIdentity, remote.KindIdentitySet, remote.KindIdentitySwitch:
				// Existing workers can reconcile their own harness
				// identities so they can advertise a ready snapshot.
				return true
			default:
				return false
			}
		},
	})
	eng.ServeWorkers(channels)
	// Remote machines: each joined machine's connections are proxied over
	// its worker channel (ADR 0002, docs/execution.md "Remote machines").
	hosts := remote.NewHosts(machines, channels, func(machineID int64) []int64 {
		var ids []int64
		for _, c := range store.ActiveConnections() {
			if c.Machine == machineID {
				ids = append(ids, c.ID)
			}
		}
		return ids
	}, func() {
		eng.Kick()
		releases.Resync()
		schedules.Resync()
		moved.Kick()
	}, logger)
	// Remote connection setup: each worker runs its machine's connectors;
	// the main server proxies them and answers their setup ports from its
	// record (never storing harness credentials).
	hosts.SetSetup(remoteSetupHooks(store, registry, eng.Kick))
	remote.ServeSetup(channels, func(machineID int64) machine.StorePorts {
		return machine.NewStorePorts(store.OnMachine(machineID), registry)
	}, func(machineID int64, sw db.Switch) {
		if !channels.Presence(machineID).Online {
			return // the ready snapshot will wake sync and scheduling
		}
		eng.ConnectionsChanged(sw.Old, sw.New)
		releases.Resync()
		schedules.Resync()
		moved.Kick()
	}, harnesses.Modules())
	remotes, err := store.ListMachines(ctx)
	if err != nil {
		return err
	}
	for _, m := range remotes {
		if m.ID != machines.DefaultID() {
			hosts.Join(m.ID)
		}
	}
	if _, err := machines.Connectors(registry); err != nil {
		return err
	}
	moved.connectors = func() harness.Connectors {
		c, err := machines.Connectors(registry)
		if err != nil {
			logger.Error("connectors", "err", err)
		}
		return c
	}
	moved.reachable = func(id int64) bool {
		m, ok := machines.Get(id)
		return ok && m.Online()
	}
	moved.onRetired = func(id, old, next int64) {
		if m, ok := machines.Get(id); ok {
			m.Retired(old)
		}
		eng.ConnectionsChanged(old, next)
	}
	systemCtx := principal.With(ctx, principal.Actor{Kind: principal.System})
	prepareWork = func(at time.Time) {
		// Recover launching tasks before runners or product requests can act
		// on them. The successful claim calls this once after commit.
		eng.Coordinator.RecoverLaunching(systemCtx)
		workReady <- at
	}
	if !access.Pending() {
		var at time.Time
		if state.ClaimedAt != nil {
			at = *state.ClaimedAt
		}
		prepareWork(at)
	}
	wg.Go(func() {
		select {
		case <-systemCtx.Done():
			return
		case scheduleSince = <-workReady:
		}
		wg.Go(func() { notifier.Run(systemCtx) })
		wg.Go(func() { holds.Run(systemCtx) })
		wg.Go(func() { pruneNotifications(systemCtx, store, logger, inbox.Signal) })
		wg.Go(func() { eng.Run(systemCtx) })
		wg.Go(func() { releases.Run(systemCtx) })
		wg.Go(func() { schedules.Run(systemCtx) })
		wg.Go(func() { moved.Run(systemCtx) })
		wg.Go(func() { sweepStagedAttachments(systemCtx, store, logger) })
	})

	mux := http.NewServeMux()
	// The MCP handler serves through the API router, and the router's agent
	// gateway route serves MCP: bound once both exist, before serving.
	var mcpHandler http.Handler
	// Transcript pollers and their event streams end with ctx, so Shutdown
	// does not wait on open streams.
	apiHandler := api.NewRouter(api.Deps{
		Config: cfg, Store: store, Engine: eng, Push: notifier, Holds: holds, Logger: logger,
		Inbox: inbox, Instance: meta, OnTreeChange: kickTrees,
		Access: access, InstallationID: state.ID,
		LiveConnectors: func() harness.Connectors {
			c, err := machines.Connectors(registry)
			if err != nil {
				logger.Error("connectors", "err", err)
			}
			return c
		},
		Harnesses: registry, Channels: channels, Machines: hosts,
		MCP: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { mcpHandler.ServeHTTP(w, r) }),
	})
	mcpHandler = access.MCP(agenttools.HTTPHandler(&agenttools.Ops{
		Client: client.NewInProcess(apiHandler, addrs[0].URL()), CLI: cliName,
	}))
	// Browser-facing endpoints get the request-origin policy; the UI's
	// static files do not need it.
	mux.Handle("/api/", policy.Wrap(apiHandler))
	// Streamable HTTP MCP: the agent tools over this server's own REST API,
	// served in-process (no dial back to a listener, tcp or unix), so
	// validation stays in one place. No local file access.
	// Machine tokens are worker-route credentials only (api.RejectMachineTokens;
	// /api applies it itself).
	mux.Handle("/mcp", policy.Wrap(mcpHandler))
	mux.Handle("/", webui.Handler())

	handler := http.Handler(mux)
	if localAgent != nil {
		agent := access.LocalAgent(mux)
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, local := r.Context().Value(agentListenerKey{}).(struct{}); local {
				agent.ServeHTTP(w, r)
				return
			}
			mux.ServeHTTP(w, r)
		})
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second,
		BaseContext: func(l net.Listener) context.Context {
			if localAgent != nil && l == localAgent {
				return context.WithValue(ctx, agentListenerKey{}, struct{}{})
			}
			return ctx
		}}
	errCh := make(chan error, len(lns)+2)
	for _, l := range lns {
		go func() { errCh <- srv.Serve(l) }()
		logger.Info("listening", "addr", l.Spec.String())
	}
	if localAgent != nil {
		go func() { errCh <- srv.Serve(localAgent) }()
		logger.Info("local agent socket ready", "url", agentURL)
	}
	if control != nil {
		wg.Go(func() { errCh <- control.Serve(ctx) })
		announceShared(o, control, cfg.DataDir, state, setup, logger)
	}
	if err := instance.Notify("READY=1"); err != nil {
		logger.Warn("readiness notification", "err", err)
	}
	if o.Ready != nil {
		o.Ready(Ready{DataDir: cfg.DataDir, Listeners: addrs, AgentURL: agentURL, Meta: meta})
	}
	var serveErr error
	select {
	case serveErr = <-errCh:
	case <-ctx.Done():
	case <-lease.Lost():
		logger.Error("database ownership lost")
		serveErr = errOwnershipLost
	}
	_ = instance.Notify("STOPPING=1")
	stop()
	timeout := o.ShutdownTimeout
	if timeout <= 0 {
		timeout = ShutdownTimeout
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	shutdownErr := srv.Shutdown(shutdownCtx)
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	if shutdownErr != nil && !errors.Is(shutdownErr, http.ErrServerClosed) {
		return shutdownErr
	}
	logger.Info("stopped")
	return nil
}

// migrate guards and migrates the schema through the engine's ports: a
// database newer than this binary is refused (exit 78); a non-empty one with
// pending migrations is first backed up (SQLite: <dataDir>/backups; a failed
// backup refuses to migrate).
func migrate(ctx context.Context, eng store.Engine, logger *slog.Logger) error {
	m := eng.Migrator()
	cur, err := m.Current(ctx)
	if err != nil {
		return err
	}
	known := m.Max()
	if cur > known {
		return config.Invalid(&db.SchemaTooNewError{Current: cur, Known: known})
	}
	if cur > 0 && cur < known {
		path, err := eng.Backup().Backup(ctx, cur, time.Now())
		if err != nil {
			return fmt.Errorf("refusing to migrate schema %d to %d: %w", cur, known, err)
		}
		if path != "" {
			logger.Info("database backup", "path", path, "schema", cur, "migrating_to", known)
			if err := sqlite.PruneBackups(filepath.Dir(path), sqlite.KeepBackups); err != nil {
				logger.Warn("prune database backups", "err", err)
			}
		}
	}
	return m.Up(ctx)
}

// sweepStagedAttachments hourly deletes uploads never attached or sent
// within 24h (abandoned new-task drafts and follow-ups).
func sweepStagedAttachments(ctx context.Context, store *db.Store, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if n, err := store.SweepStagedAttachments(ctx, 24*time.Hour); err != nil && ctx.Err() == nil {
			log.Warn("sweep staged attachments", "err", err)
		} else if n > 0 {
			log.Info("swept staged attachments", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// notificationRetention: inbox entries older than this are pruned hourly.
const notificationRetention = 30 * 24 * time.Hour

// pruneNotifications hourly deletes inbox entries past notificationRetention,
// calling signal when it deleted any.
func pruneNotifications(ctx context.Context, store *db.Store, log *slog.Logger, signal func()) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if n, err := store.PruneNotifications(ctx, notificationRetention); err != nil && ctx.Err() == nil {
			log.Warn("prune notifications", "err", err)
		} else if n > 0 {
			log.Info("pruned notifications", "count", n)
			signal()
		}
		if err := store.PruneInactivePushBindings(ctx); err != nil && ctx.Err() == nil {
			log.Warn("prune inactive push bindings", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
