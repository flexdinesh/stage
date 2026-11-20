// Package client is a typed client for the stage REST API (docs/api.md).
// The CLI, the stdio MCP server and the in-server HTTP MCP endpoint all go
// through it, so the server stays the only writer and validator.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/filelock"
	"github.com/flexdinesh/stage/server/internal/listen"
)

// DefaultURL is the server address when STAGE_URL is unset.
const DefaultURL = "http://127.0.0.1:7380"

// socketBase is the request URL base for unix socket and in-process clients
// (Host "stage").
const socketBase = "http://stage"

// Resolution is the server URL a client uses and where it came from.
type Resolution struct {
	URL string
	// Source: "--url", "STAGE_URL", "default socket" or "default".
	Source string
}

// Resolve picks the server URL: flagURL (--url), else STAGE_URL, else the
// private default socket of the data dir getenv selects (STAGE_DATA_DIR or
// the installation's default) when a live server owns it (its lock is
// held), else DefaultURL. A stale socket file left by a crashed server is
// skipped, not dialed.
func Resolve(flagURL string, getenv func(string) string) Resolution {
	if flagURL != "" {
		return Resolution{URL: flagURL, Source: "--url"}
	}
	if v := getenv("STAGE_URL"); v != "" {
		return Resolution{URL: v, Source: "STAGE_URL"}
	}
	if sock, ok := liveDefaultSocket(getenv); ok {
		return Resolution{URL: "unix://" + sock, Source: "default socket"}
	}
	return Resolution{URL: DefaultURL, Source: "default"}
}

func liveDefaultSocket(getenv func(string) string) (string, bool) {
	dir, err := config.DataDirFrom(getenv)
	if err != nil {
		return "", false
	}
	sock, err := config.DefaultSocket(getenv, dir)
	if err != nil {
		return "", false
	}
	held, err := filelock.Held(listen.LockPath(sock))
	return sock, err == nil && held
}

// VersionError: the server speaks another API version than this client.
type VersionError struct {
	BaseURL string
	Server  string // the server's Stage-Api-Version
}

func (e *VersionError) Error() string {
	return fmt.Sprintf("stage server at %s speaks API v%s, this client v%d: install the CLI and server from the same release", e.BaseURL, e.Server, buildinfo.APIVersion)
}

// Client calls the REST API at BaseURL.
type Client struct {
	// BaseURL is the server address as configured (reported to agents and
	// in errors): http(s)://host:port or unix:///abs/path.sock.
	BaseURL string
	HTTP    *http.Client
	// base prefixes request paths (BaseURL, or socketBase).
	base string
	// err is an invalid BaseURL, returned by every request.
	err error
}

// New returns a client for baseURL (DefaultURL when empty): http(s)://host:port,
// or unix:///abs/path.sock (same syntax as STAGE_LISTEN) to dial a unix
// socket. An invalid socket address makes every request fail with its error.
func New(baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	c := &Client{BaseURL: baseURL, base: baseURL, HTTP: &http.Client{Timeout: 2 * time.Minute}}
	if strings.HasPrefix(baseURL, "unix:") {
		a, err := listen.Parse(baseURL)
		if err != nil {
			c.err = fmt.Errorf("stage URL: %w", err)
			return c
		}
		path := a.Address
		c.base = socketBase
		c.HTTP.Transport = &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", path)
			},
		}
	}
	return c
}

// NewInProcess returns a client whose requests h serves directly, with no
// network: the server's own HTTP MCP endpoint uses it with the /api router.
// baseURL is only reported (BaseURL).
func NewInProcess(h http.Handler, baseURL string) *Client {
	return &Client{
		BaseURL: baseURL, base: socketBase,
		HTTP: &http.Client{Timeout: 2 * time.Minute, Transport: HandlerTransport(h)},
	}
}

// HandlerTransport is an http.RoundTripper that serves each request with h
// in the calling goroutine and returns the buffered response.
func HandlerTransport(h http.Handler) http.RoundTripper { return handlerTransport{h} }

