package machine

import (
	"context"
	"fmt"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// StorePorts are the store-backed setup ports of one machine: settings,
// credentials and connection identity, for setup code running in the main
// server's process (the local machine). A remote machine serves the same
// ports over its worker channel and keeps credentials in a worker file.
type StorePorts struct {
	db.MachineStore
	// Harnesses resolves a kind's descriptor: a port call names its harness
	// by kind, and the main server applies its own registry's policy.
	Harnesses *harness.Registry
}

// NewStorePorts binds store's machine view to reg.
func NewStorePorts(store db.MachineStore, reg *harness.Registry) StorePorts {
	return StorePorts{MachineStore: store, Harnesses: reg}
}

// Active is the machine's active connection of kind (0: none).
func (p StorePorts) Active(_ context.Context, kind string) (int64, error) {
	return p.ActiveID(kind), nil
}

// SwitchInstance switches the machine's active connection of kind to the
// instance identity (db.Store.SwitchInstance).
func (p StorePorts) SwitchInstance(ctx context.Context, kind, identity, label string, revive int64) (db.Switch, error) {
	if p.Harnesses == nil {
		return db.Switch{}, fmt.Errorf("switch %s instance: no harness registry", kind)
	}
	d, ok := p.Harnesses.Lookup(kind)
	if !ok {
		return db.Switch{}, fmt.Errorf("switch instance: unregistered harness %q", kind)
	}
	return p.MachineStore.SwitchInstance(ctx, d, identity, label, revive)
}
