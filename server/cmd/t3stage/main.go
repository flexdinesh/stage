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
	"github.com/flexdinesh/t3-stage/server/internal/chains"
	"github.com/flexdinesh/t3-stage/server/internal/config"
	"github.com/flexdinesh/t3-stage/server/internal/db"
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
	defer wg.Wait() // background loops exit once stop() cancels ctx
	defer stop()

	// PHASE3: swap point. Use the real MCP client unless T3_FAKE=1:
	//
	//	if os.Getenv("T3_FAKE") != "1" {
	//		mc := t3.NewMCPClient(t3.Options{
	//			BaseURL: cfg.T3URL, AdminToken: cfg.T3AdminToken, PublicURL: cfg.T3PublicURL,
	//			Store: store, HTTPClient: &http.Client{Timeout: 60 * time.Second}, Logger: logger,
	//		})
	//		wg.Add(1)
	//		go func() { defer wg.Done(); mc.Run(ctx) }()
	//		t3Client = mc
	//	}
	var t3Client t3.Client = t3.NewFake()
	logger.Info("using fake T3 client")

	syncSvc := t3sync.New(store, t3Client, logger)
	runner := chains.NewRunner(store, syncSvc, t3Client, logger)
	syncSvc.OnTransition = runner.OnTransition
	wg.Add(2)
	go func() { defer wg.Done(); syncSvc.Run(ctx) }()
	go func() { defer wg.Done(); runner.Run(ctx) }()

	mux := http.NewServeMux()
	mux.Handle("/api/", api.NewRouter(api.Deps{
		Config: cfg, Store: store, T3: t3Client, Sync: syncSvc, Chains: runner, Logger: logger,
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
