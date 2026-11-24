package storetest_test

import (
	"testing"

	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/db/storetest"
)

// TestContract runs the contract suite on the test engine (SQLite; the
// check:postgres database under -tags postgres once #403 wires it in dbtest).
func TestContract(t *testing.T) {
	storetest.Run(t, dbtest.Engine)
}
