package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/flexdinesh/t3-stage/server/internal/api"
	"github.com/flexdinesh/t3-stage/server/internal/config"
	"github.com/flexdinesh/t3-stage/server/internal/db"
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
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
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

	// WIRING (integration): replace the fake with the real MCP client from
	// internal/t3 (needs cfg + a db-backed t3.TokenStore).
	var t3Client t3.Client = t3.NewFake()

	// WIRING (integration): start background loops here (active poller 5s,
	// full sync 5m, catalog refresh 10m, chain runner), all bound to ctx.

	mux := http.NewServeMux()
	mux.Handle("/api/", api.NewRouter(api.Deps{Config: cfg, Pool: pool, T3: t3Client}))
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
