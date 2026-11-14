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
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// DefaultURL is the server address when STAGE_URL is unset.
const DefaultURL = "http://127.0.0.1:7380"

// Client calls the REST API at BaseURL.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// New returns a client for baseURL (DefaultURL when empty).
func New(baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultURL
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: 2 * time.Minute}}
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
type Task struct {
	db.Task
	T3WebURL *string `json:"t3WebUrl"`
	T3AppURL *string `json:"t3AppUrl"`
}

// TaskDetail is GET /api/tasks/:id.
type TaskDetail struct {
	Task
	Events   []db.TaskEvent `json:"events"`
	Thread   *Thread        `json:"thread"`
	Sequence []Task         `json:"sequence"`
	Batch    *CreatedTasks  `json:"batch"` // tasks the origin session created (null without one)
	// CurrentHandoff: what the current launch attempt appended (null when none).
	CurrentHandoff *db.Handoff `json:"currentHandoff"`
	// CurrentLaunchBase: the worktree base the current launch attempt used
	// (workspace.base predecessor; null when none).
	CurrentLaunchBase *db.LaunchBase `json:"currentLaunchBase"`
}

// CreatedTasks is a session batch: tasks one session created, grouped by run
// order (chains head first, then independent tasks).
type CreatedTasks struct {
	Chains      [][]Task `json:"chains"`
	Independent []Task   `json:"independent"`
}

// Thread is the API Thread (session) shape.
type Thread struct {
	db.Thread
	T3WebURL *string `json:"t3WebUrl"`
	T3AppURL *string `json:"t3AppUrl"`
}

// ThreadDetail is GET /api/threads/:id.
type ThreadDetail struct {
	Thread        Thread            `json:"thread"`
	Children      []Thread          `json:"children"`
	Continuations []json.RawMessage `json:"continuations"`
	Task          *Task             `json:"task"`
	CreatedTasks  CreatedTasks      `json:"createdTasks"` // tasks this session created
}

// ThreadPage is GET /api/threads.
type ThreadPage struct {
	Threads []Thread `json:"threads"`
	Total   int      `json:"total"`
}

// ThreadSearch is GET /api/threads/search.
type ThreadSearch struct {
	Results []SearchResult `json:"results"`
	T3      string         `json:"t3"` // ok|offline|timeout|error
}

// SearchResult is one session match. MatchSource: title|branch|project|pr|user|assistant.
type SearchResult struct {
	T3ThreadID       string     `json:"t3ThreadId"`
	Title            string     `json:"title"`
	ProjectTitle     *string    `json:"projectTitle"`
	MatchSource      string     `json:"matchSource"`
	Snippet          *string    `json:"snippet"`
	MessageCreatedAt *time.Time `json:"messageCreatedAt"`
	Thread           *Thread    `json:"thread"`
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
	ID      string           `json:"id"`
	Values  []t3.OptionValue `json:"values"`
	Default *t3.OptionValue  `json:"default"`
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
	ProjectID          int64            `json:"projectId"`
	Title              string           `json:"title"`
	Prompt             string           `json:"prompt"`
	ProviderInstanceID *string          `json:"providerInstanceId,omitempty"`
	Model              *string          `json:"model,omitempty"`
	ModelOptions       []t3.ModelOption `json:"modelOptions,omitempty"`
	RuntimeMode        *string          `json:"runtimeMode,omitempty"`
	InteractionMode    *string          `json:"interactionMode,omitempty"`
	Workspace          *db.Workspace    `json:"workspace,omitempty"`
	Tags               []string         `json:"tags,omitempty"`
	Notes              *string          `json:"notes,omitempty"`
	AfterTaskID        *int64           `json:"afterTaskId,omitempty"`
	AfterThreadID      *string          `json:"afterThreadId,omitempty"`
	Handoff            *bool            `json:"handoff,omitempty"`
	OriginThreadID     *string          `json:"originThreadId,omitempty"`
	AttachmentIDs      []int64          `json:"attachmentIds,omitempty"`
	StartLocal         *string          `json:"startLocal,omitempty"`
	StartTz            *string          `json:"startTz,omitempty"`
	Start              bool             `json:"start,omitempty"`
}

// CreateSchedule is the POST /api/schedules body.
type CreateSchedule struct {
	ProjectID          int64            `json:"projectId"`
	Title              string           `json:"title"`
	Prompt             string           `json:"prompt"`
	ProviderInstanceID *string          `json:"providerInstanceId,omitempty"`
	Model              *string          `json:"model,omitempty"`
	ModelOptions       []t3.ModelOption `json:"modelOptions,omitempty"`
	RuntimeMode        *string          `json:"runtimeMode,omitempty"`
	InteractionMode    *string          `json:"interactionMode,omitempty"`
	Workspace          *db.Workspace    `json:"workspace,omitempty"`
	Tags               []string         `json:"tags,omitempty"`
	Notes              *string          `json:"notes,omitempty"`
	OriginThreadID     *string          `json:"originThreadId,omitempty"`
	AttachmentIDs      []int64          `json:"attachmentIds,omitempty"`
	Cron               string           `json:"cron"`
	Tz                 string           `json:"tz"`
	Paused             bool             `json:"paused,omitempty"`
}

