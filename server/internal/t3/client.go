// Package t3 is the boundary to T3 Code. Everything T3-specific (MCP over
// streamable HTTP at T3_URL/mcp, OAuth auto-renew, tool result decoding) lives
// behind the Client interface. The rest of the backend uses only these types.
//
// Field sources: t3code packages/contracts/src/orchestratorMcp.ts,
// project.ts, model.ts, modelSelection.ts, orchestrationV2.ts (t3 0.1.7).
package t3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Client is the T3 surface the backend consumes. Implementations must be safe
// for concurrent use. Calls outside a T3 thread must pass projectId/threadId
// explicitly (OAuth clients have no implicit thread).
type Client interface {
	// Status reports the current connection/auth state. Never fails.
	Status(ctx context.Context) ConnectionStatus
	// Pair exchanges a pairing code (from `t3 pair`) for a full-access
	// mcp-client token and stores it (paste fallback for auto-renew).
	Pair(ctx context.Context, code string) error
	// ListProjects pages t3_project_list to completion.
	ListProjects(ctx context.Context) ([]Project, error)
	// CreateProject calls t3_project_create.
	CreateProject(ctx context.Context, in CreateProjectInput) (Project, error)
	// ListThreads calls t3_thread_list for one page.
	ListThreads(ctx context.Context, in ListThreadsInput) (ThreadPage, error)
	// SearchThreads calls t3_thread_search: active, non-subagent threads by
	// title/branch/PR/project, then message text. Not exhaustive.
	SearchThreads(ctx context.Context, in SearchThreadsInput) ([]SearchMatch, error)
	// ReadThread calls t3_thread_read (items limit 1). ErrNotFound if unknown.
	ReadThread(ctx context.Context, threadID string) (ThreadDetail, error)
	// LatestReply finds a thread's latest assistant reply (optionally for one
	// run) and returns up to MaxChars of its text, paging long text.
	// ErrNoReply if none; ErrNotFound if the thread is unknown.
	LatestReply(ctx context.Context, in LatestReplyInput) (Reply, error)
	// LaunchThread calls t3_thread_launch. Idempotent per ClientRequestID.
	LaunchThread(ctx context.Context, in LaunchInput) (LaunchResult, error)
	// SendMessage calls t3_thread_send (follow-up).
	SendMessage(ctx context.Context, threadID, message string, mode SendMode) error
	// QueueContinuation always creates a new turn. A request ID is persisted
	// for correlation, without assuming T3 deduplicates sends.
	QueueContinuation(ctx context.Context, threadID, message, requestID string) (SendResult, error)
	// UploadAttachment calls t3_attachment_prepare_upload, then POSTs data to
	// the returned signed relativeUrl on T3_URL. The pending id is usable in
	// LaunchInput.Attachments or SendAttachments until it expires (~10 min).
	UploadAttachment(ctx context.Context, in UploadInput) (Attachment, error)
	// DiscardAttachment calls t3_attachment_discard (pending uploads only).
	DiscardAttachment(ctx context.Context, attachmentID string) error
	// SendAttachments calls t3_thread_send_attachments (1..8, no mode, no
	// retry key).
	SendAttachments(ctx context.Context, threadID, message string, atts []Attachment) error
	// Interrupt calls t3_thread_interrupt with stop:true (Stop button semantics).
	Interrupt(ctx context.Context, threadID string) error
	// Capabilities calls orchestrator_capabilities. Returns all providers
	// unfiltered; callers drop providers with non-empty Constraints.
	Capabilities(ctx context.Context) (Catalog, error)
}

// TokenStore persists the OAuth mcp-client token. The db package implements
// it against the single-row t3_connection table; the t3 client consumes it.
type TokenStore interface {
	LoadToken(ctx context.Context) (StoredToken, error) // zero value if none
	SaveToken(ctx context.Context, tok StoredToken) error
	SetLastError(ctx context.Context, msg string) error // "" clears
}

type StoredToken struct {
	ClientID  string
	Token     string
	ExpiresAt time.Time
}

var (
	ErrNotConnected = errors.New("t3: not connected")
	ErrNotFound     = errors.New("t3: not found")
	ErrNoReply      = errors.New("t3: no assistant reply")
)

type ConnectionStatus struct {
	Connected     bool
	ClientID      string
	ExpiresAt     *time.Time
	LastError     string
	EnvironmentID string // T3 environment id, used for web links; "" if unknown
}

