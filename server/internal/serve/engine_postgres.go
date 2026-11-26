//go:build postgres

package serve

import (
	"context"
	"log/slog"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db/postgres"
)

// openPostgres opens the Postgres engine; its sessions are named "stage
// <instanceId>" so a second owner can name this one.
func openPostgres(ctx context.Context, url config.DatabaseURL, instanceID string, logger *slog.Logger) (storageEngine, error) {
	return postgres.Open(ctx, url.Reveal(), postgres.Options{ApplicationName: "stage " + instanceID, Logger: logger})
}
