package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

type FieldKind = execution.FieldKind

const (
	FieldText   = execution.FieldText
	FieldURL    = execution.FieldURL
	FieldSecret = execution.FieldSecret
	FieldBool   = execution.FieldBool
)

// Field is one setup setting or action input. Settings are sent as one JSON
// object keyed by Field.ID (PUT /api/harnesses/{kind}/settings); a null
// value resets a setting to its default.
type Field = execution.Field

// Action is the shared declaration for a connection setup operation.
type Action = execution.Action

// Setup is the schema of a harness's connection setup on a machine. Hosts
// render it generically unless they register a panel for the kind.
type Setup struct {
	Fields  []Field            `json:"fields"`
	Actions []execution.Action `json:"actions"`
	// Disconnect: DELETE /api/harnesses/{kind}/connection is supported.
	Disconnect bool `json:"disconnect"`
}

// Action is the setup's action id.
func (s Setup) Action(id string) (execution.Action, bool) {
	for _, a := range s.Actions {
		if a.ID == id {
			return a, true
		}
	}
	return execution.Action{}, false
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
	Code      string `json:"code,omitempty"`
}

// Phase describes what the connection needs from the user.
type Phase string

const (
	PhaseDisconnected Phase = "disconnected"
	PhaseNeedsSetup   Phase = "needs_setup"
	PhaseConnecting   Phase = "connecting"
	PhaseConnected    Phase = "connected"
	PhaseDegraded     Phase = "degraded"
	PhaseUnavailable  Phase = "unavailable"
)

// Fix is the guidance for a setup problem, with an optional command to copy.
type Fix struct {
	Text    string `json:"text"`
	Command string `json:"command,omitempty"`
}

// Status is a connector's full setup state (GET
// /api/harnesses/{kind}/status).
type Status struct {
	Summary
	// State is a harness-defined word (connected, unpaired, unavailable...).
	State   string           `json:"state"`
	Reason  string           `json:"reason,omitempty"`
	Phase   Phase            `json:"phase"`
	Facts   []execution.Fact `json:"facts,omitempty"`
	Fix     *Fix             `json:"fix,omitempty"`
	Fields  []FieldState     `json:"fields"`
	Actions []ActionState    `json:"actions"`
	Sync    *SyncStatus      `json:"sync,omitempty"`
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
type Result = execution.ActionResult

// CheckResult reports the result of a connector's status probe.
func (s Status) CheckResult() Result {
	message := s.Reason
	if message == "" {
		message = "Connection check completed."
	}
	return Result{Message: message, Facts: s.Facts}
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
	if err := execution.ValidateFields(s.Fields); err != nil {
		return err
	}
	return execution.ValidateActions(s.Actions, execution.TargetConnection)
}