// ThreadStatus is T3 OrchestratorMcpThreadStatus ("idle" + run statuses).
// Run statuses (Run.Status, LaunchResult.Status) use the same values minus idle.
type ThreadStatus string

const (
	StatusIdle        ThreadStatus = "idle"
	StatusPreparing   ThreadStatus = "preparing"
	StatusQueued      ThreadStatus = "queued"
	StatusStarting    ThreadStatus = "starting"
	StatusRunning     ThreadStatus = "running"
	StatusWaiting     ThreadStatus = "waiting"
	StatusCompleted   ThreadStatus = "completed"
	StatusInterrupted ThreadStatus = "interrupted"
	StatusFailed      ThreadStatus = "failed"
	StatusCancelled   ThreadStatus = "cancelled"
	StatusRolledBack  ThreadStatus = "rolled_back"
)

type RuntimeMode string

const (
	RuntimeApprovalRequired RuntimeMode = "approval-required"
	RuntimeAutoAcceptEdits  RuntimeMode = "auto-accept-edits"
	RuntimeAuto             RuntimeMode = "auto"
	RuntimeFullAccess       RuntimeMode = "full-access"
)

type InteractionMode string

const (
	InteractionDefault InteractionMode = "default"
	InteractionPlan    InteractionMode = "plan"
)

// SendMode is t3_thread_send `mode`. auto: start idle thread, steer running
// turn, or queue. queue: new turn. steer: mid-turn update. restart: interrupt
// and restart.
type SendMode string

type SendResult struct {
	ThreadID  string `json:"threadId"`
	RunID     string `json:"runId"`
	MessageID string `json:"messageId"`
	Delivery  string `json:"delivery"`
}

const (
	SendAuto    SendMode = "auto"
	SendQueue   SendMode = "queue"
	SendSteer   SendMode = "steer"
	SendRestart SendMode = "restart"
)

