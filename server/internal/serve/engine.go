package serve

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db/sqlite"
	"github.com/flexdinesh/stage/server/internal/db/store"
)

// postgresStartTimeout bounds opening Postgres and taking its ownership
// lock at startup: an unreachable database exits 1 (the restart policy
// retries) rather than leaving the server unready (docs/runtime.md).
const postgresStartTimeout = 30 * time.Second

// engine is the composed storage engine.
type engine interface {
	store.Engine
	Close() error
}

// openEngine opens the engine cfg.Database selects (docs/storage.md
// "Engines and selection"): SQLite at <DataDir>/stage.db, or Postgres at
// the URL. It logs the engine, never the URL.
func openEngine(ctx context.Context, cfg config.Config, instanceID string, logger *slog.Logger) (engine, error) {
	if cfg.Database.Engine() == config.EnginePostgres {
		ctx, cancel := context.WithTimeout(ctx, postgresStartTimeout)
		defer cancel()
		eng, err := openPostgres(ctx, cfg.Database.URL, instanceID, logger)
		if err != nil {
			return nil, err
		}
		logger.Info("database", "engine", config.EnginePostgres)
		return eng, nil
	}
	path := filepath.Join(cfg.DataDir, sqlite.DBFile)
	eng, err := sqlite.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	logger.Info("database", "engine", config.EngineSQLite, "path", path)
	return eng, nil
}

// acquireOwnership takes the engine's ownership lease, bounded like
// openEngine on Postgres.
func acquireOwnership(ctx context.Context, cfg config.Config, eng engine) (store.Lease, error) {
	if cfg.Database.Engine() == config.EnginePostgres {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, postgresStartTimeout)
		defer cancel()
	}
	return eng.Ownership().Acquire(ctx)
}
