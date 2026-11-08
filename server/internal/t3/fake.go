package t3

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// Fake is an in-memory Client for tests and local dev. Launched threads start
// "running"; drive them with SetThread.
type Fake struct {
	mu          sync.Mutex
	now         func() time.Time
	seq         int
	projects    []Project
	threads     map[string]*ThreadDetail
	order       []string          // thread ids, oldest first
	launches    map[string]string // clientRequestID -> threadID
	Catalog     Catalog
	Sent        []FakeSend
	Interrupted []string // interrupted thread ids
}

type FakeSend struct {
	ThreadID, Message string
	Mode              SendMode
}

var _ Client = (*Fake)(nil)

func NewFake() *Fake {
	return &Fake{
		now:      time.Now,
		threads:  map[string]*ThreadDetail{},
		launches: map[string]string{},
		Catalog: Catalog{Providers: []Provider{{
			ProviderInstanceID: "claude",
			DriverKind:         "claude",
			DisplayName:        "Claude",
			Models: []Model{{
				ID:    "claude-haiku-5-5",
				Label: "Haiku 5.5",
				Options: []ModelOptionSpec{{
					ID:      "effort",
					Values:  []OptionValue{StringValue("low"), StringValue("medium"), StringValue("high")},
					Default: &OptionValue{Str: "medium"},
				}},
			}},
			RuntimeModes:    []RuntimeMode{RuntimeApprovalRequired, RuntimeAutoAcceptEdits, RuntimeAuto, RuntimeFullAccess},
			CanRunChildTask: true,
		}}},
	}
}

func (f *Fake) nextID(prefix string) string {
	f.seq++
	return fmt.Sprintf("%s-%d", prefix, f.seq)
}

func (f *Fake) Status(context.Context) ConnectionStatus {
	exp := f.now().Add(30 * 24 * time.Hour)
	return ConnectionStatus{Connected: true, ClientID: "fake", ExpiresAt: &exp, EnvironmentID: "fake-env"}
}

func (f *Fake) Pair(_ context.Context, code string) error {
	if strings.TrimSpace(code) == "" {
		return errors.New("empty pairing code")
	}
	return nil
}

func (f *Fake) ListProjects(context.Context) ([]Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.projects), nil
}

func (f *Fake) CreateProject(_ context.Context, in CreateProjectInput) (Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.projects {
		if p.WorkspaceRoot == in.WorkspaceRoot {
			return Project{}, fmt.Errorf("workspace root already registered: %s", in.WorkspaceRoot)
		}
	}
	now := f.now()
	p := Project{ID: f.nextID("project"), Title: in.Title, WorkspaceRoot: in.WorkspaceRoot, CreatedAt: now, UpdatedAt: now}
	f.projects = append(f.projects, p)
	return p, nil
}

func (f *Fake) ListThreads(_ context.Context, in ListThreadsInput) (ThreadPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var all []Thread
	for i := len(f.order) - 1; i >= 0; i-- { // newest first
		t := f.threads[f.order[i]].Thread
		if !in.AllProjects && t.ProjectID != in.ProjectID {
			continue
		}
		if len(in.Statuses) > 0 && !slices.Contains(in.Statuses, t.Status) {
			continue
		}
		if in.Settled != nil && t.Settled != *in.Settled {
			continue
		}
		if in.TitleContains != "" && !strings.Contains(strings.ToLower(t.Title), strings.ToLower(in.TitleContains)) {
			continue
		}
		all = append(all, t)
	}
	limit := in.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	start := 0
	if in.Cursor != nil {
		start = min(*in.Cursor, len(all))
	}
	end := min(start+limit, len(all))
	page := ThreadPage{Threads: slices.Clone(all[start:end]), Total: len(all)}
	if end < len(all) {
		page.NextCursor = &end
	}
	return page, nil
}

func (f *Fake) ReadThread(_ context.Context, threadID string) (ThreadDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.threads[threadID]
	if !ok {
		return ThreadDetail{}, ErrNotFound
	}
	out := *t
	out.RecentRuns = slices.Clone(t.RecentRuns)
	return out, nil
}

