package machine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
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

var _ harness.Identity = StorePorts{}

// NewStorePorts binds store's machine view to reg.
func NewStorePorts(store db.MachineStore, reg *harness.Registry) StorePorts {
	return StorePorts{MachineStore: store, Harnesses: reg}
}

// Active is the machine's active connection of kind (0: none).
func (p StorePorts) Active(_ context.Context, kind string) (int64, error) {
	return p.ActiveID(kind), nil
}

func (p StorePorts) ActiveIdentity(ctx context.Context, kind string) (int64, string, error) {
	return p.Store.ActiveConnectionIdentity(ctx, p.Machine(), kind)
}

func (p StorePorts) ConnectionExists(ctx context.Context, id int64) (bool, error) {
	c, err := p.Store.GetHarnessConnection(ctx, id)
	if errors.Is(err, db.ErrNotFound) {
		return false, nil
	}
	return c.MachineID == p.Machine(), err
}

func (p StorePorts) SetConnectionIdentity(ctx context.Context, id int64, identity, label string) error {
	if err := p.requireConnection(ctx, id); err != nil {
		return err
	}
	return p.Store.SetConnectionIdentity(ctx, id, identity, label)
}

func (p StorePorts) ConnectionEvidence(ctx context.Context, id int64) ([]harness.WorkspaceEvidence, []execution.Observation, bool, error) {
	if err := p.requireConnection(ctx, id); err != nil {
		return nil, nil, false, err
	}
	bindings, observations, unknown, err := p.Store.ConnectionEvidence(ctx, id)
	if err != nil {
		return nil, nil, false, err
	}
	evidence := make([]harness.WorkspaceEvidence, 0, len(bindings))
	for _, b := range bindings {
		evidence = append(evidence, harness.WorkspaceEvidence{ExternalProjectID: b.ExternalProjectID, Workspace: b.Workspace})
	}
	return evidence, observations, unknown, nil
}

func (p StorePorts) requireConnection(ctx context.Context, id int64) error {
	found, err := p.ConnectionExists(ctx, id)
	if err == nil && !found {
		return db.ErrNotFound
	}
	return err
}

func (p StorePorts) Settings() harness.Settings {
	return storeSettings{p.MachineStore}
}

func (p StorePorts) Secrets() harness.Secrets {
	return storeSecrets{p.MachineStore}
}

func (p StorePorts) SaveSettingsWithSecret(ctx context.Context, kind string, patch json.RawMessage, key string, value json.RawMessage) error {
	return p.Store.SaveHarnessSettingsAndSecret(ctx, p.Machine(), kind, patch, key, value)
}

func (p StorePorts) SetLastError(ctx context.Context, kind, msg string) error {
	return p.Store.SetHarnessLastError(ctx, p.Machine(), kind, msg)
}

type storeSettings struct{ db.MachineStore }

func (p storeSettings) Get(ctx context.Context, kind string) (json.RawMessage, error) {
	return p.HarnessSettings(ctx, p.Machine(), kind)
}

func (p storeSettings) Save(ctx context.Context, kind string, patch json.RawMessage) error {
	return p.SaveHarnessSettings(ctx, p.Machine(), kind, patch)
}

type storeSecrets struct{ db.MachineStore }

func (p storeSecrets) Get(ctx context.Context, kind, key string) (json.RawMessage, error) {
	return p.HarnessSecret(ctx, p.Machine(), kind, key)
}

func (p storeSecrets) Set(ctx context.Context, kind, key string, value json.RawMessage) error {
	return p.SetHarnessSecret(ctx, p.Machine(), kind, key, value)
}

func (p storeSecrets) Delete(ctx context.Context, kind, key string) error {
	return p.DeleteHarnessSecret(ctx, p.Machine(), kind, key)
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
