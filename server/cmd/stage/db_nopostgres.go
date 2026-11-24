//go:build !postgres

package main

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db/store"
)

// openCopyTarget refuses: this binary has no Postgres adapter
// (config.ParseDatabase already refuses the URL; this backs it up).
func openCopyTarget(context.Context, config.DatabaseURL) (store.CopyTarget, func(), error) {
	return nil, nil, config.Invalid(errNoPostgresCopy)
}