type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(req *http.Request) (res *http.Response, err error) {
	r := req.Clone(req.Context())
	if r.Body == nil {
		r.Body = http.NoBody
	}
	defer r.Body.Close()
	r.RequestURI = r.URL.RequestURI()
	if r.Host == "" {
		r.Host = r.URL.Host
	}
	rec := httptest.NewRecorder()
	defer func() {
		if p := recover(); p != nil {
			res, err = nil, fmt.Errorf("in-process %s %s: panic: %v", r.Method, r.URL.Path, p)
		}
	}()
	t.h.ServeHTTP(rec, r)
	return rec.Result(), nil
}

// Error is a non-2xx API response ({"error": msg}).
type Error struct {
	Status int
	Msg    string
}

func (e *Error) Error() string { return fmt.Sprintf("stage API %d: %s", e.Status, e.Msg) }

// IsStatus reports whether err is an API error with that status.
func IsStatus(err error, status int) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == status
}

// Task is the API Task shape.
type ArtifactLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type Task struct {
	ArtifactLinks []ArtifactLink `json:"artifactLinks"`
	db.Task
	T3WebURL *string `json:"t3WebUrl"`
	T3AppURL *string `json:"t3AppUrl"`
}

// TaskDetail is GET /api/tasks/:id.
type TaskDetail struct {
	FinalReply  *execution.Reply `json:"finalReply,omitempty"`
	Links       execution.Links  `json:"links"`
	Acceptances []db.Acceptance  `json:"acceptances"`
	Task
	Events []db.TaskEvent `json:"events"`
	Thread *Thread        `json:"thread"`
	Tree   *TreeDetail    `json:"tree"`  // the task's tree (null outside one)
	Batch  *CreatedTasks  `json:"batch"` // tasks the origin session created (null without one)
	// CurrentHandoffs: what the current launch attempt appended, one per need.
	CurrentHandoffs []db.Handoff `json:"currentHandoffs"`
	// CurrentLaunchBase: the worktree base the current launch attempt used
	// (workspace.base predecessor; null when none).
	CurrentLaunchBase *db.LaunchBase `json:"currentLaunchBase"`
}

// TreeDetail is a tree with its members, edges and levels.
type TreeDetail struct {
	db.Tree
	Tasks  []Task        `json:"tasks"`
	Edges  []db.TreeEdge `json:"edges"`
	Levels [][]int64     `json:"levels"`
}

// CreatedTasks is a session batch: tasks one session created, grouped by
// tree, then tasks outside any tree.
type CreatedTasks struct {
	Trees []struct {
		Tree  db.TreeRef `json:"tree"`
		Tasks []Task     `json:"tasks"`
	} `json:"trees"`
	Independent []Task `json:"independent"`
}

// Thread is the API Thread (session) shape.
type Thread struct {
	db.Thread
	T3WebURL *string `json:"t3WebUrl"`
	T3AppURL *string `json:"t3AppUrl"`
}

// ThreadDetail is GET /api/connections/1/sessions/:id.
type ThreadDetail struct {
	Thread Thread `json:"thread"`
	Task   *Task  `json:"task"`
}

// ScheduleRun is one fire of a schedule (detail reads include the task).
type ScheduleRun struct {
	ID         int64     `json:"id"`
	ScheduleID int64     `json:"scheduleId"`
	FireAt     time.Time `json:"fireAt"`
	Outcome    string    `json:"outcome"`
	TaskID     *int64    `json:"taskId"`
	Detail     string    `json:"detail"`
	At         time.Time `json:"at"`
	Task       *Task     `json:"task"`
}

// ScheduleDetail is GET /api/schedules/:id.
type ScheduleDetail struct {
	db.Schedule
	Runs []ScheduleRun `json:"runs"`
}

// CatalogOption is a model option and its values.
type CatalogOption struct {
	ID      string                  `json:"id"`
	Values  []execution.OptionValue `json:"values"`
	Default *execution.OptionValue  `json:"default"`
}

// CatalogModel is a provider model.
type CatalogModel struct {
	ID      string          `json:"id"`
	Label   string          `json:"label"`
	Options []CatalogOption `json:"options"`
}