type Project struct {
	ID            string
	Title         string
	WorkspaceRoot string
	ArchivedAt    *time.Time
	DeletedAt     *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type CreateProjectInput struct {
	Title         string
	WorkspaceRoot string
}

type ListThreadsInput struct {
	ProjectID        string // ignored when AllProjects
	AllProjects      bool
	Statuses         []ThreadStatus // max 10
	TitleContains    string
	Settled          *bool
	IncludeSubagents bool
	Cursor           *int // nil = first page
	Limit            int  // 1..100; 0 = T3 default
}

type SearchThreadsInput struct {
	Query     string // trimmed, 2..200 chars
	ProjectID string // "" = all projects (calls outside a thread)
	Limit     int    // 1..50; 0 = T3 default (50)
}

// SearchSource is why T3 matched a thread: "title" covers title, branch, PR
// and project; user/assistant are message text matches.
type SearchSource string

const (
	SearchTitle     SearchSource = "title"
	SearchUser      SearchSource = "user"
	SearchAssistant SearchSource = "assistant"
)

// SearchMatch is one t3_thread_search match. Snippet and MessageCreatedAt
// are set for message matches only.
type SearchMatch struct {
	ThreadID         string
	ProjectID        string
	Title            string
	Source           SearchSource
	Snippet          string
	MessageCreatedAt *time.Time
}

type ThreadPage struct {
	Threads    []Thread
	NextCursor *int // nil = last page
	Total      int
}

// Thread is a t3_thread_list item. Nullable T3 ids decode to "".
type Thread struct {
	ThreadID             string
	ProjectID            string
	Title                string
	Link                 string // t3-thread:// or hosted link; may be ""
	CreatedBy            string // user|agent|system
	CreationSource       string // web|mobile|mcp|provider|server
	Status               ThreadStatus
	Archived             bool
	LatestRunID          string
	ProviderInstanceID   string
	Model                string
	RuntimeMode          RuntimeMode
	InteractionMode      InteractionMode
	Settled              bool
	SettledAt            *time.Time
	Snoozed              bool
	ParentThreadID       string
	RelationshipToParent string // fork|subagent|""
	ItemCount            int
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// ThreadDetail is t3_thread_read: thread + detail fields + recentRuns.
type ThreadDetail struct {
	Thread
	ActiveRunID         string
	Branch              string
	WorktreePath        string
	RunCount            int
	PendingRequestCount int
	RecentRuns          []Run // newest first
}

// LatestReplyInput selects a thread's latest assistant reply.
type LatestReplyInput struct {
	ThreadID string
	RunID    string // "" = any run
	MaxChars int    // text budget in UTF-16 units (T3 counts these); <= 0 = 50000
	// ItemCount is thread.itemCount from a recent ReadThread; 0 = read it.
	ItemCount int
}

// Reply is an assistant_message timeline item. Truncated: the message is
// longer than the returned Text.
type Reply struct {
	ItemID    string
	RunID     string
	MessageID string
	Text      string
	Truncated bool
	Position  int
	UpdatedAt time.Time
}

type Run struct {
	RunID              string
	Ordinal            int
	Status             ThreadStatus
	ProviderInstanceID string
	Model              string
	RequestedAt        time.Time
	StartedAt          *time.Time
	CompletedAt        *time.Time
}

// ModelSelection is T3 ModelSelection wire form:
// {"instanceId":"claude","model":"claude-haiku-5-5","options":[{"id":"effort","value":"low"}]}.
// T3 also accepts options as a record {"effort":"low"}; we always send the array.
type ModelSelection struct {
	InstanceID string        `json:"instanceId"`
	Model      string        `json:"model"`
	Options    []ModelOption `json:"options,omitempty"`
}

// ModelOption is T3 ProviderOptionSelection {id, value: string|boolean}.
type ModelOption struct {
	ID    string      `json:"id"`
	Value OptionValue `json:"value"`
}

// OptionValue is T3 ProviderOptionSelectionValue: a non-empty string or a boolean.
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

func (v *OptionValue) UnmarshalJSON(data []byte) error {
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		*v = BoolValue(b)
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("option value must be string or boolean: %w", err)
	}
	*v = StringValue(s)
	return nil
}

// WorkspaceType is t3_thread_launch workspaceStrategy.type.
type WorkspaceType string

const (
	WorkspaceRoot             WorkspaceType = "root"              // project checkout (+ optional Branch)
	WorkspaceExistingWorktree WorkspaceType = "existing_worktree" // requires WorktreePath
	WorkspaceWorktree         WorkspaceType = "worktree"          // new checkout from BaseRef (required)
)

type WorkspaceStrategy struct {
	Type            WorkspaceType
	Branch          string
	BaseRef         string // worktree only, e.g. "origin/main"
	StartFromOrigin *bool  // worktree only
	WorktreePath    string // existing_worktree only
}

// MaxAttachments is T3's per-message attachment limit.
const MaxAttachments = 8

// UploadInput is one image to upload (image/png|jpeg|gif|webp, <=10MB).
type UploadInput struct {
	Name     string
	MimeType string
	Data     []byte
}

// Attachment is an uploaded image as T3 messages reference it
// ({type:"image", id, name, mimeType, sizeBytes}).
type Attachment struct {
	ID        string
	Name      string
	MimeType  string
	SizeBytes int
}

type LaunchInput struct {
	ProjectID       string       // required (no implicit project outside a thread)
	Title           string       // required
	Message         string       // first prompt
	Attachments     []Attachment // uploaded images (max 8)
	ModelSelection  *ModelSelection
	RuntimeMode     RuntimeMode     // "" = inherit
	InteractionMode InteractionMode // "" = inherit
	Workspace       *WorkspaceStrategy
	ClientRequestID string // "task-<id>-<uuid>"
}

type LaunchResult struct {
	ThreadID       string
	Link           string
	ProjectID      string
	ModelSelection ModelSelection
	RunID          string       // "" if null
	Status         ThreadStatus // "" if null
}

// Catalog is orchestrator_capabilities.
type Catalog struct {
	Providers []Provider
}

type Provider struct {
	ProviderInstanceID string
	DriverKind         string
	DisplayName        string
	Models             []Model
	RuntimeModes       []RuntimeMode // nil = not reported
	CanRunChildTask    bool
	Constraints        []string // non-empty = unusable for launches
}

type Model struct {
	ID      string
	Label   string // "" when T3 omits it (label == id)
	Options []ModelOptionSpec
}

// ModelOptionSpec is OrchestratorMcpModelOption, e.g.
// {"id":"effort","values":["low","medium","high"],"default":"medium"}.
type ModelOptionSpec struct {
	ID      string
	Values  []OptionValue
	Default *OptionValue
}
