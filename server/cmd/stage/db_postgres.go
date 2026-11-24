//go:build postgres

package main

import (
	"context"
	"log/slog"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db/postgres"
	"github.com/flexdinesh/stage/server/internal/db/store"
)

// openCopyTarget opens the Postgres target and takes its ownership lock,
// so no server starts on it during the copy (held: *store.OwnedError, exit
// 75). Both are bounded by copyStartTimeout.
func openCopyTarget(ctx context.Context, url config.DatabaseURL) (store.CopyTarget, func(), error) {
	ctx, cancel := context.WithTimeout(ctx, copyStartTimeout)
	defer cancel()
	d, err := postgres.Open(ctx, url.Reveal(), postgres.Options{ApplicationName: "stage db copy", Logger: slog.Default()})
	if err != nil {
		return nil, nil, err
	}
	lease, err := d.Ownership().Acquire(ctx)
	if err != nil {
		d.Close()
		return nil, nil, err
	}
	return d, func() {
		_ = lease.Release()
		d.Close()
	}, nil
}