// CatalogProvider is a T3 provider instance.
type CatalogProvider struct {
	ProviderInstanceID string         `json:"providerInstanceId"`
	DriverKind         string         `json:"driverKind"`
	DisplayName        string         `json:"displayName"`
	Models             []CatalogModel `json:"models"`
	RuntimeModes       []string       `json:"runtimeModes"`
}

// Catalog is GET /api/catalog.
type Catalog struct {
	Providers []CatalogProvider `json:"providers"`
	FetchedAt *time.Time        `json:"fetchedAt"`
}

// CreateTask is the POST /api/tasks body (nil = omitted).
type CreateTask struct {
	NeedsSession       *db.SessionRef          `json:"needsSession,omitempty"`
	ConnectionID       int64                   `json:"connectionId,omitempty"`
	OriginSession      *db.SessionRef          `json:"originSession,omitempty"`
	ExecutionConfig    *execution.Config       `json:"executionConfig,omitempty"`
	ProjectID          int64                   `json:"projectId"`
	Title              string                  `json:"title"`
	Prompt             string                  `json:"prompt"`
	ProviderInstanceID *string                 `json:"providerInstanceId,omitempty"`
	Model              *string                 `json:"model,omitempty"`
	ModelOptions       []execution.ModelOption `json:"modelOptions,omitempty"`
	RuntimeMode        *string                 `json:"runtimeMode,omitempty"`
	InteractionMode    *string                 `json:"interactionMode,omitempty"`
	Workspace          *db.Workspace           `json:"workspace,omitempty"`
	Tags               []string                `json:"tags,omitempty"`
	InitialNote        *string                 `json:"initialNote,omitempty"`
	TreeID             *int64                  `json:"treeId,omitempty"`
	Needs              []int64                 `json:"needs,omitempty"`
	NeedsSessionID     *string                 `json:"needsSessionId,omitempty"`
	StartMode          *db.StartMode           `json:"startMode,omitempty"`
	Handoff            *bool                   `json:"handoff,omitempty"`
	OriginThreadID     *string                 `json:"originThreadId,omitempty"`
	AttachmentIDs      []int64                 `json:"attachmentIds,omitempty"`
	StartLocal         *string                 `json:"startLocal,omitempty"`
	StartTz            *string                 `json:"startTz,omitempty"`
	Start              bool                    `json:"start,omitempty"`
}

// CreateSchedule is the POST /api/schedules body.
type CreateSchedule struct {
	ConnectionID       int64                   `json:"connectionId,omitempty"`
	OriginSession      *db.SessionRef          `json:"originSession,omitempty"`
	ExecutionConfig    *execution.Config       `json:"executionConfig,omitempty"`
	ProjectID          int64                   `json:"projectId"`
	Title              string                  `json:"title"`
	Prompt             string                  `json:"prompt"`
	ProviderInstanceID *string                 `json:"providerInstanceId,omitempty"`
	Model              *string                 `json:"model,omitempty"`
	ModelOptions       []execution.ModelOption `json:"modelOptions,omitempty"`
	RuntimeMode        *string                 `json:"runtimeMode,omitempty"`
	InteractionMode    *string                 `json:"interactionMode,omitempty"`
	Workspace          *db.Workspace           `json:"workspace,omitempty"`
	Tags               []string                `json:"tags,omitempty"`
	Notes              *string                 `json:"notes,omitempty"`
	OriginThreadID     *string                 `json:"originThreadId,omitempty"`
	AttachmentIDs      []int64                 `json:"attachmentIds,omitempty"`
	Cron               string                  `json:"cron"`
	Tz                 string                  `json:"tz"`
	Paused             bool                    `json:"paused,omitempty"`
}

// Patch is a partial update body: absent keys are unchanged, nil values
// clear nullable fields (PATCH semantics in docs/api.md).
type Patch map[string]any

// Followup is the POST /api/tasks/:id/followup body.
type TaskQuery struct {
	OriginSession  *db.SessionRef
	OriginThreadID string
	ProjectID      *int64
	States         []string
	Tag            string
	Q              string
	// Closed: "" (open only), "true" (archived or in a done/archived tree)
	// or "all".
	Closed string
}

