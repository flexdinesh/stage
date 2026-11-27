// Package execution defines Stage's contract with an execution harness.
// Adapters own protocol, authentication, option semantics and status mapping.
package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
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
	Web    string `json:"web,omitempty"`
	Native string `json:"native,omitempty"`
	// Command is a shell command that resumes the session in the harness's
	// own client (a harness without a web or app destination); hosts offer
	// to copy it.
	Command string `json:"command,omitempty"`
}
type WorkspaceInfo struct {
	// OriginRemote: the raw origin remote URL the harness reports ("" when
	// none). It may carry credentials: only its normalized form
	// (gitmeta.RemoteIdentity) is compared, and it is never returned to a
	// client.
	OriginRemote  string     `json:"originRemote,omitempty"`
	ID            string     `json:"id"`
	Title         string     `json:"title"`
	WorkspaceRoot string     `json:"workspaceRoot"`
	ArchivedAt    *time.Time `json:"archivedAt,omitempty"`
	DeletedAt     *time.Time `json:"deletedAt,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	// Importable: the harness treats it as a deliberate project, so
	// discovery may import it as a Stage project (docs/architecture.md).
	Importable bool `json:"importable"`
	// ActiveAt: last harness activity, when reported; picks among IDs
	// sharing one workspace.
	ActiveAt *time.Time `json:"activeAt,omitempty"`
	// Repo: the normalized origin remote reported by the harness, when known.
	Repo string `json:"repo,omitempty"`
	// RepoPath: the harness-reported slash path from the checkout top to
	// WorkspaceRoot ("." at the top), "" when unknown. Set only with Repo.
	RepoPath string `json:"repoPath,omitempty"`
	// Local: WorkspaceRoot is a path on the filesystem of the machine that
	// hosts the adapter (a hosted harness, or a service sharing that
	// filesystem), so that machine's ops can read filesystem evidence.
	Local bool `json:"local,omitempty"`
	// ImportIf: the harness keeps no project list of its own and treats a
	// workspace as a deliberate project when it is a Git checkout; the
	// machine's ops read the files and set Importable. "": Importable is
	// the harness's own judgment.
	ImportIf ImportRule `json:"importIf,omitempty"`
	// BlockedReason: why the harness will not start a session here, in the
	// harness's words ("" when nothing known stops it). The row stays in
	// inventory but is not an eligible destination.
	BlockedReason string `json:"blockedReason,omitempty"`
	// Trust: the harness starts sessions only in folders its trust records
	// cover. The machine's ops read the files and set BlockedReason to
	// Trust.Reason when no record covers the workspace (TrustRule).
	Trust *TrustRule `json:"trust,omitempty"`
	// Evidence comes from machine.Ops on the workspace's file host.
	Evidence *WorkspaceEvidence `json:"evidence,omitempty"`
}

// TrustRule is a harness's folder trust (WorkspaceInfo.Trust). A record
// covers a workspace when it names the checkout's main root (the main
// working tree of a linked worktree), or the workspace or an ancestor up to
// the checkout top, or up to the filesystem root outside a checkout.
type TrustRule struct {
	// Records: folders the harness recorded as trusted, as written; they
	// match the canonical path verbatim.
	Records []string `json:"records"`
	// Reason: BlockedReason when no record covers the workspace.
	Reason string `json:"reason"`
}

// WorkspaceEvidence is read-only filesystem evidence for a local workspace.
type WorkspaceEvidence struct {
	CanonicalPath string `json:"canonicalPath,omitempty"`
	// CheckoutKey is the canonical Git common directory on the file host.
	CheckoutKey string `json:"checkoutKey,omitempty"`
	Subpath     string `json:"subpath,omitempty"`
	Remote      string `json:"remote,omitempty"`
	// InodeHint is the directory's device and inode on the file host.
	InodeHint string `json:"inodeHint,omitempty"`
	// Gone marks a directory confirmed missing on the file host.
	Gone bool `json:"gone,omitempty"`
}

// ImportRule is which Git checkouts a harness without a project list
// imports (WorkspaceInfo.ImportIf).
type ImportRule string

const (
	// ImportCheckoutTop: the top level of a Git checkout.
	ImportCheckoutTop ImportRule = "checkoutTop"
	// ImportInCheckout: any directory inside a Git checkout.
	ImportInCheckout ImportRule = "inCheckout"
)

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

// OptionKind is how a launch option takes its value: one of Values
// (choice), true or false (boolean), or a string the adapter validates
// (text).
type OptionKind string

const (
	OptionChoice  OptionKind = "choice"
	OptionBoolean OptionKind = "boolean"
	OptionText    OptionKind = "text"
)

// Option is a launch option a harness declares (harness-wide, or a model's
// own). Kind, Label and DependsOn may be absent in JSON from an older
// harness: EffectiveKind and Title fill them in.
type Option struct {
	ID string `json:"id"`
	// Kind: "" reads as boolean when every value is a boolean, else choice.
	Kind OptionKind `json:"kind,omitempty"`
	// Label names the option in the harness's own words ("" reads as ID).
	Label string `json:"label,omitempty"`
	// Values: a choice's values; a boolean's true and false; none for text.
	Values []OptionValue `json:"values"`
	// ValueLabels optionally names a choice's string values in the harness's words.
	ValueLabels map[string]string `json:"valueLabels,omitempty"`
	Default     *OptionValue      `json:"default"`
	// DependsOn: the option applies only while another option (of the
	// same config) has this value, e.g. baseRef only with worktree true.
	DependsOn *OptionDependency `json:"dependsOn,omitempty"`
}

// OptionDependency names the option and value another option applies
// under.
type OptionDependency struct {
	Option string      `json:"option"`
	Value  OptionValue `json:"value"`
}

// EffectiveKind is o's kind, inferred for an option declared without one.
func (o Option) EffectiveKind() OptionKind {
	if o.Kind != "" {
		return o.Kind
	}
	if len(o.Values) > 0 && !slices.ContainsFunc(o.Values, func(v OptionValue) bool { return !v.IsBool }) {
		return OptionBoolean
	}
	return OptionChoice
}

// Title is o's label, else its id.
func (o Option) Title() string {
	if o.Label != "" {
		return o.Label
	}
	return o.ID
}

// Lookup is the option of a config: its value and whether it is set.
func (c Config) Lookup(id string) (OptionValue, bool) {
	for _, o := range c.Options {
		if o.ID == id {
			return o.Value, true
		}
	}
	return OptionValue{}, false
}

// Bool is a boolean option's value (false when unset or not a boolean).
func (c Config) Bool(id string) bool {
	v, ok := c.Lookup(id)
	return ok && v.IsBool && v.Bool
}

// Text is a text option's trimmed value ("" when unset or a boolean).
func (c Config) Text(id string) string {
	v, ok := c.Lookup(id)
	if !ok || v.IsBool {
		return ""
	}
	return strings.TrimSpace(v.Str)
}

type Model struct {
	Choice
	Group       string   `json:"group"`
	Options     []Option `json:"options"`
	Permissions []Choice `json:"permissions"`
}
type Capabilities struct {
	Permissions []Choice `json:"permissions,omitempty"`
	// Options apply whatever the model (a harness-wide setting); a model's
	// own options sit beside them. Ids never collide (Valid).
	Options []Option `json:"options,omitempty"`
	// ModeLabel names the mode choice in the harness's own words ("Agent");
	// empty reads "Interaction mode".
	ModeLabel  string              `json:"modeLabel,omitempty"`
	Operations map[Op]Availability `json:"operations"`
	Models     []Model             `json:"models"`
	Modes      []Choice            `json:"modes"`
	Actions    []Action            `json:"actions,omitempty"`
	// WorkspaceSources are the ways OpWorkspaceAdd adds a workspace
	// (ValidateWorkspaceSources).
	WorkspaceSources []WorkspaceSource `json:"workspaceSources,omitempty"`
}
type Config struct {
	Model      string        `json:"model"`
	Options    []ModelOption `json:"options"`
	Permission string        `json:"permission"`
	Mode       string        `json:"mode"`
}
type Image struct {
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	Data     []byte `json:"data"`
}
type Intent struct {
	RequestID    string  `json:"requestId"`
	ConnectionID int64   `json:"connectionId"`
	Project      string  `json:"project"`
	Directory    string  `json:"directory"`
	Title        string  `json:"title"`
	Prompt       string  `json:"prompt"`
	Config       Config  `json:"config"`
	Images       []Image `json:"images"`
	// Marker is LaunchMarker(RequestID) for a correlated launch (also at the
	// end of Prompt); "" for a reserved one.
	Marker string `json:"marker,omitempty"`
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
	Text      string    `json:"text"`
	Truncated bool      `json:"truncated"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Usage is what a session has consumed over its whole life, as its harness
// records it: cumulative, best effort, never estimated by Stage.
type Usage struct {
	// Turns: prompts the session answered or is answering.
	Turns int `json:"turns"`
	// InputTokens is uncached input; cache reads and writes are separate.
	InputTokens      int64 `json:"inputTokens"`
	CacheReadTokens  int64 `json:"cacheReadTokens"`
	CacheWriteTokens int64 `json:"cacheWriteTokens"`
	OutputTokens     int64 `json:"outputTokens"`
	// WorkingMs: time spent on finished turns (0 when unknown).
	WorkingMs int64 `json:"workingMs"`
	// CostUSD: a cost the harness itself reports; nil when it reports none.
	// Stage never prices tokens.
	CostUSD *float64 `json:"costUsd"`
}

type Observation struct {
	Parent      *SessionRef `json:"parent,omitempty"`
	Config      Config      `json:"config"`
	Session     SessionRef  `json:"session"`
	Project     string      `json:"project"`
	Title       string      `json:"title"`
	State       string      `json:"state"`
	Active      bool        `json:"active"`
	Archived    bool        `json:"archived"`
	Child       bool        `json:"child"`
	Branch      string      `json:"branch"`
	Directory   string      `json:"directory"`
	Links       Links       `json:"links"`
	CreatedAt   time.Time   `json:"createdAt"`
	UpdatedAt   time.Time   `json:"updatedAt"`
	StartedAt   *time.Time  `json:"startedAt"`
	EndedAt     *time.Time  `json:"endedAt"`
	ExecutionID string      `json:"executionId"`
	Reply       *Reply      `json:"reply,omitempty"`
	Usage       *Usage      `json:"usage,omitempty"` // nil: the harness reports none (or has not yet)
	Facts       []Fact      `json:"facts,omitempty"`
	Closed      *Closure    `json:"closed,omitempty"`
	// StateReason qualifies State; "" when the state speaks for itself.
	StateReason string `json:"stateReason,omitempty"`
	// Origin is the client that started the session as the harness
	// records it (Claude Code's entrypoint, Codex's originator), raw; ""
	// when it records none. The coordinator refuses a session whose origin
	// another registered harness wraps (harness.Wraps).
	Origin string `json:"origin,omitempty"`
}

// ReasonOwnerExited: the process that owned the execution exited without
// reporting an outcome, so the execution is interrupted. The coordinator
// requires the harness to declare this reason.
const ReasonOwnerExited = "owner_exited"

// Closure signals. A closure never implies the execution succeeded.
const (
	ClosedSettled  = "settled"  // T3 thread settled
	ClosedArchived = "archived" // OpenCode session archived; Claude Code session removed (`claude rm`)
)

// Closure reports that the session was closed in its harness. Stage may infer
// completion from it (docs/execution.md).
type Closure struct {
	Signal string `json:"signal"`
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
	if o.StateReason == ReasonOwnerExited && o.State != "interrupted" {
		return fmt.Errorf("malformed observation: %s with state %q", o.StateReason, o.State)
	}
	return nil
}

// Rejected guarantees that no execution was started by this intent. All other
// launch errors are uncertain, including malformed or incomplete responses.
type Rejected struct{ Reason string }

func (e *Rejected) Error() string { return e.Reason }

// Refused: Stage will not track this session (another harness wraps it,
// e.g. a Claude session T3 started: harness.Wraps). Reason says where to
// track it instead. It is a decision, not a failure: the API answers 409.
type Refused struct{ Reason string }

func (e *Refused) Error() string { return e.Reason }

// Invalid: the harness refused an answer that does not fit its request
// (Interactive.Respond); nothing was sent. The API answers 400.
type Invalid struct{ Reason string }

func (e *Invalid) Error() string { return e.Reason }

// Busy: the session can't take a message now (it is working, waits on a
// request, or runs where Stage can't reach it); nothing was sent. Reason
// says why and what to do. The API answers 409 session_busy.
type Busy struct{ Reason string }

func (e *Busy) Error() string { return e.Reason }

type LinkResolver interface {
	ResolveLink(context.Context, string) (SessionRef, error)
}

// WorkspaceFeatures is implemented by harnesses whose configuration depends
// on the selected checkout. Use the pinned external project and directory.
type WorkspaceFeatures interface {
	FeaturesForWorkspace(ctx context.Context, projectID, directory string) (Capabilities, error)
}
type Catalog interface {
	Health(context.Context) Health
	Features(context.Context) (Capabilities, error)
}

type Inventory interface {
	Workspaces(context.Context) ([]WorkspaceInfo, error)
}

type Sessions interface {
	Observe(context.Context, SessionRef) (Observation, error)
	Opening(context.Context, SessionRef, bool) (*Reply, error)
	Attention(context.Context) ([]SessionRef, error)
}

type SessionReadMode string

const (
	SessionReadPreview SessionReadMode = "preview"
	SessionReadConfirm SessionReadMode = "confirm"
)

type sessionReadKey struct{}

// WithSessionReadMode marks a named read so adapters can bypass polling caches
// and omit reply text from a preview.
func WithSessionReadMode(ctx context.Context, mode SessionReadMode) context.Context {
	return context.WithValue(ctx, sessionReadKey{}, mode)
}

func SessionReadModeFrom(ctx context.Context) SessionReadMode {
	mode, _ := ctx.Value(sessionReadKey{}).(SessionReadMode)
	return mode
}

type Launcher interface {
	Launch(context.Context, Intent, Checkpoint, SaveCheckpoint) (Observation, error)
	// Stop validates that the scoped session can be stopped, even when its
	// model catalog is unreachable.
	Stop(context.Context, SessionRef) error
}

type Adapter interface {
	Catalog
	Inventory
	Sessions
	Launcher
}
