// Package engine composes Stage's execution services.
package engine

import (
	"context"
	"log/slog"

	"github.com/flexdinesh/stage/server/internal/coordinator"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
)

type Engine struct {
	Connections *machine.Connections
	Coordinator *coordinator.Service
}

type Options struct {
	Store     *db.Store
	Machines  *machine.Set
	Harnesses *harness.Registry
	Log       *slog.Logger
}

func New(o Options) *Engine {
	conns := machine.NewConnections(o.Store, o.Machines, o.Harnesses)
	return &Engine{Connections: conns, Coordinator: coordinator.New(o.Store, conns, o.Log)}
}

func NewForMachines(store *db.Store, reg *harness.Registry, set *machine.Set, log *slog.Logger) *Engine {
	return New(Options{Store: store, Machines: set, Harnesses: reg, Log: log})
}

// NewWithAdapters hosts adapters in process, each on its connection's
// machine (tests and fixed compositions).
func NewWithAdapters(store *db.Store, reg *harness.Registry, adapters map[int64]execution.Adapter, log *slog.Logger) *Engine {
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

func (e *Engine) Run(ctx context.Context) { e.Coordinator.Run(ctx) }

func (e *Engine) Kick() { e.Coordinator.Kick() }

func (e *Engine) ConnectionsChanged(old, next int64) {
	e.Coordinator.ConnectionsChanged(old, next)
}
