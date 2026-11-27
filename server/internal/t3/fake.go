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
	order       []string                  // thread ids, oldest first
	replies     map[string][]Reply        // thread id -> assistant replies, oldest first
	items       map[string][]TimelineItem // thread id -> visible timeline, position = index
	itemReads   int
	launches    map[string]string // clientRequestID -> threadID
	missingRefs map[string]bool   // worktree base refs that do not resolve (MissRef)
	Catalog     Catalog
	Sent        []FakeSend
	Interrupted []string // interrupted thread ids
	// Renamed: every accepted RenameThread; RenameErr fails it (tests).
	Renamed   []FakeRename
	RenameErr error
	// Uploads: every UploadAttachment, in order. Discarded: discarded ids.
	Uploads   []FakeUpload
	Discarded []string
	// Launched: every LaunchThread input that created or deduplicated a thread.
	Launched []LaunchInput
	// SentAttachments: every SendAttachments call.
	SentAttachments []FakeSendAttachments
	// LaunchErr, when set, fails LaunchThread (tests).
	LaunchErr error
	// SearchErr fails SearchThreads; SearchDelay holds it (until ctx ends).
	SearchErr   error
	SearchDelay time.Duration
	// Searches: every SearchThreads input.
	Searches []SearchThreadsInput
	messages []fakeMessage
	// Responses: every accepted RespondPendingQuestion.
	Responses []FakeResponse
	// requests: per-thread pending requests (user input and approvals).
	requests map[string][]*fakeRequest
	// askOnLaunch: every launched thread asks the demo questions a moment
	// after launch (SeedDemo); askAt holds when each one asks.
	askOnLaunch bool
	askAt       map[string]time.Time
	// probe overrides the default probe (SetProbe).
	probe *Probe
	// AdminToken: the last SetAdminToken.
	AdminToken string
	// BaseURL, PublicURL: the last Reconfigure.
	BaseURL, PublicURL string
	// Pairs: every accepted Pair code.
	Pairs []string
	// unpaired: Unpair ran since the last Pair; Status reports no token.
	unpaired bool
}

type fakeMessage struct {
	threadID, text string
	source         SearchSource
	at             time.Time
}

// FakeResponse is one accepted answer.
type FakeResponse struct {
	ThreadID, RequestID string
	Answers             map[string]Answer
}

type fakeRequest struct {
	id        string
	approval  bool
	status    string // pending|resolved|expired
	questions []Question
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
}

var _ Client = (*Fake)(nil)

func NewFake() *Fake {
	return &Fake{
		now:      time.Now,
		threads:  map[string]*ThreadDetail{},
		replies:  map[string][]Reply{},
		items:    map[string][]TimelineItem{},
		launches: map[string]string{},
		requests: map[string][]*fakeRequest{},
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

// FakeEnvironmentID is the fake T3's environment id.
const FakeEnvironmentID = "fake-env"

func (f *Fake) Status(context.Context) ConnectionStatus {
	exp := f.now().Add(30 * 24 * time.Hour)
	f.mu.Lock()
	p := f.probeLocked()
	unpaired := f.unpaired
	f.mu.Unlock()
	if unpaired {
		return ConnectionStatus{EnvironmentID: FakeEnvironmentID, Probe: p}
	}
	return ConnectionStatus{Connected: true, ClientID: "fake", ExpiresAt: &exp, EnvironmentID: FakeEnvironmentID, HasToken: true, Probe: p}
}

// Probe is a reachable fake T3 unless SetProbe says otherwise.
func (f *Fake) Probe(context.Context) Probe {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.probeLocked()
}

func (f *Fake) probeLocked() Probe {
	if f.probe != nil {
		return *f.probe
	}
	return Probe{Reachable: true, CheckedAt: f.now().UTC(), Identity: &Identity{EnvironmentID: FakeEnvironmentID, Label: "fake", ServerVersion: "fake"}}
}

// SetProbe sets what Probe and Status report (tests).
func (f *Fake) SetProbe(p Probe) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probe = &p
}

// Reconfigure records the URLs; the fake answers the same everywhere.
func (f *Fake) Reconfigure(base, public string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.BaseURL, f.PublicURL = base, public
}

// SetAdminToken records the admin token; a non-empty one pairs the fake
// (it mints a token at once).
func (f *Fake) SetAdminToken(_ context.Context, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.AdminToken = token
	if token != "" {
		f.unpaired = false
	}
	return nil
}

// WebBase is the configured public URL (the browser base for web links).
func (f *Fake) WebBase() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.PublicURL
}

