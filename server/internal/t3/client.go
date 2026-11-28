// Package t3 is the boundary to T3 Code. Everything T3-specific (MCP over
// streamable HTTP at <T3 URL>/mcp, OAuth auto-renew, tool result decoding) lives
// behind the Client interface. The rest of the backend uses only these types.
//
// Field sources: t3code packages/contracts/src/orchestratorMcp.ts,
// project.ts, model.ts, modelSelection.ts, orchestrationV2.ts (t3 0.1.7).
package t3

import (
	"context"
	"errors"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Client is the T3 surface the backend consumes. Implementations must be safe
// for concurrent use. Calls outside a T3 thread must pass projectId/threadId
// explicitly (OAuth clients have no implicit thread).
type Client interface {
	// Status reports the current connection/auth state from memory (the
	// probe cache included): never fails, never calls T3.
	Status(ctx context.Context) ConnectionStatus
	// Probe returns the reachability probe of the T3 URL, refreshing it when
	// older than the probe TTL (concurrent refreshes share one request). It
	// waits at most until ctx ends, then returns the cached probe.
	Probe(ctx context.Context) Probe
	// Reconfigure points the client at another T3 URL (server calls) and
	// public URL (browser links). A base URL change drops the MCP session,
	// the known environment id and the probe cache; the token is kept.
	Reconfigure(base, public string)
	// SetAdminToken replaces the admin token that renews the T3 token (""
	// disables renewal). A non-empty one obtains a token now when there is
	// no valid one, so a bad admin token fails here.
	SetAdminToken(ctx context.Context, token string) error
	// Pair exchanges a pairing code (from `t3 pair`) for a full-access
	// mcp-client token and stores it (paste fallback for auto-renew).
	Pair(ctx context.Context, code string) error
	// Unpair forgets the stored token (nothing is sent to T3; the token
	// stays valid there until it expires) and drops the MCP session.
	Unpair(ctx context.Context) error
	// ListProjects pages t3_project_list to completion.
	ListProjects(ctx context.Context) ([]Project, error)
	// CreateProject calls t3_project_create (not retried: T3 has no request
	// id for it).
	CreateProject(ctx context.Context, in CreateProjectInput) (CreatedProject, error)
	// CloneProject calls t3_project_clone (not retried). T3 sets no timeout;
	// ctx bounds it.
	CloneProject(ctx context.Context, in CloneInput) (CloneResult, error)
	// UpdateProject calls t3_project_update with only the set fields. It
	// sets values, so a call lost on a closed connection is retried once.
	UpdateProject(ctx context.Context, projectID string, in ProjectUpdate) error
	// DeleteProject calls t3_project_delete without force (not retried):
	// T3 refuses a project that has threads.
	DeleteProject(ctx context.Context, projectID string) error
	// ListThreads calls t3_thread_list for one page.
	ListThreads(ctx context.Context, in ListThreadsInput) (ThreadPage, error)
	// ReadThread calls t3_thread_read (items limit 1). ErrNotFound if unknown.
	ReadThread(ctx context.Context, threadID string) (ThreadDetail, error)
	// LatestReply finds a thread's latest assistant reply (optionally for one
	// run) and returns up to MaxChars of its text, paging long text.
	// ErrNoReply if none; ErrNotFound if the thread is unknown.
	LatestReply(ctx context.Context, in LatestReplyInput) (Reply, error)
	// ReadItems calls t3_thread_read (view messages) for one page of
	// timeline items. ErrNotFound if unknown.
	ReadItems(ctx context.Context, in ReadItemsInput) (ItemPage, error)
	// LaunchThread calls t3_thread_launch. Idempotent per ClientRequestID.
	LaunchThread(ctx context.Context, in LaunchInput) (LaunchResult, error)
	// UploadAttachment calls t3_attachment_prepare_upload, then POSTs data to
	// the returned signed relativeUrl on the T3 URL. The pending id is usable in
	// LaunchInput.Attachments until it expires (~10 min).
	UploadAttachment(ctx context.Context, in UploadInput) (Attachment, error)
	// DiscardAttachment calls t3_attachment_discard (pending uploads only).
	DiscardAttachment(ctx context.Context, attachmentID string) error
	// Interrupt calls t3_thread_interrupt with stop:true (Stop button semantics).
	Interrupt(ctx context.Context, threadID string) error
	// RenameThread calls t3_thread_update {action: "rename"} with a fresh
	// clientRequestId (reused when the call is retried).
	RenameThread(ctx context.Context, threadID, title string) error
	// SendMessage calls t3_thread_send. T3 deduplicates by a non-empty
	// clientRequestID (a retry with it is safe). ErrNotFound if the thread
	// is unknown.
	SendMessage(ctx context.Context, threadID, message string, mode SendMode, clientRequestID string) error
	// ListQueued pages t3_queue list in send order. Writes address a queued
	// run by ID and return ErrNotFound once it leaves the queue.
	ListQueued(ctx context.Context, threadID string) ([]QueuedRun, error)
	CancelQueued(ctx context.Context, threadID, queuedRunID string) error
	PromoteQueued(ctx context.Context, threadID, queuedRunID, targetRunID string) error
	// ListPendingQuestions calls t3_pending_request list: the thread's
	// pending user-input request ids (approvals are not listed).
	ListPendingQuestions(ctx context.Context, threadID string) ([]string, error)
	// ReadPendingQuestion calls t3_pending_request read. ErrRequestAnswered
	// or ErrRequestStale once it is no longer pending.
	ReadPendingQuestion(ctx context.Context, threadID, requestID string) (PendingQuestion, error)
	// RespondPendingQuestion calls t3_pending_request respond.
	RespondPendingQuestion(ctx context.Context, threadID, requestID string, answers map[string]Answer) error
	// OrganizeThread calls t3_thread_organize {threadId, action} (settle,
	// archive). T3 validates it: an archived thread cannot settle, a running
	// one cannot settle, an archived one cannot archive again.
	OrganizeThread(ctx context.Context, threadID, action string) error
	// Capabilities reads orchestrator_capabilities and MCP tools/list.
	Capabilities(ctx context.Context) (Catalog, error)
}

// TokenStore persists the OAuth mcp-client token and reports auth errors.
type TokenStore interface {
	LoadToken(ctx context.Context) (StoredToken, error) // zero value if none
	SaveToken(ctx context.Context, tok StoredToken) error
	SetLastError(ctx context.Context, msg string) error // "" clears
}

// StoredToken is the persisted OAuth mcp-client credential.
type StoredToken struct {
	ClientID  string    `json:"clientId"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

var (
	ErrNotConnected error = unavailableError{}
	ErrNotFound           = execution.ErrNotFound
	ErrNoReply            = errors.New("t3: no assistant reply")
)

type ConnectionStatus struct {
	Connected     bool // a valid token T3 has not rejected
	ClientID      string
	ExpiresAt     *time.Time
	LastError     string
	EnvironmentID string // T3 environment id, used for web links; "" if unknown
	HasToken      bool   // a token is stored (paired at least once)
	Expired       bool   // the stored token is past its expiry
	Rejected      bool   // T3 answered 401 to the current token
	Probe         Probe  // last reachability probe (zero: none yet)
}

// State is the T3 connection state shown to users.
type State string

const (
	StateUnreachable State = "unreachable" // nothing answered as T3 at the URL
	StateUnpaired    State = "unpaired"    // no token
	StateExpired     State = "expired"     // token past expiry
	StateRejected    State = "rejected"    // T3 answered 401 to the token
	StateConnected   State = "connected"
)

// State reports the first problem in order: unreachable, unpaired,
// expired, rejected; else connected.
func (s ConnectionStatus) State() State {
	switch {
	case !s.Probe.Reachable:
		return StateUnreachable
	case !s.HasToken:
		return StateUnpaired
	case s.Expired:
		return StateExpired
	case s.Rejected:
		return StateRejected
	}
	return StateConnected
}

// Identity is what T3 says about itself at /.well-known/t3/environment.
type Identity struct {
	EnvironmentID string
	Label         string
	ServerVersion string
}

// Probe is one unauthenticated GET {T3 URL}/.well-known/t3/environment.
type Probe struct {
	Reachable bool      // answered 200 with an environment id
	CheckedAt time.Time // zero: never probed
	Err       string    // short reason when not reachable
	Identity  *Identity // nil when not reachable
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

// SendMode is the subset of t3_thread_send modes Stage uses.
type SendMode string

const (
	SendQueue SendMode = "queue"
	SendSteer SendMode = "steer"
)

type Project = execution.WorkspaceInfo

// CreateProjectInput is t3_project_create. An empty WorkspaceRoot asks T3
// to start a new project from Title (a Git repository in its projects
// folder); CreateIfMissing is then not sent.
type CreateProjectInput struct {
	Title           string
	WorkspaceRoot   string
	CreateIfMissing bool
}

// CreatedProject is t3_project_create's result. CommitError: T3 created the
// project from its title but could not make the first commit.
type CreatedProject struct {
	Project
	CommitError string
}

// CloneInput is t3_project_clone. DestinationPath must be new or empty.
type CloneInput struct {
	RemoteURL       string
	DestinationPath string
}

// ProjectUpdate is a t3_project_update change; nil fields stay unchanged.
type ProjectUpdate struct {
	Title    *string
	Archived *bool
}

// CloneResult is the checkout T3 cloned (not yet a project).
type CloneResult struct {
	Cwd string
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
	// PullRequests: linkedPullRequest first, then pullRequests, deduped by URL.
	PullRequests []PullRequest
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// PullRequest is a PR T3 associates with a thread. State is T3's string
// (open, merged, closed, draft, ...); Source is created, agent or linked.
type PullRequest struct {
	Host       string
	Repository string
	Number     int
	URL        string
	Source     string
	State      string
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
type Reply = execution.Reply

// Item limits of t3_thread_read.
const (
	MaxItemsPerRead = 100
	MaxItemChars    = 50000
)

// ReadItemsInput pages t3_thread_read view "messages" (user_message,
// assistant_message, proposed_plan) by global timeline position.
type ReadItemsInput struct {
	ThreadID      string
	Activity      bool   // newest first, including commands and other tool items
	AfterPosition *int   // nil = from the start (T3 rejects -1)
	Limit         int    // 1..MaxItemsPerRead; 0 = T3 default (50)
	MaxChars      int    // per item, 1..MaxItemChars; 0 = T3 default (20000)
	ItemID        string // set: that item only, text from TextOffset
	TextOffset    int
}

// ItemPage is one t3_thread_read page. NextPosition is the last returned
// item's position (nil when empty).
type ItemPage struct {
	Thread       ThreadDetail
	Items        []TimelineItem
	NextPosition *int
	HasMore      bool
}

// TimelineItem is OrchestratorMcpThreadTimelineItem. Positions are global
// timeline positions (tool items in between are skipped by the view).
type TimelineItem struct {
	Position       int
	Visibility     string // local|inherited|synthetic
	SourceThreadID string
	ItemID         string
	RunID          string
	MessageID      string
	CreatedBy      string // user|agent|system|""
	Type           string // user_message|assistant_message|proposed_plan
	Status         string // idle|pending|running|waiting|completed|failed|cancelled|interrupted
	Title          string
	Text           string
	TextTruncated  bool
	NextTextOffset *int
	UpdatedAt      time.Time
}

// Settled reports whether the item can no longer change (not pending,
// running or waiting). Streaming assistant text grows in place until then.
func (i TimelineItem) Settled() bool {
	switch i.Status {
	case "pending", "running", "waiting":
		return false
	}
	return true
}

type Run struct {
	RunID              string
	Ordinal            int
	Status             ThreadStatus
	ProviderInstanceID string
	Model              string // requested
	ExecutedModel      string // model the provider ran; "" if not reported
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
type ModelOption = execution.ModelOption
type OptionValue = execution.OptionValue

var StringValue = execution.StringValue
var BoolValue = execution.BoolValue

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
	Tools     []string
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

type unavailableError struct{}

func (unavailableError) Error() string     { return "t3: not connected" }
func (unavailableError) Is(err error) bool { return err == execution.ErrUnavailable }
