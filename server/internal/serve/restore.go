package serve

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/sqlite"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/instance"
)

// InvalidateRestoredAccess takes the same ownership as the runtime and
// upgrades an existing restored database before clearing copied credentials.
// It never serves requests or starts work.
func InvalidateRestoredAccess(ctx context.Context, cfg config.Config) error {
	st, err := os.Stat(cfg.DataDir)
	if err != nil || !st.IsDir() {
		return config.Invalid(errors.New("Set STAGE_DATA_DIR to an existing restored data directory."))
	}
	owner, err := instance.Lock(cfg.DataDir)
	if err != nil {
		return err
	}
	defer owner.Release()
	cfg.DataDir = owner.DataDir

	isPostgres := cfg.Database.Engine() == config.EnginePostgres
	if !isPostgres {
		// SQLite's regular open creates a missing file and follows symlinks.
		// Require the restored file to be in the directory we locked.
		st, err := os.Lstat(filepath.Join(cfg.DataDir, sqlite.DBFile))
		if err != nil || !st.Mode().IsRegular() {
			return config.Invalid(errors.New("Restore stage.db into STAGE_DATA_DIR before clearing restored access."))
		}
	}
	logger := slog.Default()
	eng, err := openEngine(ctx, cfg, "restore", logger)
	if err != nil {
		if isPostgres {
			return errors.New("Could not open the restored Postgres database. Check STAGE_DATABASE_URL and database access.")
		}
		return err
	}
	defer eng.Close()

	lease, err := acquireOwnership(ctx, cfg, eng)
	if err != nil {
		var owned *store.OwnedError
		if errors.As(err, &owned) {
			return &store.OwnedError{}
		}
		return errors.New("Could not take ownership of the restored database. Check database access.")
	}
	defer lease.Release()
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	if lost := lease.Lost(); lost != nil {
		go func() {
			select {
			case <-lost:
				cancel(store.ErrOwnershipLost)
			case <-ctx.Done():
			}
		}()
	}

	current, err := eng.Migrator().Current(ctx)
	if err != nil {
		return errors.New("Could not read the restored database schema.")
	}
	if current == 0 {
		return config.Invalid(errors.New("Restore an existing Stage database before clearing restored access."))
	}
	if err := migrate(ctx, eng, logger); err != nil {
		return err
	}
	if err := db.NewStore(eng).InvalidateRestoredAccess(ctx); err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		return errors.New("Could not clear restored sessions and account links.")
	}
	return nil
}
