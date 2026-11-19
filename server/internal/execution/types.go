// Package execution defines Stage's contract with an execution harness.
// Adapters own protocol, authentication, option semantics and status mapping.
package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	ErrUnavailable = errors.New("execution unavailable")
	ErrNotFound    = errors.New("session unavailable")
	ErrNoReply     = errors.New("no reply")
)

type SessionRef struct {
	ConnectionID      int64  `json:"connectionId"`
	ExternalSessionID string `json:"externalSessionId"`
}
type Availability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}
type Health struct {
	Availability
	Identity  string    `json:"identity"`
	CheckedAt time.Time `json:"checkedAt"`
}
type Links struct {
	Service string `json:"service,omitempty"`
	Web     string `json:"web,omitempty"`
	App     string `json:"app,omitempty"`
	Native  string `json:"native,omitempty"`
}
type WorkspaceInfo struct {
	OriginRemote  string `json:"-"`
	ID            string
	Title         string
	WorkspaceRoot string
	ArchivedAt    *time.Time
	DeletedAt     *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
	// Importable: the harness treats it as a deliberate project, so
	// discovery may import it as a Stage project (docs/architecture.md).
	Importable bool `json:"-"`
	// ActiveAt: last harness activity, when reported; picks among IDs
	// sharing one workspace.
	ActiveAt *time.Time `json:"-"`
	// Repo: normalized origin remote (host/owner/name), "" when unknown.
	// Search metadata only; never an identity.
	Repo string `json:"-"`
}
type ModelOption struct {
	ID    string      `json:"id"`
	Value OptionValue `json:"value"`
}
type OptionValue struct {
	Str    string
	Bool   bool
	IsBool bool
}

func StringValue(s string) OptionValue { return OptionValue{Str: s} }
func BoolValue(b bool) OptionValue     { return OptionValue{Bool: b, IsBool: true} }
func (v OptionValue) MarshalJSON() ([]byte, error) {
	if v.IsBool {
		return json.Marshal(v.Bool)
	}
	return json.Marshal(v.Str)
}
func (v *OptionValue) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return errors.New("option value must be string or boolean")
	}
	var x bool
	if json.Unmarshal(b, &x) == nil {
		*v = BoolValue(x)
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("option value must be string or boolean: %w", err)
	}
	*v = StringValue(s)
	return nil
}

type Choice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Availability
}
type Option struct {
	ID      string        `json:"id"`
	Values  []OptionValue `json:"values"`
	Default *OptionValue  `json:"default"`
}
type Model struct {
	Choice
	Group       string   `json:"group"`
	Options     []Option `json:"options"`
	Permissions []Choice `json:"permissions"`
}
type Capabilities struct {
	Permissions        []Choice                `json:"permissions,omitempty"`
	Operations         map[string]Availability `json:"operations"`
	Models             []Model                 `json:"models"`
	Modes              []Choice                `json:"modes"`
	Workspaces         []Choice                `json:"workspaces"`
	SessionEnvironment string                  `json:"sessionEnvironment"`
}
type Config struct {
	Model      string        `json:"model"`
	Options    []ModelOption `json:"options"`
	Permission string        `json:"permission"`
	Mode       string        `json:"mode"`
}
type Workspace struct {
	Kind       string `json:"kind"`
	Base       string `json:"base,omitempty"`
	Branch     string `json:"branch,omitempty"`
	FromRemote bool   `json:"fromRemote"`
}
type Image struct {
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	Data     []byte `json:"data"`
}
type Intent struct {
	RequestID    string    `json:"requestId"`
	ConnectionID int64     `json:"connectionId"`
	Project      string    `json:"project"`
	Directory    string    `json:"directory"`
	Title        string    `json:"title"`
	Prompt       string    `json:"prompt"`
	Config       Config    `json:"config"`
	Workspace    Workspace `json:"workspace"`
	Images       []Image   `json:"images"`
}