// Patch is a partial update body: absent keys are unchanged, nil values
// clear nullable fields (PATCH semantics in docs/api.md).
type Patch map[string]any

// Followup is the POST /api/tasks/:id/followup body.
type Followup struct {
	Message       string  `json:"message"`
	Mode          *string `json:"mode,omitempty"`
	AttachmentIDs []int64 `json:"attachmentIds,omitempty"`
}

// TaskQuery filters GET /api/tasks.
type TaskQuery struct {
	ProjectID *int64
	States    []string
	Tag       string
	Q         string
}

// ThreadQuery filters GET /api/threads.
type ThreadQuery struct {
	ProjectID        *int64
	States           []string
	Active           bool
	Q                string
	Parent           string
	ExcludeSubagents bool
	Limit            int
	Offset           int
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.send(req, out)
}

func (c *Client) send(req *http.Request, out any) error {
	res, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("stage unreachable at %s (set STAGE_URL): %w", c.BaseURL, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode >= 400 {
		var eb struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &eb) != nil || eb.Error == "" {
			eb.Error = strings.TrimSpace(string(data))
		}
		return &Error{Status: res.StatusCode, Msg: eb.Error}
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
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

// Task reads one task with events, thread and run order.
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

// DeleteTask deletes a task.
func (c *Client) DeleteTask(ctx context.Context, id int64) error {
	return c.do(ctx, http.MethodDelete, taskPath(id, ""), nil, nil)
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
func (c *Client) FollowupTask(ctx context.Context, id int64, in Followup) (Task, error) {
	var out Task
	err := c.do(ctx, http.MethodPost, taskPath(id, "followup"), in, &out)
	return out, err
}

// DuplicateTask copies a task into a new backlog task.
func (c *Client) DuplicateTask(ctx context.Context, id int64) (Task, error) {
	var out Task
	err := c.do(ctx, http.MethodPost, taskPath(id, "duplicate"), nil, &out)
	return out, err
}

// OrderTasks links tasks to run in the given order (POST /api/tasks/order)
// and returns the resulting run order, head first.
func (c *Client) OrderTasks(ctx context.Context, ids []int64) ([]Task, error) {
	var out struct {
		Tasks []Task `json:"tasks"`
	}
	err := c.do(ctx, http.MethodPost, "/api/tasks/order", map[string]any{"taskIds": ids}, &out)
	return out.Tasks, err
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

// Threads lists sessions.
func (c *Client) Threads(ctx context.Context, f ThreadQuery) (ThreadPage, error) {
	q := url.Values{}
	setID(q, "project", f.ProjectID)
	if len(f.States) > 0 {
		q.Set("state", strings.Join(f.States, ","))
	}
	if f.Active {
		q.Set("active", "1")
		q.Set("sort", "activity")
	}
	if f.Q != "" {
		q.Set("q", f.Q)
	}
	if f.Parent != "" {
		q.Set("parent", f.Parent)
	}
	if f.ExcludeSubagents {
		q.Set("subagents", "exclude")
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	if f.Offset > 0 {
		q.Set("offset", strconv.Itoa(f.Offset))
	}
	var out ThreadPage
	err := c.do(ctx, http.MethodGet, withQuery("/api/threads", q), nil, &out)
	return out, err
}

// SearchThreads finds sessions by title, then by what was said in them (T3).
func (c *Client) SearchThreads(ctx context.Context, query string, projectID *int64, limit int) (ThreadSearch, error) {
	q := url.Values{}
	q.Set("q", query)
	setID(q, "project", projectID)
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out ThreadSearch
	err := c.do(ctx, http.MethodGet, withQuery("/api/threads/search", q), nil, &out)
	return out, err
}

// Thread reads one session (refreshed from T3 when connected).
func (c *Client) Thread(ctx context.Context, id string) (ThreadDetail, error) {
	var out ThreadDetail
	err := c.do(ctx, http.MethodGet, "/api/threads/"+url.PathEscape(id), nil, &out)
	return out, err
}

// PendingQuestions is GET /api/threads/:id/questions: answerable questions
// plus approvals stage cannot resolve (handled in T3).
type PendingQuestions struct {
	PendingRequestCount int                  `json:"pendingRequestCount"`
	ApprovalCount       int                  `json:"approvalCount"`
	Requests            []t3.PendingQuestion `json:"requests"`
}

// Questions reads a session's pending questions fresh from T3.
func (c *Client) Questions(ctx context.Context, threadID string) (PendingQuestions, error) {
	var out PendingQuestions
	err := c.do(ctx, http.MethodGet, "/api/threads/"+url.PathEscape(threadID)+"/questions", nil, &out)
	return out, err
}

// Upload stages one image (POST /api/uploads).
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/uploads", &buf)
	if err != nil {
		return db.Attachment{}, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	var out db.Attachment
	err = c.send(req, &out)
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
