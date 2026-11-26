package sync

import (
	"log/slog"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
)

// NewWithAdapters hosts adapters in process, each on its connection's
// machine (tests and fixed compositions).
func NewWithAdapters(store *db.Store, reg *harness.Registry, adapters map[int64]execution.Adapter, log *slog.Logger) *Service {
	profile := harness.Profile{Kind: harness.ProfileServer, LocalProcesses: true}
	local := machine.NewLocal(db.LocalMachine, profile, nil)
	locals := map[int64]*machine.Local{local.ID(): local}
	var others []machine.Machine
	for id, a := range adapters {
		m := store.ConnectionMachine(id)
		if m == 0 {
			m = local.ID()
		}
		if locals[m] == nil {
			locals[m] = machine.NewLocal(m, profile, nil)
			others = append(others, locals[m])
		}
		locals[m].Install(id, id, a)
	}
	s := NewForMachines(store, reg, machine.MustSet(local, others...), log)
	for _, l := range locals {
		l.OnChange(s.ConnectionsChanged)
	}
	return s
}