// ThreadQuery filters GET /api/threads.
func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	_, err := c.doStatus(ctx, method, path, body, out)
	return err
}

// doStatus is do returning the 2xx status code.
func (c *Client) doStatus(ctx context.Context, method, path string, body any, out any) (int, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.send(req, out)
}

func (c *Client) send(req *http.Request, out any) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return 0, fmt.Errorf("stage unreachable at %s (set STAGE_URL, or start the local service: `stage service start`): %w", c.BaseURL, err)
	}
	defer res.Body.Close()
	if err := c.checkVersion(res); err != nil {
		return 0, err
	}
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return 0, err
	}
	if res.StatusCode >= 400 {
		var eb struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &eb) != nil || eb.Error == "" {
			eb.Error = strings.TrimSpace(string(data))
		}
		return 0, &Error{Status: res.StatusCode, Msg: eb.Error}
	}
	if out == nil || len(data) == 0 {
		return res.StatusCode, nil
	}
	return res.StatusCode, json.Unmarshal(data, out)
}

// checkVersion refuses a response from a server on another API version.
// A response without the header (a server predating it) is accepted.
func (c *Client) checkVersion(res *http.Response) error {
	if v := res.Header.Get(buildinfo.APIVersionHeader); v != "" && v != strconv.Itoa(buildinfo.APIVersion) {
		return &VersionError{BaseURL: c.BaseURL, Server: v}
	}
	return nil
}

// Health is GET /api/health (docs/api.md).
type Health struct {
	Status     string     `json:"status"`
	DB         string     `json:"db"`
	Service    string     `json:"service"`
	Identity   string     `json:"identity"`
	Version    string     `json:"version"`
	Commit     string     `json:"commit"`
	APIVersion int        `json:"apiVersion"`
	InstanceID string     `json:"instanceId"`
	StartedAt  *time.Time `json:"startedAt"`
	Schema     int64      `json:"schema"`
	// Harnesses is each connected harness's last known state.
	Harnesses []struct {
		Kind         string  `json:"kind"`
		ConnectionID int64   `json:"connectionId"`
		Configured   bool    `json:"configured"`
		Connected    bool    `json:"connected"`
		LastError    *string `json:"lastError"`
	} `json:"harnesses"`
}

