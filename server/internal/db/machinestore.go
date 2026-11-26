package db

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/harness"
)

// MachineStore binds connection lookups and instance switches to one machine.
type MachineStore struct {
	*Store
	machine int64
}

// OnMachine is the store bound to machine.
func (s *Store) OnMachine(machine int64) MachineStore {
	return MachineStore{Store: s, machine: machine}
}

// Machine is the bound machine.
func (m MachineStore) Machine() int64 { return m.machine }

// ActiveID is the machine's active connection of harness (0 when none).
func (m MachineStore) ActiveID(harness string) int64 { return m.Store.ActiveID(m.machine, harness) }

// SwitchInstance switches the machine's active connection of d.Kind.
func (m MachineStore) SwitchInstance(ctx context.Context, d harness.Descriptor, identity, label string, revive int64) (Switch, error) {
	return m.Store.SwitchInstance(ctx, m.machine, d, identity, label, revive)
}

// RetiredConnections lists the machine's retired connections of harness.
func (m MachineStore) RetiredConnections(ctx context.Context, harness string) ([]int64, error) {
	return m.Store.RetiredConnections(ctx, m.machine, harness)
}
