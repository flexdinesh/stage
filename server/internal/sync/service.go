// Package sync coordinates Stage-owned execution, discovery and recovery.
package sync

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/workevent"
	"log/slog"
	"strings"
	gosync "sync"
	"sync/atomic"
	"time"
)

type Change = workevent.TaskChange
type Result struct {
	Projects   int       `json:"projects"`
	Threads    int       `json:"threads"`
	FinishedAt time.Time `json:"finishedAt"`
}
type CatalogSnapshot struct {
	Catalog   execution.Capabilities
	FetchedAt time.Time
}

var ErrSyncRunning error = &db.ConflictError{Msg: "sync already running"}

type Service struct {
	store                                                                        *db.Store
	adapters                                                                     map[int64]execution.Adapter
	log                                                                          *slog.Logger
	OnTransition                                                                 func(context.Context, Change)
	OnThreadChange                                                               func(context.Context, ThreadChange)
	FullSyncEvery, PollEvery, CatalogEvery, StalePollEvery, LaunchAttemptTimeout time.Duration
	progress                                                                     atomic.Pointer[Progress]
	lastStalePoll                                                                atomic.Int64
	syncing                                                                      atomic.Bool
	catalog                                                                      atomic.Pointer[CatalogSnapshot]
	kickSync, kickCatalog                                                        chan struct{}
	locks                                                                        gosync.Map
	started                                                                      time.Time
	watchSeeded                                                                  atomic.Bool
}

