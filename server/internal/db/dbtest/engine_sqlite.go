//go:build !postgres

package dbtest

import (
	"testing"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

func postgresEngine(testing.TB) (store.Engine, bool) { return nil, false }
