package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

// FieldKind is how a setup field's value is entered and shown.
type FieldKind string

const (
	FieldText   FieldKind = "text"
	FieldURL    FieldKind = "url"
	FieldSecret FieldKind = "secret" // never echoed back; status reports only whether it is set
	FieldBool   FieldKind = "bool"
)

// Field is one setup setting or action input. Settings are sent as one JSON
// object keyed by Field.ID (PUT /api/harnesses/{kind}/settings); a null
// value resets a setting to its default.
type Field struct {
	ID    string    `json:"id"`
	Label string    `json:"label"`
	Kind  FieldKind `json:"kind"`
	Help  string    `json:"help,omitempty"`
}

// Action is a named setup operation (POST
// /api/harnesses/{kind}/actions/{id}) with its JSON object input.
type Action struct {
	ID    string  `json:"id"`
	Label string  `json:"label"`
	Input []Field `json:"input,omitempty"`
}

// Setup is the schema of a harness's connection setup on a machine. Hosts
// render it generically unless they register a panel for the kind.
type Setup struct {
	Fields  []Field  `json:"fields"`
	Actions []Action `json:"actions"`
	// Disconnect: DELETE /api/harnesses/{kind}/connection is supported.
	Disconnect bool `json:"disconnect"`
}

// Action is the setup's action id.
func (s Setup) Action(id string) (Action, bool) {
	for _, a := range s.Actions {
		if a.ID == id {
			return a, true
		}
	}
	return Action{}, false
}

// Summary is a connector's last known state, from memory only (health,
// connection lists). It never probes the harness.
type Summary struct {
	// ConnectionID is the kind's active connection on this machine (0 when
	// none is planned yet).
	ConnectionID int64 `json:"connectionId"`
	// Configured: setup has been done (settings saved, paired or set by the
	// environment), so the connection is expected to work.
	Configured bool `json:"configured"`
	// Connected: the last check reached the harness with valid credentials.
	Connected bool   `json:"connected"`
	LastError string `json:"lastError,omitempty"`
}

// FieldState is a setting's current value. A secret's Value is always "".
type FieldState struct {
	ID    string `json:"id"`
	Value string `json:"value"`
	Set   bool   `json:"set"`
	// Source is saved or default.
	Source string `json:"source"`
}

