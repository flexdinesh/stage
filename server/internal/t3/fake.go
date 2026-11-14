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
	order       []string           // thread ids, oldest first
	replies     map[string][]Reply // thread id -> assistant replies, oldest first
	launches    map[string]string  // clientRequestID -> threadID
	Catalog     Catalog
	Sent        []FakeSend
	Interrupted []string // interrupted thread ids
	// Uploads: every UploadAttachment, in order. Discarded: discarded ids.
	Uploads   []FakeUpload
	Discarded []string
	// Launched: every LaunchThread input that created or deduplicated a thread.
	Launched []LaunchInput
	// SentAttachments: every SendAttachments call.
	SentAttachments []FakeSendAttachments
	// LaunchErr, when set, fails LaunchThread (tests).
	LaunchErr error
	SendErr   error
	// SearchErr fails SearchThreads; SearchDelay holds it (until ctx ends).
	SearchErr   error
	SearchDelay time.Duration
	// Searches: every SearchThreads input.
	Searches []SearchThreadsInput
	messages []fakeMessage
}

type fakeMessage struct {
	threadID, text string
	source         SearchSource
	at             time.Time
}

type FakeUpload struct {
	Attachment
	Data []byte
}

type FakeSendAttachments struct {
	ThreadID, Message string
	Attachments       []Attachment
}

type FakeSend struct {
	ThreadID, Message string
	Mode              SendMode
	RequestID         string
}

var _ Client = (*Fake)(nil)

