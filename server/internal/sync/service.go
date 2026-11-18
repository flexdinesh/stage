package sync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	gosync "sync"
	"sync/atomic"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/workevent"
)

// Change is a task state transition the service applied.
type Change = workevent.TaskChange

// Result is a full sync summary (POST /api/sync).
type Result struct {
	Projects   int       `json:"projects"`
	Threads    int       `json:"threads"`
	FinishedAt time.Time `json:"finishedAt"`
}

// CatalogSnapshot is a cached orchestrator_capabilities result (unfiltered).
type CatalogSnapshot struct {
	Catalog   t3.Catalog
	FetchedAt time.Time
}

// ErrSyncRunning: a full sync is already in progress (409).
var ErrSyncRunning error = &db.ConflictError{Msg: "sync already running"}

// Service runs the T3 <-> database loops. Set the hooks before Run.
type Service struct {
	store *db.Store
	t3    t3.Client
	log   *slog.Logger

	// OnTransition is called after every applied task transition (release
	// runner and notifier hook). Must not block for long.
	OnTransition func(ctx context.Context, c Change)
	// OnThreadChange is called when an external thread changes into
	// needs_input, done or failed (thread watcher). Must not block.
	OnThreadChange func(ctx context.Context, c ThreadChange)

	FullSyncEvery time.Duration
	PollEvery     time.Duration
	CatalogEvery  time.Duration
	// StalePollEvery: done tasks untouched for an hour are polled at
	// most this often.
	StalePollEvery time.Duration
	// LaunchAttemptTimeout bounds one t3_thread_launch call (a launch makes
	// at most two).
	LaunchAttemptTimeout time.Duration

	syncing       atomic.Bool
	syncStarted   atomic.Int64 // unix nanos of the running full sync's start
	syncProjects  atomic.Int64 // projects mirrored so far by the running full sync
	syncThreads   atomic.Int64 // threads mirrored so far by the running full sync
	kickSync      chan struct{}
	kickCatalog   chan struct{}
	catalog       atomic.Pointer[CatalogSnapshot]
	lastStalePoll atomic.Int64 // unix nanos

	started     time.Time   // threads first seen later notify only if created after this
	watchSeeded atomic.Bool // first watcher pass done (it records silently)
}

func New(store *db.Store, client t3.Client, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		store: store, t3: client, log: log.With("component", "sync"),
		FullSyncEvery:        5 * time.Minute,
		PollEvery:            5 * time.Second,
		CatalogEvery:         10 * time.Minute,
		StalePollEvery:       time.Minute,
		LaunchAttemptTimeout: 40 * time.Second,
		started:              time.Now(),
		kickSync:             make(chan struct{}, 1),
		kickCatalog:          make(chan struct{}, 1),
	}
}

