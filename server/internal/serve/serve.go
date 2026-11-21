// Package serve runs the Stage server process: it owns the data dir, opens
// every configured listener, and serves one set of handlers, store and
// background runtime on all of them (docs/runtime.md).
package serve

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/guard"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/install"
	"github.com/flexdinesh/stage/server/internal/instance"
	"github.com/flexdinesh/stage/server/internal/listen"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/notify"
	"github.com/flexdinesh/stage/server/internal/opencode"
	"github.com/flexdinesh/stage/server/internal/release"
	"github.com/flexdinesh/stage/server/internal/schedule"
	t3sync "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/t3setup"
	"github.com/flexdinesh/stage/server/internal/webui"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// ShutdownTimeout lets in-flight launches (bounded at 90s) record their
// thread: a kill mid-launch leaves the task failed with the outcome unknown.
// Service managers' stop timeouts (compose stop_grace_period) must exceed it.
const ShutdownTimeout = 100 * time.Second

// Options configure Run.
type Options struct {
	Config config.Config
	// T3 is the T3 client; nil uses the in-memory fake with Config.T3Fake
	// (T3_FAKE=1), else the real MCP client from Config.
	T3     t3.Client
	Logger *slog.Logger
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
	Meta      instance.Meta
}

// Run serves until ctx ends, then shuts down gracefully. Order:
//  1. lock the data dir (a second server on it, on any address, exits here)
//  2. open every listener (a failed bind closes the others; the database
//     is not touched yet)
//  3. open the database, refuse a schema newer than this binary (78), back
//     it up when migrations are pending, migrate, start T3, recover
//     launching tasks and start the background loops
//  4. serve all listeners, then notify READY=1 (systemd)
//
// Shutdown: STOPPING=1, background loops and notification streams end, the
// HTTP server drains for up to ShutdownTimeout, then T3 and the database
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
	policy, err := guard.New(cfg.AllowedOrigins, logger)
	if err != nil {
		return config.Invalid(err)
	}

	ctx, stop := context.WithCancel(ctx)
	defer stop()
	dbPath := filepath.Join(cfg.DataDir, db.DBFile)
	pool, err := db.Open(ctx, dbPath)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := migrate(ctx, pool, cfg.DataDir, logger); err != nil {
		return err
	}
	logger.Info("database", "path", dbPath)
	store := db.NewStore(pool)

	var wg sync.WaitGroup
	var local *localMachine
	defer func() {
		stop()
		wg.Wait() // background loops exit once ctx ends
		if local != nil && local.close != nil {
			local.close()
		}
	}()

	// Agent instructions name the CLI this identity installed on PATH.
	home, _ := os.UserHomeDir()
	cliName := install.CLIName(home, buildinfo.Identity)
	// The fixed harness composition: every kind this server knows, in order.
	harnesses := harness.MustRegistry(t3.Descriptor, opencode.Descriptor)
	profile := o.Profile
	if profile.Kind == "" {
		profile.Kind = harness.ProfileServer
	}
	// The machine-runtime seam: the in-process local machine is the only
	// machine until workers exist (ADR 0002).
	local, err = newLocalMachine(ctx, &wg, store, cfg, o.T3, profile, logger)
	if err != nil {
		return err
	}
	machines := machine.MustSet(local)
	syncSvc := t3sync.NewForMachines(store, machines, logger)
	syncSvc.Harnesses = harnesses
	syncSvc.PublicURL = cfg.PublicURL
	syncSvc.CLI = cliName
	notifier := notify.New(store, cfg.PublicURL, logger)
	// One release and one schedule runner per active connection, on every
	// machine; the sets follow instance switches.
	releases := newPerConnection(store, func(id int64) *release.Runner {
		r := release.NewRunner(store, syncSvc, logger)
		r.ConnectionID = id
		r.OnReady = notifier.OnReady
		return r
	})
	schedules := newPerConnection(store, func(id int64) *schedule.Runner {
		sc := schedule.NewRunner(store, syncSvc, logger)
		sc.ConnectionID = id
		return sc
	})
	kickTrees := func() {
		releases.Each(func(r *release.Runner) { r.Kick() })
	}
	// The coordinator and runners follow the machine's adapters.
	local.OnChange(func(old, next int64) {
		syncSvc.ConnectionsChanged(old, next)
		releases.Resync()
		schedules.Resync()
	})
	// One inbox hub wakes notification streams on inserts (notifier), reads
	// (API), prunes and task transitions (badge). Streams end with ctx.
	inbox := notify.NewHub(ctx)
	notifier.Inbox = inbox
	// Hooks only queue work; set before RecoverLaunching emits transitions.
	syncSvc.OnTransition = func(ctx context.Context, c t3sync.Change) {
		releases.Each(func(r *release.Runner) { r.OnTransition(ctx, c) })
		notifier.OnTransition(ctx, c)
		inbox.Signal()
	}
	syncSvc.OnThreadChange = notifier.OnThreadChange
	syncSvc.OnAccepted = kickTrees
	if err := local.compose(ctx, store, cfg, harnesses, syncSvc.Kick, func(connection int64) t3setup.SyncProgress {
		p := syncSvc.Progress(connection)
		return t3setup.SyncProgress{Running: p.Running, StartedAt: p.StartedAt, Projects: p.Projects, Threads: p.Threads}
	}, logger); err != nil {
		return err
	}
	connectors, err := machines.Connectors(harnesses)
	if err != nil {
		return err
	}
	// Before the poller, release runner, scheduler and HTTP server can touch launching tasks.
	syncSvc.RecoverLaunching(ctx)
	wg.Add(4)
	go func() { defer wg.Done(); notifier.Run(ctx) }()
	go func() { defer wg.Done(); syncSvc.Run(ctx) }()
	wg.Go(func() { releases.Run(ctx) })
	wg.Go(func() { schedules.Run(ctx) })
	go func() { defer wg.Done(); sweepStagedAttachments(ctx, store, logger) }()
	go func() { defer wg.Done(); pruneNotifications(ctx, store, logger, inbox.Signal) }()

	// Worker channels end with ctx, so Shutdown does not wait on their
	// streams (docs/runtime.md "Worker channel").
	channels := workerchan.NewHub(ctx, workerchan.Options{Version: buildinfo.Version, Seen: api.TouchMachineFunc(store, func(err error) {
		logger.Warn("machine presence", "err", err)
	})})

	mux := http.NewServeMux()
	// Transcript pollers and their event streams end with ctx, so Shutdown
	// does not wait on open streams.
	apiHandler := api.NewRouter(api.Deps{
		Config: cfg, Store: store, Sync: syncSvc, Push: notifier, Logger: logger,
		Inbox: inbox, Instance: meta, OnTreeChange: kickTrees, Setup: local.setup, Connectors: connectors,
		Harnesses: harnesses, Channels: channels,
	})
	// Browser-facing endpoints get the request-origin policy; the UI's
	// static files do not need it.
	mux.Handle("/api/", policy.Wrap(apiHandler))
	// Streamable HTTP MCP: the agent tools over this server's own REST API,
	// served in-process (no dial back to a listener, tcp or unix), so
	// validation stays in one place. No local file access.
	// Machine tokens are worker-route credentials only (api.RejectMachineTokens;
	// /api applies it itself).
	mux.Handle("/mcp", policy.Wrap(api.RejectMachineTokens(agenttools.HTTPHandler(&agenttools.Ops{
		Client: client.NewInProcess(apiHandler, addrs[0].URL()), CLI: cliName,
	}))))
	mux.Handle("/", webui.Handler())

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, len(lns))
	for _, l := range lns {
		go func() { errCh <- srv.Serve(l) }()
		logger.Info("listening", "addr", l.Spec.String())
	}
	if err := instance.Notify("READY=1"); err != nil {
		logger.Warn("readiness notification", "err", err)
	}
	if o.Ready != nil {
		o.Ready(Ready{DataDir: cfg.DataDir, Listeners: addrs, Meta: meta})
	}
	var serveErr error
	select {
	case serveErr = <-errCh:
	case <-ctx.Done():
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

// migrate guards and migrates the schema: a database newer than this binary
// is refused (exit 78); a non-empty one with pending migrations is first
// backed up to <dataDir>/backups (a failed backup refuses to migrate).
func migrate(ctx context.Context, pool *sql.DB, dataDir string, logger *slog.Logger) error {
	cur, err := db.CurrentVersion(ctx, pool)
	if err != nil {
		return err
	}
	known := db.MaxVersion()
	if cur > known {
		return config.Invalid(&db.SchemaTooNewError{Current: cur, Known: known})
	}
	if cur > 0 && cur < known {
		dir := filepath.Join(dataDir, db.BackupsDir)
		path, err := db.Backup(ctx, pool, dir, cur, time.Now())
		if err != nil {
			return fmt.Errorf("refusing to migrate schema %d to %d: %w", cur, known, err)
		}
		logger.Info("database backup", "path", path, "schema", cur, "migrating_to", known)
		if err := db.PruneBackups(dir, db.KeepBackups); err != nil {
			logger.Warn("prune database backups", "err", err)
		}
	}
	return db.Migrate(ctx, pool)
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
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
