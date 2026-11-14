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
)

// Change is a task state transition the service applied.
type Change struct {
	TaskID int64
	From   db.TaskState
	To     db.TaskState
	Detail string
}

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

// Service runs the T3 <-> Postgres loops. Set the hooks before Run.
type Service struct {
	store *db.Store
	t3    t3.Client
	log   *slog.Logger

	// OnTransition is called after every applied task transition (link
	// runner hook). Must not block for long.
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
	catalog       atomic.Pointer[CatalogSnapshot]
	lastStalePoll atomic.Int64 // unix nanos

	started     time.Time   // threads first seen later notify only if created after this
	watchSeeded atomic.Bool // first watcher pass done (it records silently)
	publicURL   string
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
	}
}

// Run starts the full sync, active poller, thread watcher and catalog loops
// and blocks until ctx is done. Errors are logged, never fatal. Call
// RecoverLaunching first.
func (s *Service) Run(ctx context.Context) {
	var wg gosync.WaitGroup
	loop := func(name string, every time.Duration, fn func(context.Context) error) {
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
			}
		}
	}
	wg.Add(5)
	go loop("full sync", s.FullSyncEvery, func(ctx context.Context) error {
		_, err := s.FullSync(ctx)
		if errors.Is(err, ErrSyncRunning) {
			return nil
		}
		return err
	})
	go loop("poll", s.PollEvery, s.PollActive)
	go loop("watch threads", s.PollEvery, s.WatchThreads)
	go loop("continuations", s.PollEvery, s.ReconcileContinuations)
	go loop("catalog refresh", s.CatalogEvery, func(ctx context.Context) error {
		_, err := s.RefreshCatalog(ctx)
		return err
	})
	wg.Wait()
}

// SetPublicURL configures canonical links in continuation envelopes before Run.
func (s *Service) SetPublicURL(url string) { s.publicURL = url }

// RecoverLaunching fails tasks stuck in launching (process died mid-launch).
// The launch may have reached T3, so the next start reuses the attempt's
// clientRequestId and T3 returns that thread instead of creating a second.
// Run it once at startup before the poller, link runner and HTTP server.
func (s *Service) RecoverLaunching(ctx context.Context) {
	tasks, err := s.store.ListTasks(ctx, db.TaskFilter{States: []string{string(db.StateLaunching)}})
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

// FullSync mirrors all T3 projects and threads (all projects, subagents
// included) and records last_full_sync_at.
func (s *Service) FullSync(ctx context.Context) (Result, error) {
	if !s.syncing.CompareAndSwap(false, true) {
		return Result{}, ErrSyncRunning
	}
	defer s.syncing.Store(false)

	projects, err := s.t3.ListProjects(ctx)
	if err != nil {
		return Result{}, t3Err(fmt.Errorf("list projects: %w", err))
	}
	nProjects, err := s.store.UpsertT3Projects(ctx, projects)
	if err != nil {
		return Result{}, fmt.Errorf("upsert projects: %w", err)
	}
	nThreads := 0
	var cursor *int
	for page := 0; page < 10000; page++ {
		res, err := s.t3.ListThreads(ctx, t3.ListThreadsInput{
			AllProjects: true, IncludeSubagents: true, Limit: 100, Cursor: cursor,
		})
		if err != nil {
			return Result{}, t3Err(fmt.Errorf("list threads: %w", err))
		}
		if err := s.store.UpsertThreads(ctx, res.Threads); err != nil {
			return Result{}, fmt.Errorf("upsert threads: %w", err)
		}
		// List rows omit pending requests and active run metadata. Read root
		// detail as well, including old, settled and task-owned sessions.
		for _, thread := range res.Threads {
			if thread.ParentThreadID != "" {
				continue
			}
			if _, err := s.readSession(ctx, thread.ThreadID); err != nil && !errors.Is(err, t3.ErrNotFound) {
				return Result{}, t3Err(fmt.Errorf("read session: %w", err))
			}
		}
		nThreads += len(res.Threads)
		if res.NextCursor == nil || len(res.Threads) == 0 || (cursor != nil && *res.NextCursor <= *cursor) {
			break
		}
		cursor = res.NextCursor
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

// frozen: the thread no longer drives the task state. backlog tasks, and
// settled tasks with a follower (it has been released). Other settled tasks
// and all cancelled tasks may reopen: T3 reports a cancelled turn (e.g. a
// denied permission) as thread status cancelled, then the thread runs on; a
// cancelled task's follower waits for it to settle.
func frozen(t db.Task) bool {
	switch t.State {
	case db.StateBacklog:
		return true
	case db.StateSettled:
		return t.NextTaskID != nil
	}
	return false
}

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

// StartTask is a manual start (POST /api/tasks/:id/start), "Start now": a
// backlog task still waiting on an unfinished predecessor is unlinked from
// it, then launched. Its own follower stays linked.
func (s *Service) StartTask(ctx context.Context, id int64) (db.Task, error) {
	return s.launch(ctx, id, true, "launch requested")
}

// LaunchTask starts a task in T3 (link runner path; keeps links).
func (s *Service) LaunchTask(ctx context.Context, id int64) (db.Task, error) {
	return s.launch(ctx, id, false, "launch requested")
}

// LaunchScheduled starts a task whose start time has come (scheduler path;
// keeps links). detail is the launching event's detail.
func (s *Service) LaunchScheduled(ctx context.Context, id int64, detail string) (db.Task, error) {
	return s.launch(ctx, id, false, detail)
}

// launch: launching (attempt+1, or the same attempt when the previous launch
// ended in a transport failure or restart) → t3_thread_launch with the
// attempt's clientRequestId (task-<id>-<uuid>) → running with the new thread.
// A thread in another project than the task's is a clientRequestId
// collision (T3 deduped to someone else's launch): the task fails instead of
// adopting it. A transport failure is
// retried once with the same clientRequestId. On T3 error the task lands in
// failed with lastError and a *db.T3Error is returned alongside the updated
// task.
func (s *Service) launch(ctx context.Context, id int64, manual bool, detail string) (db.Task, error) {
	// The launch must finish (or fail) even if the HTTP client goes away.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
	defer cancel()

	res, err := s.transition(ctx, db.Transition{
		TaskID: id, From: launchable, To: db.StateLaunching, Detail: detail, BeginAttempt: true,
		UnlinkWaiting: manual,
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
	in.Attachments, err = s.uploadImages(ctx, id)
	var out t3.LaunchResult
	collision := false
	if err == nil {
		out, err = s.launchThread(ctx, in)
		if t3.IsTransportError(err) && ctx.Err() == nil {
			s.log.Warn("launch transport failure; retrying with the same clientRequestId", "task", id, "clientRequestId", in.ClientRequestID, "err", err)
			out, err = s.launchThread(ctx, in)
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