// Kick asks Run for a full sync and a catalog refresh now (after pairing),
// instead of at their next tick. Never blocks; kicks while one is pending
// coalesce.
func (s *Service) Kick() {
	for _, ch := range []chan struct{}{s.kickSync, s.kickCatalog} {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Progress is the running full sync, read lock-free (GET /api/t3/status).
type Progress struct {
	Running   bool
	StartedAt *time.Time
	Projects  int // projects mirrored so far
	Threads   int // threads mirrored so far
}

// Progress reports the running full sync (zero when none runs).
func (s *Service) Progress() Progress {
	if !s.syncing.Load() {
		return Progress{}
	}
	p := Progress{Running: true, Projects: int(s.syncProjects.Load()), Threads: int(s.syncThreads.Load())}
	if n := s.syncStarted.Load(); n != 0 {
		at := time.Unix(0, n).UTC()
		p.StartedAt = &at
	}
	return p
}

// Run starts the full sync, active poller, thread watcher and catalog loops
// and blocks until ctx is done. Errors are logged, never fatal. Call
// RecoverLaunching first.
func (s *Service) Run(ctx context.Context) {
	var wg gosync.WaitGroup
	// kick (nil: none) runs fn before the next tick.
	loop := func(name string, every time.Duration, kick <-chan struct{}, fn func(context.Context) error) {
		defer wg.Done()
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			if err := fn(ctx); err != nil && ctx.Err() == nil {
				s.log.Warn(name+" failed", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			case <-kick:
				t.Reset(every)
			}
		}
	}
	wg.Add(4)
	go loop("full sync", s.FullSyncEvery, s.kickSync, func(ctx context.Context) error {
		_, err := s.FullSync(ctx)
		if errors.Is(err, ErrSyncRunning) {
			return nil
		}
		return err
	})
	go loop("poll", s.PollEvery, nil, s.PollActive)
	go loop("watch threads", s.PollEvery, nil, s.WatchThreads)
	go loop("catalog refresh", s.CatalogEvery, s.kickCatalog, func(ctx context.Context) error {
		_, err := s.RefreshCatalog(ctx)
		return err
	})
	wg.Wait()
}

// RecoverLaunching fails tasks stuck in launching (process died mid-launch).
// The launch may have reached T3, so the next start reuses the attempt's
// clientRequestId and T3 returns that thread instead of creating a second.
// Run it once at startup before the poller, release runner and HTTP server.
func (s *Service) RecoverLaunching(ctx context.Context) {
	tasks, err := s.store.ListTasks(ctx, db.TaskFilter{States: []string{string(db.StateLaunching)}, Closed: "all"})
	if err != nil {
		s.log.Warn("recover launching tasks", "err", err)
		return
	}
	msg := "launch interrupted by server restart; start again to resume it"
	for _, t := range tasks {
		if _, err := s.transition(ctx, db.Transition{
			TaskID: t.ID, From: []db.TaskState{db.StateLaunching}, To: db.StateFailed, Detail: msg, LastError: &msg,
			ReuseAttempt: true,
		}); err != nil {
			s.log.Warn("recover launching task", "task", t.ID, "err", err)
		}
	}
}

func (s *Service) transition(ctx context.Context, t db.Transition) (db.TransitionResult, error) {
	res, err := s.store.TransitionTask(ctx, t)
	if err != nil || !res.Changed {
		return res, err
	}
	s.log.Info("task transition", "task", t.TaskID, "from", res.From, "to", t.To, "detail", t.Detail)
	if s.OnTransition != nil {
		s.OnTransition(ctx, Change{TaskID: t.TaskID, From: res.From, To: t.To, Detail: t.Detail})
	}
	return res, nil
}

func t3Err(err error) error {
	if err == nil {
		return nil
	}
	return &db.T3Error{Err: err}
}

// FullSync refreshes projects and task-owned sessions. Active external
// discovery belongs to the notification watcher.
func (s *Service) FullSync(ctx context.Context) (Result, error) {
	if !s.syncing.CompareAndSwap(false, true) {
		return Result{}, ErrSyncRunning
	}
	defer s.syncing.Store(false)
	s.syncProjects.Store(0)
	s.syncThreads.Store(0)
	s.syncStarted.Store(time.Now().UnixNano())

	projects, err := s.t3.ListProjects(ctx)
	if err != nil {
		return Result{}, t3Err(fmt.Errorf("list projects: %w", err))
	}
	nProjects, err := s.store.UpsertT3Projects(ctx, projects)
	if err != nil {
		return Result{}, fmt.Errorf("upsert projects: %w", err)
	}
	s.syncProjects.Store(int64(nProjects))
	// Reconcile only sessions owned by Stage tasks. External active-session
	// discovery belongs to the T3 watcher; no historical session sweep.
	tasks, err := s.store.ListTasks(ctx, db.TaskFilter{Closed: "all"})
	if err != nil {
		return Result{}, err
	}
	nThreads := 0
	for _, task := range tasks {
		if task.T3ThreadID == nil {
			continue
		}
		if err := s.PollTask(ctx, task); err != nil {
			return Result{}, err
		}
		nThreads++
		s.syncThreads.Store(int64(nThreads))
	}
	now := time.Now().UTC()
	if err := s.store.SetLastFullSync(ctx, now); err != nil {
		return Result{}, err
	}
	s.log.Info("full sync done", "projects", nProjects, "threads", nThreads)
	return Result{Projects: nProjects, Threads: nThreads, FinishedAt: now}, nil
}

// PollActive reads every pollable task's thread and applies state changes.
func (s *Service) PollActive(ctx context.Context) error {
	now := time.Now()
	last := s.lastStalePoll.Load()
	stale := now.Sub(time.Unix(0, last)) >= s.StalePollEvery && s.lastStalePoll.CompareAndSwap(last, now.UnixNano())
	tasks, err := s.store.PollableTasks(ctx, stale)
	if err != nil {
		return err
	}
	if len(tasks) == 0 || !s.t3.Status(ctx).Connected {
		return nil
	}
	for _, t := range tasks {
		if ctx.Err() != nil {
			return nil
		}
		if err := s.PollTask(ctx, t); err != nil {
			if errors.Is(err, t3.ErrNotConnected) {
				return err
			}
			s.log.Warn("poll task", "task", t.ID, "err", err)
		}
	}
	return nil
}

// PollTask reads one task's thread, refreshes the mirror row and applies
// the mapped task state, except for frozen tasks (see frozen).
func (s *Service) PollTask(ctx context.Context, task db.Task) error {
	if task.ConnectionID != db.T3ConnectionID {
		return nil
	}
	if task.T3ThreadID == nil {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	d, err := s.t3.ReadThread(callCtx, *task.T3ThreadID)
	cancel()
	if errors.Is(err, t3.ErrNotFound) {
		if frozen(task) {
			return nil
		}
		msg := "thread not found in T3"
		_, err := s.transition(ctx, db.Transition{
			TaskID: task.ID, From: []db.TaskState{task.State}, To: db.StateFailed, Detail: msg, LastError: &msg,
		})
		return err
	}
	if err != nil {
		return t3Err(err)
	}
	if err := s.mirrorDetail(ctx, d); err != nil {
		return fmt.Errorf("upsert thread: %w", err)
	}
	if frozen(task) {
		return nil
	}
	next := mapDetail(task.State, d)
	if next == task.State {
		return nil
	}
	_, err = s.transition(ctx, db.Transition{
		TaskID: task.ID, From: []db.TaskState{task.State}, To: next, Detail: describe(d),
	})
	return err
}

// Backlog tasks have not launched. Every observed execution state stays live;
// execution changes never modify user acceptance.
func frozen(t db.Task) bool { return t.State == db.StateBacklog }

// describe is the event detail: the observed T3 status.
func describe(d t3.ThreadDetail) string {
	switch {
	case d.Settled:
		return "thread settled"
	case d.PendingRequestCount > 0:
		return fmt.Sprintf("thread status %s, %d pending request(s)", d.Status, d.PendingRequestCount)
	case d.Status == t3.StatusIdle && len(d.RecentRuns) > 0:
		return fmt.Sprintf("thread status idle, latest run %s", d.RecentRuns[0].Status)
	}
	return "thread status " + string(d.Status)
}

// RefreshCatalog fetches orchestrator_capabilities into the cache.
func (s *Service) RefreshCatalog(ctx context.Context) (*CatalogSnapshot, error) {
	c, err := s.t3.Capabilities(ctx)
	if err != nil {
		return nil, t3Err(err)
	}
	snap := &CatalogSnapshot{Catalog: c, FetchedAt: time.Now().UTC()}
	s.catalog.Store(snap)
	return snap, nil
}

// Catalog returns the cached catalog, fetching it when empty.
func (s *Service) Catalog(ctx context.Context) (*CatalogSnapshot, error) {
	if c := s.catalog.Load(); c != nil {
		return c, nil
	}
	return s.RefreshCatalog(ctx)
}

var launchable = []db.TaskState{db.StateBacklog, db.StateFailed, db.StateInterrupted, db.StateCancelled}

// StartTask is a manual start. Every prerequisite must be accepted at claim time.
func (s *Service) StartTask(ctx context.Context, id int64) (db.Task, error) {
	return s.launch(ctx, id, "launch requested", false)
}

// LaunchTask starts a task in T3 (release runner path).
func (s *Service) LaunchTask(ctx context.Context, id int64) (db.Task, error) {
	return s.launch(ctx, id, "launch requested", true)
}

// LaunchScheduled starts a task whose start time has come (scheduler path).
// detail is the launching event's detail.
func (s *Service) LaunchScheduled(ctx context.Context, id int64, detail string) (db.Task, error) {
	return s.launch(ctx, id, detail, false)
}

// launch: launching (attempt+1, or the same attempt when the previous launch
// ended in a transport failure or restart) → t3_thread_launch with the
// attempt's clientRequestId (task-<id>-<uuid>) → running with the new thread.
// A thread in another project than the task's is a clientRequestId
// collision (T3 deduped to someone else's launch): the task fails instead of
// adopting it. A transport failure is
// retried once with the same clientRequestId. On T3 error the task lands in
// failed with lastError and a *db.T3Error is returned alongside the updated
// task. An adopted session is never launched (Conflict).
func (s *Service) launch(ctx context.Context, id int64, detail string, automatic bool) (db.Task, error) {
	// The launch must finish (or fail) even if the HTTP client goes away.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
	defer cancel()

	cur, err := s.store.GetTask(ctx, id)
	if err != nil {
		return db.Task{}, err
	}
	if cur.Adopted {
		return db.Task{}, db.Conflict("task %d tracks an adopted session: open it in T3 instead of starting it", id)
	}
	if cur.ConnectionID != db.T3ConnectionID {
		return db.Task{}, db.Conflict("this harness is not available yet; select a usable T3 binding")
	}
	if !s.t3.Status(ctx).Connected {
		return db.Task{}, &db.ConflictError{Code: "connection_unavailable", Msg: "T3 is disconnected. Reconnect it in Settings before starting."}
	}
	// Verify external routing before claiming or retrying. A changed directory
	// must never silently redirect an idempotent launch request.
	projects, err := s.t3.ListProjects(ctx)
	if err != nil {
		return db.Task{}, &db.ConflictError{Code: "connection_unavailable", Msg: "Cannot verify the T3 workspace. Check the connection in Settings before starting."}
	}
	if _, err = s.store.UpsertT3Projects(ctx, projects); err != nil {
		return db.Task{}, err
	}
	if old, err := s.store.LaunchDestination(ctx, id, cur.LaunchAttempt); err == nil {
		// Only uncertain retries reuse this claim; new attempts use the current binding.
		reuse, err := s.store.ReusesLaunch(ctx, id)
		if err != nil {
			return db.Task{}, err
		}
		if reuse {
			found := false
			for _, p := range projects {
				if p.ID == old.ExternalProjectID && p.WorkspaceRoot == old.Workspace && p.DeletedAt == nil {
					found = true
				}
			}
			if !found {
				return db.Task{}, db.Conflict("original launch workspace is unavailable or changed; restore it before reconciling this attempt")
			}
		}
	}
	res, err := s.transition(ctx, db.Transition{
		TaskID: id, From: launchable, To: db.StateLaunching, Detail: detail, BeginAttempt: true, RequireAuto: automatic,
	})
	if err != nil {
		return db.Task{}, err
	}
	if !res.Changed {
		return db.Task{}, db.Conflict("task is %s; start is allowed from backlog, failed, interrupted or cancelled", res.From)
	}
	task, err := s.store.GetTask(ctx, id)
	if err != nil {
		s.failLaunching(ctx, id, "launch aborted", err.Error(), nil, false)
		return db.Task{}, err
	}
	in := launchInput(task, res.RequestID)
	deps := s.depSourcesOf(task)
	block, err := s.prepareHandoff(ctx, task)
	if err != nil {
		s.failLaunching(ctx, id, "launch aborted", "persist accepted handoff: "+err.Error(), nil, true)
		return db.Task{}, err
	}
	if block != "" {
		in.Message = task.Prompt + "\n\n" + block
	}
	journal, err := s.store.JournalLaunchContext(ctx, task, journalInstructions(task))
	if err != nil {
		s.failLaunching(ctx, id, "launch aborted", "persist journal context: "+err.Error(), nil, true)
		return db.Task{}, err
	}
	in.Message += "\n\n" + journal
	var fallback *t3.WorkspaceStrategy
	if task.Workspace.FromPredecessor() {
		in.Workspace, fallback = s.prepareBase(ctx, task, deps)
	}
	in.Attachments, err = s.uploadImages(ctx, id)
	var out t3.LaunchResult
	collision := false
	if err == nil {
		out, err = s.launchWithRetry(ctx, in)
		if fallback != nil && BaseRefRejected(err, in.Workspace.BaseRef) && ctx.Err() == nil {
			// T3 created no thread: relaunch from the task's own baseRef.
			s.log.Warn("need's branch rejected; launching from fallback base", "task", id, "branch", in.Workspace.BaseRef, "err", err)
			s.rejectBase(ctx, task, fallback)
			in.Workspace = fallback
			out, err = s.launchWithRetry(ctx, in)
		}
		if err == nil && out.ProjectID != "" && out.ProjectID != task.T3ProjectID {
			collision = true // definite: the next start needs a new clientRequestId
			err = fmt.Errorf("clientRequestId %s collision: T3 returned existing thread %s in project %s", in.ClientRequestID, out.ThreadID, out.ProjectID)
		}
		if err != nil {
			s.DiscardUploads(ctx, in.Attachments)
		}
	}
	if err != nil {
		msg := err.Error()
		s.failLaunching(ctx, id, "launch failed: "+msg, msg, nil, !collision && t3.IsTransportError(err))
		task, gerr := s.store.GetTask(ctx, id)
		if gerr != nil {
			return db.Task{}, gerr
		}
		return task, t3Err(err)
	}
	now := time.Now().UTC()
	projectID := out.ProjectID
	if projectID == "" {
		projectID = task.T3ProjectID
	}
	status := out.Status
	if status == "" {
		status = t3.StatusRunning
	}
	if err := s.store.UpsertThreads(ctx, []t3.Thread{{
		ThreadID: out.ThreadID, ProjectID: projectID, Title: task.Title, Link: out.Link,
		CreationSource: "mcp", Status: status,
		ProviderInstanceID: out.ModelSelection.InstanceID, Model: out.ModelSelection.Model,
		RuntimeMode: t3.RuntimeMode(task.RuntimeMode), InteractionMode: t3.InteractionMode(task.InteractionMode),
		CreatedAt: now, UpdatedAt: now,
	}}); err != nil {
		s.log.Warn("mirror launched thread", "task", id, "err", err)
	}
	r, err := s.transition(ctx, db.Transition{
		TaskID: id, From: []db.TaskState{db.StateLaunching}, To: db.StateRunning,
		Detail: "thread launched", ThreadID: &out.ThreadID,
	})
	if err != nil {
		s.failLaunching(ctx, id, "launch not recorded", "thread launched but not recorded: "+err.Error(), &out.ThreadID, false)
		return db.Task{}, err
	}
	if !r.Changed {
		// Skipped/cancelled while launching: don't leave an orphan run.
		s.log.Warn("task left launching during launch; interrupting thread", "task", id, "state", r.From, "thread", out.ThreadID)
		if err := s.t3.Interrupt(ctx, out.ThreadID); err != nil {
			s.log.Warn("interrupt orphan thread", "thread", out.ThreadID, "err", err)
		}
	}
	return s.store.GetTask(ctx, id)
}

// uploadImages uploads a task's images to T3 (fresh pending uploads each
// launch). On failure the ones already uploaded are discarded.
func (s *Service) uploadImages(ctx context.Context, taskID int64) ([]t3.Attachment, error) {
	images, err := s.store.TaskAttachments(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return s.UploadImages(ctx, images)
}

// UploadImages uploads images to T3 in order; on failure it discards the
// ones already uploaded.
func (s *Service) UploadImages(ctx context.Context, images []db.AttachmentData) ([]t3.Attachment, error) {
	out := make([]t3.Attachment, 0, len(images))
	for _, img := range images {
		callCtx, cancel := context.WithTimeout(ctx, s.LaunchAttemptTimeout)
		a, err := s.t3.UploadAttachment(callCtx, t3.UploadInput{Name: img.Name, MimeType: img.MimeType, Data: img.Data})
		cancel()
		if err != nil {
			s.DiscardUploads(ctx, out)
			return nil, fmt.Errorf("upload image %q: %w", img.Name, err)
		}
		out = append(out, a)
	}
	return out, nil
}

// DiscardUploads best-effort discards pending T3 uploads that were not sent.
func (s *Service) DiscardUploads(ctx context.Context, atts []t3.Attachment) {
	for _, a := range atts {
		if err := s.t3.DiscardAttachment(ctx, a.ID); err != nil {
			s.log.Warn("discard t3 upload", "attachment", a.ID, "err", err)
		}
	}
}

// launchWithRetry is launchThread, retried once with the same clientRequestId
// on a transport failure.
func (s *Service) launchWithRetry(ctx context.Context, in t3.LaunchInput) (t3.LaunchResult, error) {
	out, err := s.launchThread(ctx, in)
	if t3.IsTransportError(err) && ctx.Err() == nil {
		s.log.Warn("launch transport failure; retrying with the same clientRequestId", "clientRequestId", in.ClientRequestID, "err", err)
		out, err = s.launchThread(ctx, in)
	}
	return out, err
}

// launchThread is one t3_thread_launch call bounded by LaunchAttemptTimeout.
func (s *Service) launchThread(ctx context.Context, in t3.LaunchInput) (t3.LaunchResult, error) {
	callCtx, cancel := context.WithTimeout(ctx, s.LaunchAttemptTimeout)
	defer cancel()
	return s.t3.LaunchThread(callCtx, in)
}

// failLaunching is a best-effort launching → failed with lastError (and the
// thread id when one was launched). reuse: the launch outcome is unknown, so
// the next start reuses the clientRequestId.
func (s *Service) failLaunching(ctx context.Context, id int64, detail, lastErr string, threadID *string, reuse bool) {
	if _, err := s.transition(ctx, db.Transition{
		TaskID: id, From: []db.TaskState{db.StateLaunching}, To: db.StateFailed,
		Detail: detail, LastError: &lastErr, ThreadID: threadID, ReuseAttempt: reuse && threadID == nil,
	}); err != nil {
		s.log.Error("record launch failure", "task", id, "err", err)
	}
}

func launchInput(task db.Task, requestID string) t3.LaunchInput {
	in := t3.LaunchInput{
		ProjectID:       task.T3ProjectID,
		Title:           task.Title,
		Message:         task.Prompt,
		RuntimeMode:     t3.RuntimeMode(task.RuntimeMode),
		InteractionMode: t3.InteractionMode(task.InteractionMode),
		Workspace:       WorkspaceStrategy(task.Workspace),
		ClientRequestID: requestID,
	}
	if task.ProviderInstanceID != nil && task.Model != nil {
		in.ModelSelection = &t3.ModelSelection{
			InstanceID: *task.ProviderInstanceID, Model: *task.Model, Options: task.ModelOptions,
		}
	}
	return in
}

// WorkspaceStrategy maps a task workspace to T3 workspaceStrategy: root →
// {root}; worktree → {worktree, baseRef, branch?, startFromOrigin: baseRef
// starts with a remote name ("origin/", "upstream/")}.
func WorkspaceStrategy(w db.Workspace) *t3.WorkspaceStrategy {
	if w.Type != "worktree" {
		return &t3.WorkspaceStrategy{Type: t3.WorkspaceRoot}
	}
	ws := &t3.WorkspaceStrategy{Type: t3.WorkspaceWorktree, BaseRef: w.BaseRef}
	if w.Branch != nil {
		ws.Branch = *w.Branch
	}
	fromOrigin := hasRemotePrefix(w.BaseRef)
	ws.StartFromOrigin = &fromOrigin
	return ws
}

func hasRemotePrefix(ref string) bool {
	remote, rest, ok := strings.Cut(ref, "/")
	return ok && rest != "" && (remote == "origin" || remote == "upstream")
}
