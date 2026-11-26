//go:build !postgres

package serve

import (
	"context"
	"log/slog"

	"github.com/flexdinesh/stage/server/internal/config"
)

// openPostgres refuses: this binary has no Postgres adapter (config.Load
// already refuses the URL; this backs up a Config built another way).
func openPostgres(context.Context, config.DatabaseURL, string, *slog.Logger) (storageEngine, error) {
	return nil, config.Invalid(config.ErrNoPostgres)
}
