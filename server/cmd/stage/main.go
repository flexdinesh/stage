package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata" // IANA zones for /api/stats tz even without system zoneinfo

	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/cli"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/links"
	"github.com/flexdinesh/stage/server/internal/notify"
	"github.com/flexdinesh/stage/server/internal/schedule"
	t3sync "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/transcript"
	"github.com/flexdinesh/stage/server/internal/webui"
)

func main() {
	// No args (or `serve`): the server. Anything else: the agent CLI.
	if len(os.Args) > 1 && os.Args[1] != "serve" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		code := cli.Run(ctx, os.Args[1:], cli.OSEnv())
		stop()
		os.Exit(code)
	}
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return fmt.Errorf("data dir: %w", err)
	}
	dbPath := filepath.Join(cfg.DataDir, db.DBFile)
	pool, err := db.Open(ctx, dbPath)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	logger := slog.Default()
	logger.Info("database", "path", dbPath)
	store := db.NewStore(pool)
	var wg sync.WaitGroup
	var closeT3 func()
	defer func() {
		wg.Wait() // background loops exit once stop() cancels ctx
		if closeT3 != nil {
			closeT3()
		}
	}()
	defer stop()

	// T3_FAKE=1 forces the in-memory fake (dev/demo without a T3 server).
	var t3Client t3.Client
	if os.Getenv("T3_FAKE") == "1" {
		fake := t3.NewFake()
		// T3_FAKE_SEED=1 seeds demo threads waiting on questions (UI work).
		if os.Getenv("T3_FAKE_SEED") == "1" {
			fake.SeedDemo()
		}
		t3Client = fake
		logger.Info("using fake T3 client")
	} else {
		mc := t3.NewMCPClient(t3.Options{
			BaseURL: cfg.T3URL, AdminToken: cfg.T3AdminToken, PublicURL: cfg.T3PublicURL, ClientName: cfg.T3ClientName,
			Store: store, HTTPClient: &http.Client{Timeout: 60 * time.Second}, Logger: logger,
		})
		closeT3 = mc.Close
		wg.Add(1)
		go func() { defer wg.Done(); mc.Run(ctx) }()
		t3Client = mc
		logger.Info("using T3 MCP client", "url", cfg.T3URL)
	}

	syncSvc := t3sync.New(store, t3Client, logger)
	syncSvc.SetPublicURL(cfg.T3PublicURL)
	runner := links.NewRunner(store, syncSvc, logger)
	scheduler := schedule.NewRunner(store, syncSvc, logger)
	notifier := notify.New(store, cfg.PublicURL, logger)
	// Hooks only queue work; set before RecoverLaunching emits transitions.
	syncSvc.OnTransition = func(ctx context.Context, c t3sync.Change) {
		runner.OnTransition(ctx, c)
		notifier.OnTransition(ctx, c)
	}
	syncSvc.OnThreadChange = func(ctx context.Context, c t3sync.ThreadChange) {
		runner.OnThreadChange(ctx, c)
		notifier.OnThreadChange(ctx, c)
	}
	// Before the poller, link runner, scheduler and HTTP server can touch launching tasks.
	syncSvc.RecoverLaunching(ctx)
	wg.Add(6)
	go func() { defer wg.Done(); notifier.Run(ctx) }()
	go func() { defer wg.Done(); syncSvc.Run(ctx) }()
	go func() { defer wg.Done(); runner.Run(ctx) }()
	go func() { defer wg.Done(); scheduler.Run(ctx) }()
	go func() { defer wg.Done(); sweepStagedAttachments(ctx, store, logger) }()
	go func() { defer wg.Done(); pruneNotifications(ctx, store, logger) }()

	mux := http.NewServeMux()
	// Transcript pollers and their event streams end with ctx, so Shutdown
	// does not wait on open streams.
	transcripts := transcript.NewHub(ctx, t3Client, api.MirrorActive(store), logger)
	mux.Handle("/api/", api.NewRouter(api.Deps{
		Config: cfg, Store: store, T3: t3Client, Sync: syncSvc, Push: notifier, Logger: logger, Transcript: transcripts,
	}))
	// Streamable HTTP MCP: the agent tools over this server's own REST API
	// (loopback), so validation stays in one place. No local file access.
	mux.Handle("/mcp", agenttools.HTTPHandler(&agenttools.Ops{
		Client: client.New("http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Port))),
	}))
	mux.Handle("/", webui.Handler())

	srv := &http.Server{Addr: cfg.Addr(), Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.Addr())
		errCh <- srv.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	// Let in-flight launches (bounded at 90s) record their thread: a kill
	// mid-launch leaves the task failed with the outcome unknown. compose
	// stop_grace_period must exceed this.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
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

// pruneNotifications hourly deletes inbox entries past notificationRetention.
func pruneNotifications(ctx context.Context, store *db.Store, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if n, err := store.PruneNotifications(ctx, notificationRetention); err != nil && ctx.Err() == nil {
			log.Warn("prune notifications", "err", err)
		} else if n > 0 {
			log.Info("pruned notifications", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