// Health fetches GET /api/health. A 503 (database down) still returns the
// decoded body, with the *Error. A response that is not a Stage server's
// health is an error. It does not apply the API version check, so callers
// can report a mismatch.
func (c *Client) Health(ctx context.Context) (Health, error) {
	var h Health
	if c.err != nil {
		return h, c.err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+"/api/health", nil)
	if err != nil {
		return h, err
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return h, fmt.Errorf("stage unreachable at %s: %w", c.BaseURL, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return h, err
	}
	if json.Unmarshal(data, &h) != nil || (h.Service != buildinfo.Service && h.Status == "") {
		return h, fmt.Errorf("%s did not answer as a stage server (HTTP %d)", c.BaseURL, res.StatusCode)
	}
	if res.StatusCode != http.StatusOK {
		return h, &Error{Status: res.StatusCode, Msg: "database " + h.DB}
	}
	return h, nil
}

func setID(q url.Values, key string, id *int64) {
	if id != nil {
		q.Set(key, strconv.FormatInt(*id, 10))
	}
}

func withQuery(path string, q url.Values) string {
	if len(q) == 0 {
		return path
	}
	return path + "?" + q.Encode()
}

// Projects lists active projects.
func (c *Client) Projects(ctx context.Context) ([]db.Project, error) {
	var out struct {
		Projects []db.Project `json:"projects"`
	}
	err := c.do(ctx, http.MethodGet, "/api/projects", nil, &out)
	return out.Projects, err
}

// Catalog lists providers and models.
func (c *Client) Catalog(ctx context.Context) (Catalog, error) {
	var out Catalog
	err := c.do(ctx, http.MethodGet, "/api/catalog", nil, &out)
	return out, err
}

// Tasks lists tasks (updatedAt desc).
func (c *Client) Tasks(ctx context.Context, f TaskQuery) ([]Task, error) {
	q := url.Values{}
	q.Set("originThreadId", f.OriginThreadID)
	if f.OriginSession != nil {
		q.Set("originConnectionId", strconv.FormatInt(f.OriginSession.ConnectionID, 10))
		q.Set("originSessionId", f.OriginSession.ExternalSessionID)
	}
	setID(q, "project", f.ProjectID)
	if len(f.States) > 0 {
		q.Set("state", strings.Join(f.States, ","))
	}
	if f.Tag != "" {
		q.Set("tag", f.Tag)
	}
	if f.Q != "" {
		q.Set("q", f.Q)
	}
	if f.Closed != "" {
		q.Set("closed", f.Closed)
	}
	var out struct {
		Tasks []Task `json:"tasks"`
	}
	err := c.do(ctx, http.MethodGet, withQuery("/api/tasks", q), nil, &out)
	return out.Tasks, err
}

func taskPath(id int64, action string) string {
	p := "/api/tasks/" + strconv.FormatInt(id, 10)
	if action != "" {
		p += "/" + action
	}
	return p
}

// Task reads one task with events, thread and tree.
func (c *Client) Task(ctx context.Context, id int64) (TaskDetail, error) {
	var out TaskDetail
	err := c.do(ctx, http.MethodGet, taskPath(id, ""), nil, &out)
	return out, err
}

// CreateTask creates a task (backlog, or launching when Start).
func (c *Client) CreateTask(ctx context.Context, in CreateTask) (Task, error) {
	var out Task
	err := c.do(ctx, http.MethodPost, "/api/tasks", in, &out)
	return out, err
}

// UpdateTask patches a task.
func (c *Client) UpdateTask(ctx context.Context, id int64, p Patch) (Task, error) {
	var out Task
	err := c.do(ctx, http.MethodPatch, taskPath(id, ""), p, &out)
	return out, err
}

// DeleteTask deletes an unstarted task (409 once started: archive it).
func (c *Client) DeleteTask(ctx context.Context, id int64) error {
	return c.do(ctx, http.MethodDelete, taskPath(id, ""), nil, nil)
}

// ArchiveTask archives (archived) or unarchives a task.
func (c *Client) ArchiveTask(ctx context.Context, id int64, archived bool) (Task, error) {
	action := "unarchive"
	if archived {
		action = "archive"
	}
	var out Task
	err := c.do(ctx, http.MethodPost, taskPath(id, action), nil, &out)
	return out, err
}

// StartTask launches a task in T3.
func (c *Client) StartTask(ctx context.Context, id int64) (Task, error) {
	var out Task
	err := c.do(ctx, http.MethodPost, taskPath(id, "start"), nil, &out)
	return out, err
}

// StopTask interrupts a task's thread.
func (c *Client) StopTask(ctx context.Context, id int64) (Task, error) {
	var out Task
	err := c.do(ctx, http.MethodPost, taskPath(id, "stop"), nil, &out)
	return out, err
}

// FollowupTask sends a message (and/or staged images) to a task's thread.
func (c *Client) DuplicateTask(ctx context.Context, id int64) (Task, error) {
	var out Task
	err := c.do(ctx, http.MethodPost, taskPath(id, "duplicate"), nil, &out)
	return out, err
}

// SetNeeds replaces a task's needs (PUT /api/tasks/:id/needs); empty clears
// them.
func (c *Client) SetNeeds(ctx context.Context, id int64, needs []int64) (Task, error) {
	if needs == nil {
		needs = []int64{}
	}
	var out Task
	err := c.do(ctx, http.MethodPut, taskPath(id, "needs"), map[string][]int64{"needs": needs}, &out)
	return out, err
}

// TreeQuery filters GET /api/trees: Status active (default; plus open
// trees settled since SettledFrom) | done | archived | all; All is
// Status all.
type TreeQuery struct {
	All         bool
	Status      string
	ProjectID   *int64
	SettledFrom *time.Time
}

// NewTree is the POST /api/trees body.
type NewTree struct {
	Title     string          `json:"title"`
	StartMode *db.StartMode   `json:"startMode,omitempty"`
	Tasks     []db.TreeMember `json:"tasks"`
}

// TreeStart is POST /api/trees/:id/start: the members started and the ones
// that failed to start.
type TreeStart struct {
	Started []Task `json:"started"`
	Errors  []struct {
		TaskID int64  `json:"taskId"`
		Error  string `json:"error"`
	} `json:"errors"`
}

// AdoptSession is the POST /api/connections/1/sessions/:id/adopt body.
type AdoptSession struct {
	CallerSession   *db.SessionRef `json:"callerSession,omitempty"`
	ProjectID       *int64         `json:"projectId,omitempty"`
	NewProjectTitle string         `json:"newProjectTitle,omitempty"`
	Title           *string        `json:"title,omitempty"`
	InitialNote     *string        `json:"initialNote,omitempty"`
	Tags            []string       `json:"tags,omitempty"`
	TreeID          *int64         `json:"treeId,omitempty"`
}

func treePath(id int64, action string) string {
	p := "/api/trees/" + strconv.FormatInt(id, 10)
	if action != "" {
		p += "/" + action
	}
	return p
}

// Trees lists trees with their member roll-up.
func (c *Client) Trees(ctx context.Context, f TreeQuery) ([]db.TreeSummary, error) {
	q := url.Values{}
	if f.All {
		q.Set("all", "1")
	}
	if f.Status != "" {
		q.Set("status", f.Status)
	}
	setID(q, "project", f.ProjectID)
	if f.SettledFrom != nil {
		q.Set("settledFrom", f.SettledFrom.UTC().Format(time.RFC3339))
	}
	var out struct {
		Trees []db.TreeSummary `json:"trees"`
	}
	err := c.do(ctx, http.MethodGet, withQuery("/api/trees", q), nil, &out)
	return out.Trees, err
}

// Tree reads one tree with its members, edges and levels.
func (c *Client) Tree(ctx context.Context, id int64) (TreeDetail, error) {
	var out TreeDetail
	err := c.do(ctx, http.MethodGet, treePath(id, ""), nil, &out)
	return out, err
}

// CreateTree creates a tree from existing tasks.
func (c *Client) CreateTree(ctx context.Context, in NewTree) (TreeDetail, error) {
	if in.Tasks == nil {
		in.Tasks = []db.TreeMember{}
	}
	var out TreeDetail
	err := c.do(ctx, http.MethodPost, "/api/trees", in, &out)
	return out, err
}

// UpdateTree patches a tree (title, startMode, status).
func (c *Client) UpdateTree(ctx context.Context, id int64, p Patch) (TreeDetail, error) {
	var out TreeDetail
	err := c.do(ctx, http.MethodPatch, treePath(id, ""), p, &out)
	return out, err
}

// DeleteTree deletes an unstarted tree and its tasks (409 once a member
// started: archive it).
func (c *Client) DeleteTree(ctx context.Context, id int64) error {
	return c.do(ctx, http.MethodDelete, treePath(id, ""), nil, nil)
}

// DissolveTree removes a tree; its tasks stay, their needs go.
func (c *Client) DissolveTree(ctx context.Context, id int64) error {
	return c.do(ctx, http.MethodPost, treePath(id, "dissolve"), nil, nil)
}

// AddToTree adds task taskID to tree id; needs nil keeps the task's needs.
func (c *Client) AddToTree(ctx context.Context, id, taskID int64, needs *[]int64) (TreeDetail, error) {
	body := struct {
		TaskID int64    `json:"taskId"`
		Needs  *[]int64 `json:"needs,omitempty"`
	}{taskID, needs}
	var out TreeDetail
	err := c.do(ctx, http.MethodPost, treePath(id, "tasks"), body, &out)
	return out, err
}

// RemoveFromTree splices task taskID out of tree id (its dependents take
// its needs).
func (c *Client) RemoveFromTree(ctx context.Context, id, taskID int64) error {
	return c.do(ctx, http.MethodDelete, treePath(id, "tasks/"+strconv.FormatInt(taskID, 10)), nil, nil)
}

// StartTree starts every ready member of tree id.
func (c *Client) StartTree(ctx context.Context, id int64) (TreeStart, error) {
	var out TreeStart
	err := c.do(ctx, http.MethodPost, treePath(id, "start"), nil, &out)
	return out, err
}

// AdoptThread tracks session threadID as a task; created is false when a
// task already owned it.
func (c *Client) AdoptThread(ctx context.Context, threadID string, in AdoptSession) (Task, bool, error) {
	t3, err := c.T3ConnectionID(ctx)
	if err != nil {
		return Task{}, false, err
	}
	return c.AdoptScoped(ctx, db.SessionRef{ConnectionID: t3, ExternalSessionID: threadID}, in)
}

// T3ConnectionID is the server's active T3 connection: legacy T3 thread
// ids belong to it.
func (c *Client) T3ConnectionID(ctx context.Context) (int64, error) {
	cs, err := c.Connections(ctx)
	if err != nil {
		return 0, err
	}
	for _, conn := range cs {
		if conn.Harness == db.HarnessT3 && conn.Active {
			return conn.ID, nil
		}
	}
	return 0, errors.New("no T3 connection on this Stage server")
}
func sessionPath(ref db.SessionRef) string {
	return "/api/connections/" + strconv.FormatInt(ref.ConnectionID, 10) + "/sessions/" + url.PathEscape(ref.ExternalSessionID)
}
func (c *Client) AdoptScoped(ctx context.Context, ref db.SessionRef, in AdoptSession) (Task, bool, error) {
	var out Task
	status, err := c.doStatus(ctx, http.MethodPost, sessionPath(ref)+"/adopt", in, &out)
	return out, status == http.StatusCreated, err
}

func schedulePath(id int64, action string) string {
	p := "/api/schedules/" + strconv.FormatInt(id, 10)
	if action != "" {
		p += "/" + action
	}
	return p
}

// Schedules lists schedules (next fire first).
func (c *Client) Schedules(ctx context.Context, projectID *int64) ([]db.Schedule, error) {
	q := url.Values{}
	setID(q, "project", projectID)
	var out struct {
		Schedules []db.Schedule `json:"schedules"`
	}
	err := c.do(ctx, http.MethodGet, withQuery("/api/schedules", q), nil, &out)
	return out.Schedules, err
}

// Schedule reads one schedule with its recent runs.
func (c *Client) Schedule(ctx context.Context, id int64) (ScheduleDetail, error) {
	var out ScheduleDetail
	err := c.do(ctx, http.MethodGet, schedulePath(id, ""), nil, &out)
	return out, err
}

// CreateSchedule creates a recurring schedule.
func (c *Client) CreateSchedule(ctx context.Context, in CreateSchedule) (db.Schedule, error) {
	var out db.Schedule
	err := c.do(ctx, http.MethodPost, "/api/schedules", in, &out)
	return out, err
}

// UpdateSchedule patches a schedule (incl. paused).
func (c *Client) UpdateSchedule(ctx context.Context, id int64, p Patch) (db.Schedule, error) {
	var out db.Schedule
	err := c.do(ctx, http.MethodPatch, schedulePath(id, ""), p, &out)
	return out, err
}

// DeleteSchedule deletes a schedule (spawned tasks stay).
func (c *Client) DeleteSchedule(ctx context.Context, id int64) error {
	return c.do(ctx, http.MethodDelete, schedulePath(id, ""), nil, nil)
}

// RunSchedule spawns and starts a task now.
func (c *Client) RunSchedule(ctx context.Context, id int64) (Task, error) {
	var out Task
	err := c.do(ctx, http.MethodPost, schedulePath(id, "run"), nil, &out)
	return out, err
}

// PreviewSchedule returns the next three fires of cron in tz.
func (c *Client) PreviewSchedule(ctx context.Context, cron, tz string) ([]time.Time, error) {
	var out struct {
		Next []time.Time `json:"next"`
	}
	err := c.do(ctx, http.MethodPost, "/api/schedules/preview", map[string]string{"cron": cron, "tz": tz}, &out)
	return out.Next, err
}

type SessionContext struct {
	Session db.SessionRef   `json:"session"`
	Task    *Task           `json:"task"`
	Project *db.Project     `json:"project"`
	Links   execution.Links `json:"links"`
	Title   string          `json:"title"`
}

func (c *Client) SessionContext(ctx context.Context, ref db.SessionRef) (SessionContext, error) {
	var out SessionContext
	err := c.do(ctx, http.MethodGet, sessionPath(ref)+"/context", nil, &out)
	return out, err
}
func (c *Client) Connections(ctx context.Context) ([]db.HarnessConnection, error) {
	var out struct {
		Connections []db.HarnessConnection `json:"connections"`
	}
	err := c.do(ctx, http.MethodGet, "/api/connections", nil, &out)
	return out.Connections, err
}

// SetAutoComplete is PUT /api/connections/{id}/auto-complete.
func (c *Client) SetAutoComplete(ctx context.Context, id int64, on bool) (db.HarnessConnection, error) {
	var out struct {
		Connection db.HarnessConnection `json:"connection"`
	}
	err := c.do(ctx, http.MethodPut, fmt.Sprintf("/api/connections/%d/auto-complete", id), map[string]bool{"autoComplete": on}, &out)
	return out.Connection, err
}
func (c *Client) Capabilities(ctx context.Context, id int64) (execution.Capabilities, error) {
	return c.ProjectCapabilities(ctx, id, 0)
}
func (c *Client) ProjectCapabilities(ctx context.Context, id, projectID int64) (execution.Capabilities, error) {
	var out execution.Capabilities
	path := fmt.Sprintf("/api/connections/%d/capabilities", id)
	if projectID != 0 {
		path += fmt.Sprintf("?projectId=%d", projectID)
	}
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) Thread(ctx context.Context, id string) (ThreadDetail, error) {
	var out ThreadDetail
	t3, err := c.T3ConnectionID(ctx)
	if err != nil {
		return out, err
	}
	err = c.do(ctx, http.MethodGet, "/api/connections/"+strconv.FormatInt(t3, 10)+"/sessions/"+url.PathEscape(id), nil, &out)
	return out, err
}

// Ref is GET /api/refs/resolve: what a pasted reference names.
type Ref struct {
	Links        execution.Links `json:"links"`
	SessionTitle string          `json:"sessionTitle,omitempty"`
	Session      *db.SessionRef  `json:"session"`
	Kind         string          `json:"kind"` // task | session
	TaskID       *int64          `json:"taskId"`
	ThreadID     *string         `json:"threadId"`
}

// ResolveRef reads a stage link, T3 thread link, #task id or thread id.
func (c *Client) ResolveRef(ctx context.Context, ref string) (Ref, error) {
	var out Ref
	err := c.do(ctx, http.MethodGet, withQuery("/api/refs/resolve", url.Values{"ref": {ref}}), nil, &out)
	return out, err
}

// PendingQuestions is GET /api/connections/1/sessions/:id/questions: answerable questions
// plus approvals stage cannot resolve (handled in T3).
func (c *Client) Upload(ctx context.Context, name string, data []byte) (db.Attachment, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		return db.Attachment{}, err
	}
	if _, err := fw.Write(data); err != nil {
		return db.Attachment{}, err
	}
	if err := mw.Close(); err != nil {
		return db.Attachment{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/uploads", &buf)
	if err != nil {
		return db.Attachment{}, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	var out db.Attachment
	_, err = c.send(req, &out)
	return out, err
}

// Preferences is GET /api/preferences (model defaults subset).
type Preferences struct {
	Pinned   *db.ModelDefaults `json:"pinned"`
	LastUsed *db.ModelDefaults `json:"lastUsed"`
}

// Preferences reads the pinned and last-used model defaults.
func (c *Client) Preferences(ctx context.Context) (Preferences, error) {
	var out Preferences
	err := c.do(ctx, http.MethodGet, "/api/preferences", nil, &out)
	return out, err
}
