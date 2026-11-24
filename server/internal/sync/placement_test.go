package sync_test

import (
	"log/slog"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/sqlite"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/remote/remotetest"
	coordinator "github.com/flexdinesh/stage/server/internal/sync"
)

// placement is where the contract suite's harness runs: in the main
// server's process, or on a remote machine behind a real worker channel
// (ADR 0002). The coordinator must not tell them apart.
type placement struct {
	name   string
	remote bool
}

// remoteMachine is the remote placement's machine id.
const remoteMachine = 2

var placements = []placement{{name: "local"}, {name: "remote", remote: true}}

// forPlacements runs fn once per placement.
func forPlacements(t *testing.T, fn func(t *testing.T, p placement)) {
	for _, p := range placements {
		t.Run(p.name, func(t *testing.T) { fn(t, p) })
	}
}

// machine is the machine the placement's connections belong to (it adds
// the remote machine's row).
func (p placement) machine(t *testing.T, pool *sqlite.DB) int64 {
	t.Helper()
	if !p.remote {
		return db.LocalMachine
	}
	if _, err := pool.ExecContext(t.Context(), "INSERT OR IGNORE INTO machines(id,label) VALUES($1,'remote')", remoteMachine); err != nil {
		t.Fatal(err)
	}
	return remoteMachine
}

// coordinator drives adapters: hosted in process, or proxied to a worker
// that serves them (a new main server and worker per call).
func (p placement) coordinator(t *testing.T, store *db.Store, adapters map[int64]execution.Adapter, log *slog.Logger) *coordinator.Service {
	t.Helper()
	if !p.remote {
		return coordinator.NewWithAdapters(store, adapters, log)
	}
	rig := remotetest.New(t, remoteMachine, adapters)
	return coordinator.NewForMachines(store, rig.Set(), log)
}