func (f *Fake) Pair(_ context.Context, code string) error {
	code = strings.TrimSpace(code)
	if code == "" {
		return errors.New("empty pairing code")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Pairs = append(f.Pairs, code)
	f.unpaired = false
	return nil
}

// Unpair makes Status report no token until the next Pair.
func (f *Fake) Unpair(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unpaired = true
	return nil
}

func (f *Fake) ListProjects(context.Context) ([]Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := slices.Clone(f.projects)
	// As the wire decoding does: a live project is importable.
	for i := range out {
		out[i].Importable = out[i].ArchivedAt == nil && out[i].DeletedAt == nil
	}
	return out, nil
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
	if at, ok := f.askAt[threadID]; ok && !f.now().Before(at) {
		delete(f.askAt, threadID)
		f.requests[threadID] = append(f.requests[threadID], &fakeRequest{id: f.nextID("request"), status: "pending", questions: demoQuestions()})
		f.recount(t)
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

// messageItemTypes are the item types t3_thread_read view "messages" returns.
var messageItemTypes = []string{"user_message", "assistant_message", "proposed_plan"}

func (f *Fake) ReadItems(_ context.Context, in ReadItemsInput) (ItemPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.itemReads++
	t, ok := f.threads[in.ThreadID]
	if !ok {
		return ItemPage{}, ErrNotFound
	}
	detail := *t
	detail.RecentRuns = slices.Clone(t.RecentRuns)
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, MaxItemsPerRead)
	maxChars := in.MaxChars
	if maxChars <= 0 {
		maxChars = 20000
	}
	maxChars = min(maxChars, MaxItemChars)
	after := -1
	if in.AfterPosition != nil {
		after = *in.AfterPosition
	}
	var matching []TimelineItem
	for _, it := range f.items[in.ThreadID] {
		if in.ItemID != "" {
			if it.ItemID == in.ItemID {
				matching = append(matching, it)
			}
			continue
		}
		if it.Position > after && slices.Contains(messageItemTypes, it.Type) {
			matching = append(matching, it)
		}
	}
	page := ItemPage{Thread: detail, HasMore: len(matching) > limit}
	if len(matching) > limit {
		matching = matching[:limit]
	}
	offset := 0
	if in.ItemID != "" {
		offset = in.TextOffset
	}
	for _, it := range matching {
		text := []rune(it.Text)
		start := min(offset, len(text))
		end := min(start+maxChars, len(text))
		it.Text = string(text[start:end])
		it.TextTruncated = end < len(text)
		it.NextTextOffset = nil
		if it.TextTruncated {
			next := end
			it.NextTextOffset = &next
		}
		page.Items = append(page.Items, it)
	}
	if n := len(page.Items); n > 0 {
		pos := page.Items[n-1].Position
		page.NextPosition = &pos
	}
	return page, nil
}

// AddItem appends a timeline item to a thread (any type; only message types
// are readable) and returns its position. Zero Status means completed.
func (f *Fake) AddItem(threadID string, it TimelineItem) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	pos := len(f.items[threadID])
	it.Position = pos
	if it.ItemID == "" {
		it.ItemID = f.nextID("item")
	}
	if it.SourceThreadID == "" {
		it.SourceThreadID = threadID
	}
	if it.Visibility == "" {
		it.Visibility = "local"
	}
	if it.Status == "" {
		it.Status = "completed"
	}
	if it.UpdatedAt.IsZero() {
		it.UpdatedAt = f.now()
	}
	f.items[threadID] = append(f.items[threadID], it)
	return pos
}

// UpdateItem mutates the item at a position (streaming text, status).
func (f *Fake) UpdateItem(threadID string, position int, mutate func(*TimelineItem)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := f.items[threadID]
	if position < 0 || position >= len(items) {
		return ErrNotFound
	}
	mutate(&items[position])
	items[position].Position = position
	items[position].UpdatedAt = f.now()
	return nil
}

// ItemReads counts ReadItems calls (tests: poller T3 load).
func (f *Fake) ItemReads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.itemReads
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
	id, replay := f.launches[in.ClientRequestID]
	replay = replay && in.ClientRequestID != ""
	if w := in.Workspace; !replay && w != nil && w.Type == WorkspaceWorktree && f.missingRefs[w.BaseRef] {
		// T3 checkWorktreeBase: rejected before any thread is created.
		return LaunchResult{}, &ToolError{Tool: "t3_thread_launch", Code: "invalid_request", Message: fmt.Sprintf(
			"Base ref %q does not resolve to a commit in project %q. No thread was created.", w.BaseRef, in.ProjectID)}
	}
	f.Launched = append(f.Launched, in)
	if replay {
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
	id = f.nextID("thread")
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
	if f.askOnLaunch {
		f.askAt[id] = now.Add(3 * time.Second)
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

// FakeRename is one RenameThread call.
type FakeRename struct{ ThreadID, Title string }

// RenameThread mimics t3_thread_update rename: the thread takes the title.
func (f *Fake) RenameThread(_ context.Context, threadID, title string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.RenameErr != nil {
		return f.RenameErr
	}
	t, ok := f.threads[threadID]
	if !ok {
		return ErrNotFound
	}
	t.Title = strings.TrimSpace(title)
	f.Renamed = append(f.Renamed, FakeRename{ThreadID: threadID, Title: t.Title})
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

// MissRef makes worktree launches from base ref fail like T3's
// checkWorktreeBase (the ref does not resolve in the project).
func (f *Fake) MissRef(ref string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.missingRefs == nil {
		f.missingRefs = map[string]bool{}
	}
	f.missingRefs[ref] = true
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

// AddUserInput adds a pending user-input request (as AskUserQuestion would)
// and returns its id. PendingRequestCount follows.
func (f *Fake) AddUserInput(threadID string, questions []Question) (string, error) {
	return f.addRequest(threadID, &fakeRequest{questions: slices.Clone(questions)})
}

// AddApproval adds a pending permission request (never listed by
// t3_pending_request). PendingRequestCount follows.
func (f *Fake) AddApproval(threadID string) (string, error) {
	return f.addRequest(threadID, &fakeRequest{approval: true})
}

func (f *Fake) addRequest(threadID string, r *fakeRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.threads[threadID]
	if !ok {
		return "", ErrNotFound
	}
	r.id = f.nextID("request")
	r.status = "pending"
	f.requests[threadID] = append(f.requests[threadID], r)
	f.recount(t)
	return r.id, nil
}

// ResolveRequest settles a request outside stage (answered in T3: resolved;
// "expired" for a stale one). PendingRequestCount follows.
func (f *Fake) ResolveRequest(threadID, requestID, status string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.threads[threadID]
	if !ok {
		return ErrNotFound
	}
	for _, r := range f.requests[threadID] {
		if r.id == requestID {
			r.status = status
			f.recount(t)
			return nil
		}
	}
	return ErrNotFound
}

// recount sets PendingRequestCount from pending requests. Caller holds mu.
func (f *Fake) recount(t *ThreadDetail) {
	n := 0
	for _, r := range f.requests[t.ThreadID] {
		if r.status == "pending" {
			n++
		}
	}
	t.PendingRequestCount = n
	t.UpdatedAt = f.now()
}

func (f *Fake) ListPendingQuestions(_ context.Context, threadID string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.threads[threadID]; !ok {
		return nil, &ToolError{Tool: "t3_pending_request", Code: "thread_not_found", Message: "Thread " + threadID + " is no longer available."}
	}
	ids := []string{}
	for _, r := range f.requests[threadID] {
		if !r.approval && r.status == "pending" {
			ids = append(ids, r.id)
		}
	}
	return ids, nil
}

// question is T3 readQuestion. Caller holds mu.
func (f *Fake) question(threadID, requestID string) (*fakeRequest, error) {
	if _, ok := f.threads[threadID]; !ok {
		return nil, &ToolError{Tool: "t3_pending_request", Code: "thread_not_found", Message: "Thread " + threadID + " is no longer available."}
	}
	for _, r := range f.requests[threadID] {
		if r.id != requestID || r.approval {
			continue
		}
		switch r.status {
		case "pending":
			return r, nil
		case "resolved":
			return nil, &ToolError{Tool: "t3_pending_request", Code: "invalid_request", Message: "The user-input request was already answered."}
		default:
			return nil, &ToolError{Tool: "t3_pending_request", Code: "invalid_request", Message: "The user-input request is no longer pending (" + r.status + ")."}
		}
	}
	return nil, &ToolError{Tool: "t3_pending_request", Code: "invalid_request", Message: "The pending user-input request was not found."}
}

func (f *Fake) ReadPendingQuestion(_ context.Context, threadID, requestID string) (PendingQuestion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, err := f.question(threadID, requestID)
	if err != nil {
		return PendingQuestion{}, err
	}
	return PendingQuestion{RequestID: r.id, Questions: slices.Clone(r.questions)}, nil
}

func (f *Fake) RespondPendingQuestion(_ context.Context, threadID, requestID string, answers map[string]Answer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, err := f.question(threadID, requestID)
	if err != nil {
		return err
	}
	if len(answers) == 0 {
		return &ToolError{Tool: "t3_pending_request", Code: "invalid_request", Message: "answers is required."}
	}
	r.status = "resolved"
	f.recount(f.threads[threadID])
	f.Responses = append(f.Responses, FakeResponse{ThreadID: threadID, RequestID: requestID, Answers: answers})
	return nil
}

// SeedDemo fills the fake for local UI work (T3_FAKE_SEED=1): a demo project
// with external threads waiting on questions, a mix, and an approval only;
// launched tasks then ask the demo questions too.
func (f *Fake) SeedDemo() {
	ctx := context.Background()
	p := f.AddProject("demo", "/demo")
	launch := func(title string) string {
		r, _ := f.LaunchThread(ctx, LaunchInput{ProjectID: p.ID, Title: title})
		// The opening prompt, as T3 records it (session detail Prompt panel).
		f.AddItem(r.ThreadID, TimelineItem{Type: "user_message", Text: title + ".\n\nRead the relevant code first, keep the change minimal, and ask before anything destructive.", CreatedBy: "user"})
		return r.ThreadID
	}
	asks := launch("Pick the rollout plan for the sync rewrite")
	_, _ = f.AddUserInput(asks, demoQuestions())
	mixed := launch("Migrate notifications table")
	_, _ = f.AddUserInput(mixed, []Question{{ID: "strict", Header: "Backfill", Question: "Backfill old rows now?",
		AllowCustomAnswer: ptrBool(false), Options: []QuestionOption{{Label: "Yes", Description: "Runs in one transaction"}, {Label: "No", Description: "New rows only"}}}})
	_, _ = f.AddApproval(mixed)
	approval := launch("Delete stale worktrees")
	_, _ = f.AddApproval(approval)
	f.mu.Lock()
	f.askOnLaunch = true
	f.askAt = map[string]time.Time{}
	f.mu.Unlock()
}

func ptrBool(b bool) *bool { return &b }

func demoQuestions() []Question {
	initial := "Ship behind a flag first"
	return []Question{
		{ID: "approach", Header: "Approach", Question: "Which rollout approach should I take?", Options: []QuestionOption{
			{Label: "Big bang", Description: "Replace the sync loop in one change"},
			{Label: "Incremental", Description: "Run old and new loops side by side, compare, then switch", Value: ptrString("incremental")},
		}},
		{ID: "checks", Header: "Checks", Question: "Which checks must pass before merging?", MultiSelect: true, Options: []QuestionOption{
			{Label: "Unit tests", Description: "go test ./..."},
			{Label: "Fake T3 e2e", Description: "Full flow against the in-memory fake"},
			{Label: "Manual phone check", Description: ""},
		}},
		{ID: "notes", Header: "Notes", Question: "Anything else I should know?", InitialAnswer: &initial, Options: []QuestionOption{}},
	}
}

func ptrString(s string) *string { return &s }