func (f *Fake) LaunchThread(_ context.Context, in LaunchInput) (LaunchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if in.ProjectID == "" || strings.TrimSpace(in.Title) == "" {
		return LaunchResult{}, errors.New("projectId and title are required")
	}
	if id, ok := f.launches[in.ClientRequestID]; ok && in.ClientRequestID != "" {
		return launchResult(f.threads[id]), nil
	}
	now := f.now()
	sel := ModelSelection{InstanceID: "claude", Model: "claude-haiku-5-5"}
	if in.ModelSelection != nil {
		sel = *in.ModelSelection
	}
	runtime, interaction := in.RuntimeMode, in.InteractionMode
	if runtime == "" {
		runtime = RuntimeFullAccess
	}
	if interaction == "" {
		interaction = InteractionDefault
	}
	id := f.nextID("thread")
	run := Run{RunID: f.nextID("run"), Ordinal: 1, Status: StatusRunning, ProviderInstanceID: sel.InstanceID, Model: sel.Model, RequestedAt: now, StartedAt: &now}
	d := &ThreadDetail{
		Thread: Thread{
			ThreadID: id, ProjectID: in.ProjectID, Title: in.Title, Link: "t3-thread://fake-env/" + id,
			CreatedBy: "user", CreationSource: "mcp", Status: StatusRunning, LatestRunID: run.RunID,
			ProviderInstanceID: sel.InstanceID, Model: sel.Model, RuntimeMode: runtime, InteractionMode: interaction,
			ItemCount: 1, CreatedAt: now, UpdatedAt: now,
		},
		ActiveRunID: run.RunID,
		RunCount:    1,
		RecentRuns:  []Run{run},
	}
	if in.Workspace != nil {
		d.Branch = in.Workspace.Branch
		d.WorktreePath = in.Workspace.WorktreePath
	}
	f.threads[id] = d
	f.order = append(f.order, id)
	if in.ClientRequestID != "" {
		f.launches[in.ClientRequestID] = id
	}
	return launchResult(d), nil
}

func launchResult(d *ThreadDetail) LaunchResult {
	return LaunchResult{
		ThreadID:       d.ThreadID,
		Link:           d.Link,
		ProjectID:      d.ProjectID,
		ModelSelection: ModelSelection{InstanceID: d.ProviderInstanceID, Model: d.Model},
		RunID:          d.LatestRunID,
		Status:         d.Status,
	}
}

func (f *Fake) SendMessage(_ context.Context, threadID, message string, mode SendMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.threads[threadID]; !ok {
		return ErrNotFound
	}
	f.Sent = append(f.Sent, FakeSend{ThreadID: threadID, Message: message, Mode: mode})
	return nil
}

func (f *Fake) Interrupt(_ context.Context, threadID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.threads[threadID]
	if !ok {
		return ErrNotFound
	}
	f.Interrupted = append(f.Interrupted, threadID)
	if t.ActiveRunID != "" {
		f.setStatus(t, StatusInterrupted)
	}
	return nil
}

func (f *Fake) Capabilities(context.Context) (Catalog, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Catalog, nil
}

// AddProject seeds a T3-side project (as if created in T3 directly).
func (f *Fake) AddProject(title, workspaceRoot string) Project {
	p, _ := f.CreateProject(context.Background(), CreateProjectInput{Title: title, WorkspaceRoot: workspaceRoot})
	return p
}

// SetThread mutates a thread in place (status, settled, pending requests...).
func (f *Fake) SetThread(threadID string, mutate func(*ThreadDetail)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.threads[threadID]
	if !ok {
		return ErrNotFound
	}
	mutate(t)
	t.UpdatedAt = f.now()
	return nil
}

// SetStatus sets thread + latest run status; terminal statuses end the active run.
func (f *Fake) SetStatus(threadID string, status ThreadStatus) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.threads[threadID]
	if !ok {
		return ErrNotFound
	}
	f.setStatus(t, status)
	return nil
}

// Settle marks a thread settled (done in T3).
func (f *Fake) Settle(threadID string) error {
	now := f.now()
	return f.SetThread(threadID, func(t *ThreadDetail) {
		t.Settled = true
		t.SettledAt = &now
	})
}

func (f *Fake) setStatus(t *ThreadDetail, status ThreadStatus) {
	now := f.now()
	t.Status = status
	t.UpdatedAt = now
	if len(t.RecentRuns) > 0 {
		t.RecentRuns[0].Status = status
	}
	switch status {
	case StatusCompleted, StatusInterrupted, StatusFailed, StatusCancelled, StatusRolledBack:
		t.ActiveRunID = ""
		if len(t.RecentRuns) > 0 {
			t.RecentRuns[0].CompletedAt = &now
		}
	}
}
