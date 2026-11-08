package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata" // IANA zones for /api/stats tz even without system zoneinfo

	"github.com/flexdinesh/t3-stage/server/internal/api"
	"github.com/flexdinesh/t3-stage/server/internal/config"
	"github.com/flexdinesh/t3-stage/server/internal/db"
	"github.com/flexdinesh/t3-stage/server/internal/links"
	t3sync "github.com/flexdinesh/t3-stage/server/internal/sync"
	"github.com/flexdinesh/t3-stage/server/internal/t3"
	"github.com/flexdinesh/t3-stage/server/internal/webui"
)

func main() {
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
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return err
	}
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	logger := slog.Default()
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
		t3Client = t3.NewFake()
		logger.Info("using fake T3 client")
	} else {
		mc := t3.NewMCPClient(t3.Options{
			BaseURL: cfg.T3URL, AdminToken: cfg.T3AdminToken, PublicURL: cfg.T3PublicURL,
			Store: store, HTTPClient: &http.Client{Timeout: 60 * time.Second}, Logger: logger,
		})
		closeT3 = mc.Close
		wg.Add(1)
		go func() { defer wg.Done(); mc.Run(ctx) }()
		t3Client = mc
		logger.Info("using T3 MCP client", "url", cfg.T3URL)
	}

	syncSvc := t3sync.New(store, t3Client, logger)
	runner := links.NewRunner(store, syncSvc, logger)
	syncSvc.OnTransition = runner.OnTransition
	// Before the poller, link runner and HTTP server can touch launching tasks.
	syncSvc.RecoverLaunching(ctx)
	wg.Add(3)
	go func() { defer wg.Done(); syncSvc.Run(ctx) }()
	go func() { defer wg.Done(); runner.Run(ctx) }()
	go func() { defer wg.Done(); sweepStagedAttachments(ctx, store, logger) }()

	mux := http.NewServeMux()
	mux.Handle("/api/", api.NewRouter(api.Deps{
		Config: cfg, Store: store, T3: t3Client, Sync: syncSvc, Logger: logger,
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
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