// ActionState is whether an action can run now.
type ActionState struct {
	ID        string `json:"id"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// Status is a connector's full setup state (GET
// /api/harnesses/{kind}/status).
type Status struct {
	Summary
	// State is a harness-defined word (connected, unpaired, unavailable...).
	State   string        `json:"state"`
	Reason  string        `json:"reason,omitempty"`
	Fields  []FieldState  `json:"fields"`
	Actions []ActionState `json:"actions"`
	Sync    *SyncStatus   `json:"sync,omitempty"`
	// Details is the harness's own setup state for the host's panel for
	// this kind. Generic code never reads it.
	Details any `json:"details,omitempty"`
}

// SyncStatus is Stage's full-sync progress and persisted connection status.
type SyncStatus struct {
	LastError      *string    `json:"lastError"`
	LastFullSyncAt *time.Time `json:"lastFullSyncAt"`
	Running        bool       `json:"running"`
	StartedAt      *time.Time `json:"startedAt"`
	Projects       int        `json:"projects"`
	Sessions       int        `json:"sessions"`
}

// Result is an action's or setting change's outcome.
type Result struct {
	// Replaced is the connection the change retired (0: none).
	Replaced int64 `json:"replaced"`
}

// Connector is one harness kind's connection setup on one machine. Each
// connected harness package implements it over narrow ports; the server
// composes one per kind. Errors are the store's typed errors (invalid,
// conflict, harness).
type Connector interface {
	Summary(ctx context.Context) Summary
	// Status reports the setup state; refresh may probe the harness
	// (bounded), otherwise it is memory only.
	Status(ctx context.Context, refresh bool) (Status, error)
	// Configure applies a settings object (Setup.Fields keys).
	Configure(ctx context.Context, settings json.RawMessage) (Result, error)
	// Run runs a Setup.Actions action with its input object.
	Run(ctx context.Context, action string, input json.RawMessage) (Result, error)
	Disconnect(ctx context.Context) error
}

// ErrUnknownAction is returned by Run for an action the setup lacks.
var ErrUnknownAction = errors.New("unknown setup action")

// UnknownAction wraps ErrUnknownAction with the action and harness.
func UnknownAction(d Descriptor, action string) error {
	return fmt.Errorf("%w %q for %s", ErrUnknownAction, action, d.Name)
}

// Connectors is the server's connector per machine and kind, checked
// against the registry: every connector names a registered kind, connected
// or hosted. A machine has at most one connector per kind, serving that
// kind's active connection on the machine. Whether a machine may host a
// kind is the machine's decision (machine.Machine.Allows), applied before
// connectors are indexed here.
type Connectors struct {
	reg *Registry
	m   map[int64]map[string]Connector
}

// NewConnectors validates m (machine id to kind to connector) against reg.
func NewConnectors(reg *Registry, m map[int64]map[string]Connector) (Connectors, error) {
	for machine, kinds := range m {
		if machine <= 0 {
			return Connectors{}, fmt.Errorf("connectors for invalid machine %d", machine)
		}
		for kind := range kinds {
			if _, ok := reg.Lookup(kind); !ok {
				return Connectors{}, fmt.Errorf("connector for unregistered harness %q", kind)
			}
		}
	}
	return Connectors{reg: reg, m: m}, nil
}

// Get is machine's connector for kind.
func (c Connectors) Get(machine int64, kind string) (Connector, bool) {
	k, ok := c.m[machine][kind]
	return k, ok
}

// Each calls fn for each connector: machines in id order, kinds in
// registry order.
func (c Connectors) Each(fn func(machine int64, d Descriptor, k Connector)) {
	if c.reg == nil {
		return
	}
	machines := make([]int64, 0, len(c.m))
	for machine := range c.m {
		machines = append(machines, machine)
	}
	slices.Sort(machines)
	for _, machine := range machines {
		for _, d := range c.reg.All() {
			if k, ok := c.m[machine][d.Kind]; ok {
				fn(machine, d, k)
			}
		}
	}
}

// Owner is the connector whose active connection is id, on any machine.
func (c Connectors) Owner(ctx context.Context, id int64) (Descriptor, Connector, bool) {
	var od Descriptor
	var oc Connector
	c.Each(func(_ int64, d Descriptor, k Connector) {
		if oc == nil && id != 0 && k.Summary(ctx).ConnectionID == id {
			od, oc = d, k
		}
	})
	return od, oc, oc != nil
}

func (s Setup) validate() error {
	ids := map[string]bool{}
	for _, f := range s.Fields {
		if err := f.validate(); err != nil {
			return err
		}
		if ids[f.ID] {
			return fmt.Errorf("setup field %q declared twice", f.ID)
		}
		ids[f.ID] = true
	}
	acts := map[string]bool{}
	for _, a := range s.Actions {
		if a.ID == "" || a.Label == "" || acts[a.ID] {
			return fmt.Errorf("setup action %q needs a unique id and a label", a.ID)
		}
		acts[a.ID] = true
		in := map[string]bool{}
		for _, f := range a.Input {
			if err := f.validate(); err != nil {
				return err
			}
			if in[f.ID] {
				return fmt.Errorf("action %q input %q declared twice", a.ID, f.ID)
			}
			in[f.ID] = true
		}
	}
	return nil
}

func (f Field) validate() error {
	switch {
	case f.ID == "" || f.Label == "":
		return fmt.Errorf("setup field %q needs an id and a label", f.ID)
	case f.Kind != FieldText && f.Kind != FieldURL && f.Kind != FieldSecret && f.Kind != FieldBool:
		return fmt.Errorf("setup field %q: unknown kind %q", f.ID, f.Kind)
	}
	return nil
}
