package harness

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Module composes one harness on the machine that runs it. Its settings
// schema is also used by the main server to validate worker setup calls.
type Module struct {
	Descriptor       Descriptor
	Compose          func(context.Context, Ports) (Composition, error)
	ValidateSettings func(json.RawMessage) error
	LegacySecrets    []LegacySecret
	Env              EnvOptions
	Owners           []string
}

type Composition struct {
	Connector Connector
	// Close releases Stage's client resources after the lifetime context
	// ends. The harness's own processes may outlive the client.
	Close func()
}

// EnvOptions declares process options and the removed arguments that still
// need a migration message in the service's environment file.
type EnvOptions struct {
	Keys, Removed  []string
	WorkerTemplate string
}

// LegacySecret identifies and validates one pre-module worker file entry.
type LegacySecret struct {
	Entry, Kind, Key string
	Validate         func(json.RawMessage) error
}

// Settings holds public, machine-scoped settings. Save atomically applies
// a top-level patch. A present null resets that field per the module schema.
type Settings interface {
	Get(ctx context.Context, kind string) (json.RawMessage, error)
	Save(ctx context.Context, kind string, patch json.RawMessage) error
}

// Secrets holds machine-local opaque values. Get returns nil when absent.
type Secrets interface {
	Get(ctx context.Context, kind, key string) (json.RawMessage, error)
	Set(ctx context.Context, kind, key string, value json.RawMessage) error
	Delete(ctx context.Context, kind, key string) error
}

// Identity is the machine's connection record and the evidence a module can
// use to prove continuity. It cannot inspect another machine's connections.
type Identity interface {
	Active(context.Context, string) (int64, error)
	ActiveIdentity(context.Context, string) (int64, string, error)
	SwitchInstance(ctx context.Context, kind, identity, label string, revive int64) (Switch, error)
	ConnectionExists(context.Context, int64) (bool, error)
	SetConnectionIdentity(ctx context.Context, id int64, identity, label string) error
	RetiredConnections(context.Context, string) ([]int64, error)
	ConnectionEvidence(context.Context, int64) ([]WorkspaceEvidence, []execution.Observation, bool, error)
}

// Switch is the outcome of making a harness instance active.
type Switch struct {
	Old, New int64
	Changed  bool
	Revived  bool
}

// WorkspaceEvidence is the binding a candidate instance needs to prove.
type WorkspaceEvidence struct {
	ExternalProjectID string `json:"externalProjectId"`
	Workspace         string `json:"workspace"`
}

// Ports are bound to the machine before Compose runs. Getenv and LookupPath
// refer to that process. Overrides supply machine-local test dependencies.
type Ports struct {
	Settings Settings
	Secrets  Secrets
	Identity Identity
	Install  func(old, next int64, adapter execution.Adapter)
	// SaveSettingsWithSecret is supplied where both stores share a local
	// transaction. A nil value deletes the secret.
	SaveSettingsWithSecret func(ctx context.Context, kind string, patch json.RawMessage, key string, value json.RawMessage) error
	SetLastError           func(ctx context.Context, kind, msg string) error
	DataDir                string
	Profile                Profile
	Logger                 *slog.Logger
	Getenv                 func(string) string
	LookupPath             func(string) (string, error)
	Overrides              map[string]any
	OnChanged              func()
	// CLI is the Stage CLI on this machine's PATH, named in fix commands
	// ("": DefaultCLI; see CLIName).
	CLI string
}