func New(store *db.Store, adapter execution.Adapter, log *slog.Logger) *Service {
	return NewWithAdapters(store, map[int64]execution.Adapter{1: adapter}, log)
}
func NewWithAdapters(store *db.Store, adapters map[int64]execution.Adapter, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{store: store, adapters: adapters, log: log, FullSyncEvery: 5 * time.Minute, PollEvery: 5 * time.Second, CatalogEvery: 10 * time.Minute, StalePollEvery: time.Minute, LaunchAttemptTimeout: 40 * time.Second, kickSync: make(chan struct{}, 1), kickCatalog: make(chan struct{}, 1), started: time.Now()}
}
func (s *Service) Adapter(id int64) (execution.Adapter, error) {
	a := s.adapters[id]
	if a == nil {
		return nil, &db.ConflictError{Code: "connection_unavailable", Msg: "This harness is unavailable. Select a supported connection."}
	}
	return a, nil
}
func (s *Service) lock(id int64) func() {
	value, _ := s.locks.LoadOrStore(id, &gosync.Mutex{})
	mu := value.(*gosync.Mutex)
	mu.Lock()
	return mu.Unlock
}
func (s *Service) Kick() {
	for _, ch := range []chan struct{}{s.kickSync, s.kickCatalog} {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

type Progress struct {
	Running           bool
	StartedAt         *time.Time
	Projects, Threads int
}

func (s *Service) Progress() Progress {
	if p := s.progress.Load(); p != nil {
		return *p
	}
	return Progress{}
}
func (s *Service) Run(ctx context.Context) {
	var wg gosync.WaitGroup
	loop := func(every time.Duration, kick <-chan struct{}, fn func(context.Context) error) {
		defer wg.Done()
		timer := time.NewTicker(every)
		defer timer.Stop()
		for {
			if err := fn(ctx); err != nil && ctx.Err() == nil {
				s.log.Warn("execution sync", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			case <-kick:
			}
		}
	}
	wg.Add(4)
	go loop(s.FullSyncEvery, s.kickSync, func(ctx context.Context) error { _, e := s.FullSync(ctx); return e })
	go loop(s.PollEvery, nil, s.PollActive)
	go loop(s.PollEvery, nil, s.WatchThreads)
	go loop(s.CatalogEvery, s.kickCatalog, func(ctx context.Context) error { _, e := s.RefreshCatalog(ctx); return e })
	wg.Wait()
}
func (s *Service) RecoverLaunching(ctx context.Context) {
	if err := s.store.RecoverAttempts(ctx); err != nil {
		s.log.Error("recover attempts", "err", err)
	}
}
func (s *Service) transition(ctx context.Context, t db.Transition) (db.TransitionResult, error) {
	r, e := s.store.TransitionTask(ctx, t)
	if e == nil && r.Changed && s.OnTransition != nil {
		s.OnTransition(ctx, Change{TaskID: t.TaskID, From: r.From, To: t.To, Detail: t.Detail})
	}
	return r, e
}
func harnessErr(err error) error {
	if err == nil {
		return nil
	}
	return &db.HarnessError{Err: err}
}
func (s *Service) FullSync(ctx context.Context) (Result, error) {
	if !s.syncing.CompareAndSwap(false, true) {
		return Result{}, ErrSyncRunning
	}
	now := time.Now().UTC()
	s.progress.Store(&Progress{Running: true, StartedAt: &now})
	defer func() { s.syncing.Store(false); s.progress.Store(&Progress{}) }()
	r := Result{}
	var errs []error
	for id, a := range s.adapters {
		ps, e := a.Workspaces(ctx)
		if e != nil {
			errs = append(errs, e)
			continue
		}
		n, e := s.store.RefreshWorkspaces(ctx, id, ps)
		if e != nil {
			errs = append(errs, e)
		} else {
			r.Projects += n
			s.progress.Store(&Progress{Running: true, StartedAt: &now, Projects: r.Projects})
		}
	}
	tasks, e := s.store.ListTasks(ctx, db.TaskFilter{Closed: "all"})
	if e != nil {
		return r, e
	}
	for _, t := range tasks {
		if t.Session != nil {
			if e := s.PollTask(ctx, t); e != nil {
				errs = append(errs, e)
			} else {
				r.Threads++
			}
		}
	}
	r.FinishedAt = time.Now().UTC()
	if len(errs) == 0 {
		if e = s.store.SetLastFullSync(ctx, r.FinishedAt); e != nil {
			return r, e
		}
	}
	return r, errors.Join(errs...)
}
func (s *Service) PollActive(ctx context.Context) error {
	now := time.Now()
	last := s.lastStalePoll.Load()
	stale := now.Sub(time.Unix(0, last)) >= s.StalePollEvery && s.lastStalePoll.CompareAndSwap(last, now.UnixNano())
	tasks, e := s.store.PollableTasks(ctx, stale)
	if e != nil {
		return e
	}
	for _, t := range tasks {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if t.State == db.StateLaunching {
			unlock := s.lock(t.ID)
			a, err := s.store.Attempt(ctx, t.ID, t.LaunchAttempt)
			if err == nil && (a.RetryAt == nil || !time.Now().Before(*a.RetryAt)) {
				_, err = s.resume(ctx, t, a)
			}
			unlock()
			if err != nil {
				s.log.Warn("reconcile launch", "task", t.ID, "err", err)
			}
		} else if t.Session != nil {
			if e = s.PollTask(ctx, t); e != nil {
				s.log.Warn("observe task", "task", t.ID, "err", e)
			}
		}
	}
	return nil
}
func (s *Service) PollTask(ctx context.Context, task db.Task) error {
	if task.Session == nil || task.State == db.StateLaunching {
		return nil
	}
	a, e := s.Adapter(task.ConnectionID)
	if e != nil {
		return e
	}
	call, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	o, e := a.Observe(call, *task.Session)
	if e != nil {
		_ = s.store.ObservationUnavailable(ctx, *task.Session, e.Error())
		return harnessErr(e)
	}
	if o.Session != *task.Session {
		return errors.New("observation returned a different session")
	}
	if e = s.store.PutObservation(ctx, o); e != nil {
		return e
	}
	o, e = s.store.Observation(ctx, *task.Session)
	if e != nil {
		return e
	}
	if (task.State == db.StateBacklog && !task.Adopted) || o.State == "idle" {
		return nil
	}
	change := db.Transition{TaskID: task.ID, From: []db.TaskState{task.State}, To: db.TaskState(o.State), Detail: "execution observed " + o.State}
	if task.Adopted && task.LaunchedAt == nil {
		change.ObservedStart = o.StartedAt
	}
	_, e = s.transition(ctx, change)
	return e
}
func (s *Service) RefreshCatalog(ctx context.Context) (*CatalogSnapshot, error) {
	a, e := s.Adapter(1)
	if e != nil {
		return nil, e
	}
	c, e := a.Features(ctx)
	if e != nil {
		return nil, harnessErr(e)
	}
	snap := &CatalogSnapshot{Catalog: c, FetchedAt: time.Now().UTC()}
	s.catalog.Store(snap)
	return snap, nil
}
func (s *Service) Catalog(ctx context.Context) (*CatalogSnapshot, error) {
	if snap := s.catalog.Load(); snap != nil {
		return snap, nil
	}
	return s.RefreshCatalog(ctx)
}
func (s *Service) StartTask(ctx context.Context, id int64) (db.Task, error) {
	return s.launch(ctx, id, "launch requested", false)
}
func (s *Service) LaunchTask(ctx context.Context, id int64) (db.Task, error) {
	return s.launch(ctx, id, "launch requested", true)
}
func (s *Service) LaunchScheduled(ctx context.Context, id int64, detail string) (db.Task, error) {
	return s.launch(ctx, id, detail, false)
}
func (s *Service) launch(ctx context.Context, id int64, detail string, automatic bool) (db.Task, error) {
	unlock := s.lock(id)
	defer unlock()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
	defer cancel()
	t, e := s.store.GetTask(ctx, id)
	if e != nil {
		return t, e
	}
	if t.State == db.StateLaunching {
		a, e := s.store.Attempt(ctx, id, t.LaunchAttempt)
		if e != nil {
			return t, e
		}
		return s.resume(ctx, t, a)
	}
	if t.Adopted {
		return t, db.Conflict("An adopted session cannot be launched. Open it in its harness.")
	}
	a, e := s.Adapter(t.ConnectionID)
	if e != nil {
		return t, e
	}
	if h := a.Health(ctx); !h.Available {
		return t, &db.ConflictError{Code: "connection_unavailable", Msg: h.Reason}
	}
	features, e := a.Features(ctx)
	if e != nil {
		return t, harnessErr(e)
	}
	if op := features.Operations["launch"]; !op.Available {
		return t, db.Conflict("Launch unavailable: %s", op.Reason)
	}
	if t.Workspace.FromPredecessor() {
		if op := features.Operations["predecessorBase"]; !op.Available {
			return t, db.Conflict("Predecessor base unavailable: %s", op.Reason)
		}
	}
	ps, e := a.Workspaces(ctx)
	if e != nil {
		return t, &db.ConflictError{Code: "connection_unavailable", Msg: "Cannot verify the workspace. Check its connection in Settings."}
	}
	if _, e = s.store.RefreshWorkspaces(ctx, t.ConnectionID, ps); e != nil {
		return t, e
	}
	// Resolve a requested base before claiming anything. No fallback is implicit.
	workspace, e := s.workspace(ctx, t, a)
	if e != nil {
		_ = s.store.NoteBlockedLaunch(ctx, id, e.Error())
		return t, e
	}
	config := t.ExecutionConfig
	if config == nil {
		decoder, ok := a.(execution.LegacyDecoder)
		if !ok {
			return t, db.Invalid("executionConfig is required for this harness")
		}
		raw, err := json.Marshal(t)
		if err != nil {
			return t, err
		}
		c, err := decoder.LegacyConfiguration(raw)
		if err != nil {
			return t, err
		}
		config = &c
	}
	claim, e := s.transition(ctx, db.Transition{TaskID: id, From: []db.TaskState{db.StateBacklog, db.StateFailed, db.StateInterrupted, db.StateCancelled}, To: db.StateLaunching, Detail: detail, BeginAttempt: true, RequireAuto: automatic})
	if e != nil {
		return t, e
	}
	if !claim.Changed {
		return t, db.Conflict("task is %s; cannot start", claim.From)
	}
	t, e = s.store.GetTask(ctx, id)
	if e != nil {
		return t, e
	}
	// Until the immutable intent is saved, no adapter effect is allowed.
	prepared := false
	defer func() {
		if !prepared {
			if att, err := s.store.Attempt(ctx, id, t.LaunchAttempt); err == nil && att.Intent == nil && att.Phase == "prepared" {
				_ = s.store.RejectAttempt(ctx, att, "Launch preparation failed before dispatch. Review the error and start again.")
			}
		}
	}()
	if t.Workspace.FromPredecessor() {
		pick := t.Workspace.BaseTaskID
		if pick == nil && len(t.Needs) == 1 {
			pick = &t.Needs[0]
		}
		if pick != nil {
			need, err := s.store.GetTask(ctx, *pick)
			if err != nil {
				return t, err
			}
			if err = s.store.RecordLaunchBase(ctx, id, db.LaunchBase{Session: need.Session, Attempt: t.LaunchAttempt, BaseRef: workspace.Base, Source: db.LaunchBasePredecessor, SourceTaskID: pick, SourceThreadID: need.T3ThreadID, SourceTitle: need.Title}); err != nil {
				return t, err
			}
		}
	}
	dest, e := s.store.LaunchDestination(ctx, id, t.LaunchAttempt)
	if e != nil {
		return t, e
	}
	handoff, e := s.prepareHandoff(ctx, t)
	if e != nil {
		return t, e
	}
	journal, e := s.store.JournalLaunchContext(ctx, t, journalInstructions(t, features.SessionEnvironment))
	if e != nil {
		return t, e
	}
	prompt := t.Prompt
	if handoff != "" {
		prompt += "\n\n" + handoff
	}
	prompt += "\n\n" + journal
	images, e := s.store.TaskAttachments(ctx, id)
	if e != nil {
		return t, e
	}
	in := execution.Intent{RequestID: claim.RequestID, ConnectionID: t.ConnectionID, Project: dest.ExternalProjectID, Directory: dest.Workspace, Title: t.Title, Prompt: prompt, Config: *config, Workspace: workspace, Images: []execution.Image{}}
	for _, img := range images {
		in.Images = append(in.Images, execution.Image{Name: img.Name, MimeType: img.MimeType, Data: img.Data})
	}
	if e = s.store.PrepareAttempt(ctx, id, t.LaunchAttempt, in); e != nil {
		return t, e
	}
	prepared = true
	attempt, e := s.store.Attempt(ctx, id, t.LaunchAttempt)
	if e != nil {
		return t, e
	}
	return s.resume(ctx, t, attempt)
}
func (s *Service) resume(ctx context.Context, t db.Task, attempt db.Attempt) (db.Task, error) {
	if attempt.Phase == "confirmed" || attempt.Phase == "rejected" {
		return s.store.GetTask(ctx, t.ID)
	}
	if attempt.Intent == nil && attempt.Phase == "prepared" {
		if e := s.store.RejectAttempt(ctx, attempt, "Launch preparation was interrupted before dispatch. Review the task and start again."); e != nil {
			return t, e
		}
		return s.store.GetTask(ctx, t.ID)
	}
	if attempt.Intent == nil {
		return t, db.Conflict("Launch identity is incomplete. Link the existing session.")
	}
	a, e := s.Adapter(attempt.Intent.ConnectionID)
	if e != nil {
		return t, e
	}
	call, cancel := context.WithTimeout(ctx, s.LaunchAttemptTimeout)
	defer cancel()
	workspaces, e := a.Workspaces(call)
	if e != nil {
		return t, harnessErr(e)
	}
	verified := false
	for _, w := range workspaces {
		if w.ID == attempt.Intent.Project && w.WorkspaceRoot == attempt.Intent.Directory && w.DeletedAt == nil {
			verified = true
		}
	}
	if !verified {
		return t, db.Conflict("The original launch workspace changed. Restore it before reconciling this attempt.")
	}
	var o execution.Observation
	for retry := 0; retry < 2; retry++ {
		o, e = a.Launch(call, *attempt.Intent, attempt.Checkpoint, func(ctx context.Context, cp execution.Checkpoint) error {
			return s.store.SaveCheckpoint(ctx, &attempt, cp)
		})
		var rejected *execution.Rejected
		if e == nil || errors.As(e, &rejected) || call.Err() != nil {
			break
		}
	}
	// Record the outcome even after the external timeout.
	record, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer done()
	if e == nil {
		e = o.Valid()
	}
	if e == nil && (o.Session.ConnectionID != attempt.Intent.ConnectionID || o.Project != attempt.Intent.Project) {
		e = errors.New("launch returned a different destination")
	}
	if e != nil {
		var rejection *execution.Rejected
		if errors.As(e, &rejection) {
			if err := s.store.RejectAttempt(record, attempt, e.Error()); err != nil {
				return t, err
			}
		} else {
			if err := s.store.Unconfirmed(record, attempt, e); err != nil {
				return t, err
			}
		}
		t, _ = s.store.GetTask(record, t.ID)
		return t, harnessErr(e)
	}
	if e = s.store.PutObservation(record, o); e != nil {
		_ = s.store.Unconfirmed(record, attempt, e)
		return t, e
	}
	if e = s.store.FinishAttempt(record, attempt, o, false); e != nil {
		return t, e
	}
	if s.OnTransition != nil {
		s.OnTransition(record, Change{TaskID: t.ID, From: db.StateLaunching, To: db.TaskState(o.State), Detail: "execution confirmed"})
	}
	return s.store.GetTask(record, t.ID)
}
func (s *Service) workspace(ctx context.Context, t db.Task, a execution.Adapter) (execution.Workspace, error) {
	w := execution.Workspace{Kind: t.Workspace.Type, Base: t.Workspace.BaseRef, FromRemote: strings.HasPrefix(t.Workspace.BaseRef, "origin/") || strings.HasPrefix(t.Workspace.BaseRef, "upstream/")}
	if t.Workspace.Branch != nil {
		w.Branch = *t.Workspace.Branch
	}
	if !t.Workspace.FromPredecessor() {
		return w, nil
	}
	var pick int64
	if t.Workspace.BaseTaskID != nil {
		pick = *t.Workspace.BaseTaskID
	} else if len(t.Needs) == 1 {
		pick = t.Needs[0]
	} else {
		return w, db.Conflict("Choose the predecessor or an explicit base before starting.")
	}
	need, e := s.store.GetTask(ctx, pick)
	if e != nil {
		return w, e
	}
	if need.ConnectionID != t.ConnectionID || need.ProjectID != t.ProjectID || need.Session == nil {
		return w, db.Conflict("The predecessor cannot supply a branch in this harness and project. Choose a base.")
	}
	o, e := a.Observe(ctx, *need.Session)
	if e != nil {
		return w, db.Conflict("Predecessor branch unavailable. Restore the connection or choose a base.")
	}
	if !o.Base.Available {
		return w, db.Conflict("%s Choose a base.", o.Base.Reason)
	}
	w.Base = o.Branch
	w.FromRemote = false
	return w, nil
}
