package storetest_test

import (
	"testing"

	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/db/storetest"
)

// TestContract runs the contract suite on the test engine (SQLite; a fresh
// Postgres database under check:postgres, dbtest.Engine).
func TestContract(t *testing.T) {
	storetest.Run(t, dbtest.Engine)
}