func NewFake() *Fake {
	return &Fake{
		now:      time.Now,
		threads:  map[string]*ThreadDetail{},
		replies:  map[string][]Reply{},
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
		if p.WorkspaceRoot == in.WorkspaceRoot && p.DeletedAt == nil {
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
		if !in.IncludeSubagents && t.ParentThreadID != "" {
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

// SearchThreads mimics t3_thread_search: unarchived root threads whose title,
// branch or project title contains the query (source title), then message
// matches (AddMessage) from other threads, newest message first.
func (f *Fake) SearchThreads(ctx context.Context, in SearchThreadsInput) ([]SearchMatch, error) {
	f.mu.Lock()
	f.Searches = append(f.Searches, in)
	delay, searchErr := f.SearchDelay, f.SearchErr
	f.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if searchErr != nil {
		return nil, searchErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	q := strings.ToLower(strings.TrimSpace(in.Query))
	limit := in.Limit
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	projectTitle := map[string]string{}
	for _, p := range f.projects {
		projectTitle[p.ID] = p.Title
	}
	eligible := func(t *ThreadDetail) bool {
		return !t.Archived && t.RelationshipToParent != "subagent" && (in.ProjectID == "" || t.ProjectID == in.ProjectID)
	}
	var out []SearchMatch
	shown := map[string]bool{}
	for i := len(f.order) - 1; i >= 0; i-- {
		t := f.threads[f.order[i]]
		if !eligible(t) {
			continue
		}
		for _, text := range []string{t.Title, t.Branch, projectTitle[t.ProjectID]} {
			if text != "" && strings.Contains(strings.ToLower(text), q) {
				out = append(out, SearchMatch{ThreadID: t.ThreadID, ProjectID: t.ProjectID, Title: t.Title, Source: SearchTitle})
				shown[t.ThreadID] = true
				break
			}
		}
	}
	for i := len(f.messages) - 1; i >= 0; i-- {
		m := f.messages[i]
		t, ok := f.threads[m.threadID]
		if !ok || shown[m.threadID] || !eligible(t) || !strings.Contains(strings.ToLower(m.text), q) {
			continue
		}
		at := m.at
		out = append(out, SearchMatch{ThreadID: t.ThreadID, ProjectID: t.ProjectID, Title: t.Title, Source: m.source, Snippet: m.text, MessageCreatedAt: &at})
		shown[m.threadID] = true
	}
	return out[:min(limit, len(out))], nil
}

// AddMessage seeds message text for SearchThreads (source user|assistant).
func (f *Fake) AddMessage(threadID string, source SearchSource, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, fakeMessage{threadID: threadID, text: text, source: source, at: f.now()})
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

// LatestReply returns the newest reply set with SetReply (of RunID if set),
// text cut to MaxChars runes.
func (f *Fake) LatestReply(_ context.Context, in LatestReplyInput) (Reply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.threads[in.ThreadID]; !ok {
		return Reply{}, ErrNotFound
	}
	replies := f.replies[in.ThreadID]
	for i := len(replies) - 1; i >= 0; i-- {
		r := replies[i]
		if in.RunID != "" && r.RunID != in.RunID {
			continue
		}
		limit := in.MaxChars
		if limit <= 0 {
			limit = 50000
		}
		if runes := []rune(r.Text); len(runes) > limit {
			r.Text = string(runes[:limit])
			r.Truncated = true
		}
		return r, nil
	}
	return Reply{}, ErrNoReply
}

func (f *Fake) LaunchThread(_ context.Context, in LaunchInput) (LaunchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if in.ProjectID == "" || strings.TrimSpace(in.Title) == "" {
		return LaunchResult{}, errors.New("projectId and title are required")
	}
	if f.LaunchErr != nil {
		return LaunchResult{}, f.LaunchErr
	}
	if len(in.Attachments) > MaxAttachments {
		return LaunchResult{}, fmt.Errorf("at most %d attachments", MaxAttachments)
	}
	for _, a := range in.Attachments {
		if !f.pending(a.ID) {
			return LaunchResult{}, fmt.Errorf("unknown attachment %s", a.ID)
		}
	}
	f.Launched = append(f.Launched, in)
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

func (f *Fake) QueueContinuation(_ context.Context, threadID, message, requestID string) (SendResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.threads[threadID]
	if !ok {
		return SendResult{}, ErrNotFound
	}
	if f.SendErr != nil {
		return SendResult{}, f.SendErr
	}
	f.Sent = append(f.Sent, FakeSend{ThreadID: threadID, Message: message, Mode: SendQueue, RequestID: requestID})
	now := f.now()
	run := Run{RunID: f.nextID("run"), Ordinal: d.RunCount + 1, Status: StatusRunning, RequestedAt: now, StartedAt: &now}
	d.RecentRuns = append([]Run{run}, d.RecentRuns...)
	d.LatestRunID = run.RunID
	d.ActiveRunID = run.RunID
	d.Status = StatusRunning
	d.Settled = false
	d.SettledAt = nil
	d.RunCount++
	d.UpdatedAt = now
	return SendResult{ThreadID: threadID, RunID: run.RunID, MessageID: f.nextID("message"), Delivery: "started"}, nil
}

func (f *Fake) UploadAttachment(_ context.Context, in UploadInput) (Attachment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(in.Data) == 0 || in.Name == "" || in.MimeType == "" {
		return Attachment{}, errors.New("name, mimeType and data are required")
	}
	a := Attachment{ID: f.nextID("pending"), Name: in.Name, MimeType: in.MimeType, SizeBytes: len(in.Data)}
	f.Uploads = append(f.Uploads, FakeUpload{Attachment: a, Data: slices.Clone(in.Data)})
	return a, nil
}

func (f *Fake) DiscardAttachment(_ context.Context, attachmentID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Discarded = append(f.Discarded, attachmentID)
	return nil
}

func (f *Fake) SendAttachments(_ context.Context, threadID, message string, atts []Attachment) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.threads[threadID]; !ok {
		return ErrNotFound
	}
	if len(atts) == 0 || len(atts) > MaxAttachments {
		return fmt.Errorf("1..%d attachments required", MaxAttachments)
	}
	for _, a := range atts {
		if !f.pending(a.ID) {
			return fmt.Errorf("unknown attachment %s", a.ID)
		}
	}
	f.SentAttachments = append(f.SentAttachments, FakeSendAttachments{ThreadID: threadID, Message: message, Attachments: slices.Clone(atts)})
	return nil
}

// pending: id was uploaded and not discarded. Caller holds mu.
func (f *Fake) pending(id string) bool {
	if slices.Contains(f.Discarded, id) {
		return false
	}
	return slices.ContainsFunc(f.Uploads, func(u FakeUpload) bool { return u.ID == id })
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

// SetProject mutates a T3-side project in place (title, archive, delete...).
func (f *Fake) SetProject(projectID string, mutate func(*Project)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.projects {
		if f.projects[i].ID == projectID {
			mutate(&f.projects[i])
			f.projects[i].UpdatedAt = f.now()
			return nil
		}
	}
	return ErrNotFound
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

// SetReply records an assistant reply on a thread: replaces the reply with
// the same ItemID, else appends it as the newest. Empty ItemID gets one.
func (f *Fake) SetReply(threadID string, r Reply) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.threads[threadID]; !ok {
		return ErrNotFound
	}
	if r.ItemID == "" {
		r.ItemID = f.nextID("item")
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = f.now()
	}
	replies := f.replies[threadID]
	if i := slices.IndexFunc(replies, func(x Reply) bool { return x.ItemID == r.ItemID }); i >= 0 {
		replies[i] = r
	} else {
		f.replies[threadID] = append(replies, r)
	}
	return nil
}

// RemoveThread forgets a thread (T3 no longer knows it).
func (f *Fake) RemoveThread(threadID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.threads, threadID)
	delete(f.replies, threadID)
	f.order = slices.DeleteFunc(f.order, func(id string) bool { return id == threadID })
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