// Checkpoint is adapter-owned, versioned JSON. It must contain no credentials.
// Save must commit before the adapter performs the following external effect.
type Checkpoint struct {
	Version int             `json:"version"`
	Data    json.RawMessage `json:"data"`
}
type SaveCheckpoint func(context.Context, Checkpoint) error
type Reply struct {
	ItemID    string    `json:"itemId"`
	RunID     string    `json:"runId"`
	MessageID string    `json:"messageId"`
	Text      string    `json:"text"`
	Truncated bool      `json:"truncated"`
	Position  int       `json:"position"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Artifact retains historical PR JSON without exposing PR tracking operations.
type Artifact struct {
	Host       string `json:"host"`
	Repository string `json:"repository"`
	Number     int    `json:"number"`
	URL        string `json:"url"`
	Source     string `json:"source"`
	State      string `json:"state"`
}
type Observation struct {
	ModelLabel  string       `json:"modelLabel"`
	Parent      *SessionRef  `json:"parent,omitempty"`
	Config      Config       `json:"config"`
	Session     SessionRef   `json:"session"`
	Project     string       `json:"project"`
	Title       string       `json:"title"`
	State       string       `json:"state"`
	Active      bool         `json:"active"`
	Archived    bool         `json:"archived"`
	Child       bool         `json:"child"`
	Branch      string       `json:"branch"`
	Directory   string       `json:"directory"`
	Links       Links        `json:"links"`
	CreatedAt   time.Time    `json:"createdAt"`
	UpdatedAt   time.Time    `json:"updatedAt"`
	StartedAt   *time.Time   `json:"startedAt"`
	EndedAt     *time.Time   `json:"endedAt"`
	ExecutionID string       `json:"executionId"`
	Reply       *Reply       `json:"reply,omitempty"`
	Artifacts   []Artifact   `json:"artifacts"`
	Base        Availability `json:"base"`
	Closed      *Closure     `json:"closed,omitempty"`
}

// Closure signals. A closure never implies the execution succeeded.
const (
	ClosedSettled  = "settled"  // T3 thread settled
	ClosedArchived = "archived" // OpenCode session archived
)

// Closure reports that the session was closed in its harness. Stage may infer
// completion from it (docs/execution.md).
type Closure struct {
	Signal string `json:"signal"`
}
type Source interface{ Observation() Observation }

func (o Observation) Observation() Observation { return o }

type Credential struct {
	ClientID  string
	Token     string
	ExpiresAt time.Time
}

func (o Observation) Valid() error {
	if o.Session.ConnectionID <= 0 || o.Session.ExternalSessionID == "" || o.Project == "" {
		return errors.New("malformed observation: missing identity")
	}
	switch o.State {
	case "running", "needs_input", "done", "failed", "interrupted", "cancelled", "idle":
	default:
		return fmt.Errorf("malformed observation: unknown state %q", o.State)
	}
	if o.Active != (o.State == "running" || o.State == "needs_input") {
		return errors.New("malformed observation: contradictory activity")
	}
	return nil
}

// Rejected guarantees that no execution was started by this intent. All other
// launch errors are uncertain, including malformed or incomplete responses.
type Rejected struct{ Reason string }

func (e *Rejected) Error() string { return e.Reason }

type LegacyDecoder interface {
	LegacyConfiguration(json.RawMessage) (Config, error)
}

type LinkResolver interface {
	ResolveLink(context.Context, string) (SessionRef, error)
}

// WorkspaceFeatures is implemented by harnesses whose configuration depends
// on the selected checkout. Use the pinned external project and directory.
type WorkspaceFeatures interface {
	FeaturesForWorkspace(ctx context.Context, projectID, directory string) (Capabilities, error)
}
type Adapter interface {
	Health(context.Context) Health
	Features(context.Context) (Capabilities, error)
	Workspaces(context.Context) ([]WorkspaceInfo, error)
	RegisterWorkspace(context.Context, string, string) (WorkspaceInfo, error)
	Observe(context.Context, SessionRef) (Observation, error)
	Launch(context.Context, Intent, Checkpoint, SaveCheckpoint) (Observation, error)
	Stop(context.Context, SessionRef) error
	Attention(context.Context) ([]SessionRef, error)
	Opening(context.Context, SessionRef, bool) (*Reply, error)
	Destinations(context.Context, SessionRef) Links
}
