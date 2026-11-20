// Package sync coordinates Stage-owned execution, discovery and recovery.
package sync

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"strings"
	gosync "sync"
	"sync/atomic"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/workevent"
)

type Change = workevent.TaskChange
type Result struct {
	Unavailable []int64   `json:"unavailableConnections,omitempty"`
	Projects    int       `json:"projects"`
	Threads     int       `json:"threads"`
	FinishedAt  time.Time `json:"finishedAt"`
}
type CatalogSnapshot struct {
	Catalog   execution.Capabilities
	FetchedAt time.Time
}

var ErrSyncRunning error = &db.ConflictError{Msg: "sync already running"}

type Service struct {
	adapterMu                                                                    gosync.RWMutex
	store                                                                        *db.Store
	adapters                                                                     map[int64]execution.Adapter
	log                                                                          *slog.Logger
	OnTransition                                                                 func(context.Context, Change)
	OnThreadChange                                                               func(context.Context, ThreadChange)
	OnAccepted                                                                   func()            // a completion was inferred (tree release may follow)
	Harnesses                                                                    *harness.Registry // descriptors by kind; completion is inferred only from a declared closure
	PublicURL                                                                    string            // STAGE_PUBLIC_URL, named in launch journal instructions when set
	CLI                                                                          string            // installed CLI name (install.CLIName), named in launch journal instructions
	FullSyncEvery, PollEvery, CatalogEvery, StalePollEvery, LaunchAttemptTimeout time.Duration
	progress                                                                     atomic.Pointer[Progress]
	lastStalePoll                                                                gosync.Map // connection ID to *atomic.Int64
	syncing                                                                      atomic.Bool
	catalog                                                                      atomic.Pointer[CatalogSnapshot]
	kickSync, kickCatalog                                                        chan struct{}
	locks                                                                        gosync.Map
	started                                                                      time.Time
	watchSeeded                                                                  gosync.Map // connection IDs with a successful first observation pass
}

func New(store *db.Store, adapter execution.Adapter, log *slog.Logger) *Service {
	return NewWithAdapters(store, map[int64]execution.Adapter{store.T3ID(): adapter}, log)
}
func NewWithAdapters(store *db.Store, adapters map[int64]execution.Adapter, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{store: store, adapters: adapters, log: log, FullSyncEvery: 5 * time.Minute, PollEvery: 5 * time.Second, CatalogEvery: 10 * time.Minute, StalePollEvery: time.Minute, LaunchAttemptTimeout: 40 * time.Second, kickSync: make(chan struct{}, 1), kickCatalog: make(chan struct{}, 1), started: time.Now()}
}
func (s *Service) Adapter(id int64) (execution.Adapter, error) {
	s.adapterMu.RLock()
	defer s.adapterMu.RUnlock()
	a := s.adapters[id]
	if a == nil {
		// A replaced instance keeps its history but is never reached again.
		if s.store.Harness(id) != "" && !s.store.ConnectionActive(id) {
			return nil, db.ConnectionRetired()
		}
		return nil, &db.ConflictError{Code: "connection_unavailable", Msg: "This harness is unavailable. Select a supported connection."}
	}
	return a, nil
}

// SetAdapter installs a connection's adapter; nil uninstalls it.
func (s *Service) SetAdapter(id int64, a execution.Adapter) {
	s.adapterMu.Lock()
	if a == nil {
		delete(s.adapters, id)
	} else {
		s.adapters[id] = a
	}
	s.adapterMu.Unlock()
	s.Kick()
}

// SwapAdapter moves a harness to a new connection after its instance
// changed: old is no longer polled, watched or launched on.
func (s *Service) SwapAdapter(old, next int64, a execution.Adapter) {
	s.adapterMu.Lock()
	if old != next {
		delete(s.adapters, old)
		s.watchSeeded.Delete(old)
	}
	if a == nil {
		delete(s.adapters, next) // disconnected
	} else {
		s.adapters[next] = a
	}
	s.adapterMu.Unlock()
	s.Kick()
}
func (s *Service) connections() map[int64]execution.Adapter {
	s.adapterMu.RLock()
	defer s.adapterMu.RUnlock()
	return maps.Clone(s.adapters)
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
			// An uninstalled (disconnected) connection's workers idle quietly.
			var unavailable *db.ConflictError
			if err := fn(ctx); err != nil && ctx.Err() == nil && !(errors.As(err, &unavailable) && unavailable.Code == "connection_unavailable") {
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
	// Each connection owns its poll and discovery timers. A slow or disconnected
	// service can consume its own timeout without delaying the other's next tick.
	workers := map[int64]chan struct{}{}
	ensure := func() {
		for id := range s.connections() {
			if _, ok := workers[id]; ok {
				continue
			}
			refresh := make(chan struct{}, 1)
			workers[id] = refresh
			wg.Add(4)
			go loop(s.FullSyncEvery, refresh, func(ctx context.Context) error { _, err := s.syncConnection(ctx, id); return err })
			go loop(s.PollEvery, nil, func(ctx context.Context) error { return s.Stream(ctx, id) })
			go loop(s.PollEvery, nil, func(ctx context.Context) error { return s.pollConnection(ctx, id) })
			go loop(s.PollEvery, nil, func(ctx context.Context) error { return s.watchConnection(ctx, id) })
		}
	}
	ensure()
	wg.Add(1)
	go loop(s.CatalogEvery, s.kickCatalog, func(ctx context.Context) error { _, e := s.RefreshCatalog(ctx); return e })
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
		case <-s.kickSync:
			for _, ch := range workers {
				select {
				case ch <- struct{}{}:
				default:
				}
			}
			ensure()
		}
	}
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
func (s *Service) FullSync(ctx context.Context, skip ...int64) (Result, error) {
	connections := s.connections()
	for _, id := range skip {
		delete(connections, id)
	}
	type response struct {
		id     int64
		result Result
		err    error
	}
	results := make(chan response, len(connections))
	for id := range connections {
		go func() { r, e := s.syncConnection(ctx, id); results <- response{id, r, e} }()
	}
	var out Result
	var errs []error
	for range connections {
		r := <-results
		out.Projects += r.result.Projects
		out.Threads += r.result.Threads
		if r.err != nil {
			out.Unavailable = append(out.Unavailable, r.id)
			errs = append(errs, r.err)
		}
	}
	out.FinishedAt = time.Now().UTC()
	slices.Sort(out.Unavailable)
	if len(errs) < len(connections) {
		return out, nil
	}
	return out, errors.Join(errs...)
}

func (s *Service) syncConnection(ctx context.Context, id int64) (Result, error) {
	now := time.Now().UTC()
	if s.store.IsT3(id) {
		if !s.syncing.CompareAndSwap(false, true) {
			return Result{}, ErrSyncRunning
		}
		s.progress.Store(&Progress{Running: true, StartedAt: &now})
		defer func() { s.progress.Store(&Progress{}); s.syncing.Store(false) }()
	}
	a, err := s.Adapter(id)
	if err != nil {
		return Result{}, err
	}
	inventory, err := s.inventory(ctx, id, a)
	if err != nil {
		return Result{}, err
	}
	n, err := s.store.RefreshWorkspaces(ctx, id, inventory)
	if err != nil {
		return Result{}, err
	}
	r := Result{Projects: n}
	if s.store.IsT3(id) {
		s.progress.Store(&Progress{Running: true, StartedAt: &now, Projects: n})
	}
	var errs []error
	// Import only after a complete, successful inventory refresh. A failed
	// import never blocks session polling.
	if imported, err := s.store.ImportWorkspaces(ctx, id); err != nil {
		errs = append(errs, err)
	} else if imported != (db.ImportResult{}) {
		s.log.Info("project import", "connection", id, "created", imported.Created, "bound", imported.Bound, "archived", imported.Archived, "unarchived", imported.Unarchived)
	}
	tasks, err := s.store.ListTasks(ctx, db.TaskFilter{Closed: "all"})
	if err != nil {
		return r, errors.Join(append(errs, err)...)
	}
	for _, t := range tasks {
		if t.ConnectionID != id || t.Session == nil {
			continue
		}
		if err := s.PollTask(ctx, t); err != nil {
			errs = append(errs, err)
		} else {
			r.Threads++
		}
	}
	r.FinishedAt = time.Now().UTC()
	if s.store.IsT3(id) && len(errs) == 0 {
		errs = append(errs, s.store.SetLastFullSync(ctx, r.FinishedAt))
	}
	return r, errors.Join(errs...)
}
func (s *Service) PollActive(ctx context.Context) error {
	var errs []error
	for id := range s.connections() {
		errs = append(errs, s.pollConnection(ctx, id))
	}
	return errors.Join(errs...)
}
func (s *Service) pollConnection(ctx context.Context, id int64) error {
	v, _ := s.lastStalePoll.LoadOrStore(id, &atomic.Int64{})
	lastPoll := v.(*atomic.Int64)
	now := time.Now()
	last := lastPoll.Load()
	stale := now.Sub(time.Unix(0, last)) >= s.StalePollEvery && lastPoll.CompareAndSwap(last, now.UnixNano())
	tasks, e := s.store.PollableTasks(ctx, stale)
	if e != nil {
		return e
	}
	for _, t := range tasks {
		if t.ConnectionID != id {
			continue
		}
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
	return s.apply(ctx, task, o)
}

// apply records an observation of task's session, polled or pushed, and
// moves the task to its state.
func (s *Service) apply(ctx context.Context, task db.Task, o execution.Observation) error {
	if o.Session != *task.Session {
		return errors.New("observation returned a different session")
	}
	if e := s.checkObservation(task.Harness, o); e != nil {
		return e
	}
	if e := s.store.PutObservation(ctx, o); e != nil {
		return e
	}
	o, e := s.store.Observation(ctx, *task.Session)
	if e != nil {
		return e
	}
	if (task.State == db.StateBacklog && !task.Adopted) || o.State == "idle" {
		return nil
	}
	detail := "execution observed " + o.State
	if o.StateReason != "" {
		detail += " (" + o.StateReason + ")"
	}
	change := db.Transition{TaskID: task.ID, From: []db.TaskState{task.State}, To: db.TaskState(o.State), Detail: detail}
	if task.Adopted && task.LaunchedAt == nil {
		change.ObservedStart = o.StartedAt
	}
	if _, e = s.transition(ctx, change); e != nil {
		return e
	}
	closure, ok := s.closure(task.Harness, o)
	if !ok {
		return nil
	}
	inferred, e := s.store.InferCompletion(ctx, task.ID, o, closure)
	if e != nil || !inferred {
		return e
	}
	s.log.Info("completion inferred from harness closure", "task", task.ID, "signal", o.Closed.Signal, "execution", o.ExecutionID)
	if s.OnAccepted != nil {
		s.OnAccepted()
	}
	return nil
}

// checkObservation validates o against kind's policies: only a harness that
// owns its processes may report an execution lost with its owner.
func (s *Service) checkObservation(kind string, o execution.Observation) error {
	if err := o.Valid(); err != nil {
		return err
	}
	if o.StateReason == execution.ReasonOwnerExited {
		if d, ok := s.descriptor(kind); !ok || !d.OwnsProcesses() {
			return errors.New("malformed observation: only a harness that owns its processes may report " + execution.ReasonOwnerExited)
		}
	}
	return nil
}

// descriptor is kind's registered descriptor.
func (s *Service) descriptor(kind string) (harness.Descriptor, bool) {
	if s.Harnesses == nil {
		return harness.Descriptor{}, false
	}
	return s.Harnesses.Lookup(kind)
}

// Stream ingests connection id's pushed observations while its adapter
// streams them (execution.EventSource); it returns at once for an adapter
// that does not push. Polling reconciles whatever a stream misses.
func (s *Service) Stream(ctx context.Context, id int64) error {
	a, err := s.Adapter(id)
	if err != nil {
		return err
	}
	src, ok := a.(execution.EventSource)
	if !ok {
		return nil
	}
	return src.Events(ctx, func(ctx context.Context, o execution.Observation) error {
		if o.Session.ConnectionID != id {
			return errors.New("pushed observation names another connection")
		}
		return s.Ingest(ctx, o)
	})
}

// Ingest applies a pushed observation to the task that owns its session.
// An unowned session is left to discovery.
func (s *Service) Ingest(ctx context.Context, o execution.Observation) error {
	t, err := s.store.TaskBySession(ctx, o.Session)
	if errors.Is(err, db.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if t.State == db.StateLaunching {
		return nil // the launch reconciles its own session
	}
	return s.apply(ctx, t, o)
}

// closure is the harness's declared closure for o's signal.
func (s *Service) closure(kind string, o execution.Observation) (harness.Closure, bool) {
	d, ok := s.descriptor(kind)
	if o.Closed == nil || !ok {
		return harness.Closure{}, false
	}
	return d.Closure(o.Closed.Signal)
}

// attribution is kind's caller attribution (zero when unknown).
func (s *Service) attribution(kind string) harness.Attribution {
	d, _ := s.descriptor(kind)
	return d.Attribution
}

func (s *Service) RefreshCatalog(ctx context.Context) (*CatalogSnapshot, error) {
	// The legacy catalog is the active T3 connection's (GET /api/catalog),
	// wherever T3 moved after a replaced instance.
	a, e := s.Adapter(s.store.T3ID())
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
	d, _ := s.descriptor(t.Harness)
	correlated := d.Launch == harness.LaunchCorrelated
	if _, ok := a.(execution.Correlator); correlated && !ok {
		return t, db.Conflict("Launch unavailable: this harness cannot reconcile a launch it does not reserve.")
	}
	if t.Workspace.FromPredecessor() {
		if op := features.Operations["predecessorBase"]; !op.Available {
			return t, db.Conflict("Predecessor base unavailable: %s", op.Reason)
		}
	}
	ps, e := s.inventory(ctx, t.ConnectionID, a)
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
	journal, e := s.store.JournalLaunchContext(ctx, t, journalInstructions(t, s.attribution(t.Harness), s.PublicURL, s.CLI))
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
	if correlated {
		in.Marker = execution.LaunchMarker(claim.RequestID)
		in.Prompt += "\n\n" + in.Marker
	}
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
	save := func(ctx context.Context, cp execution.Checkpoint) error {
		return s.store.SaveCheckpoint(ctx, &attempt, cp)
	}
	var o execution.Observation
	if attempt.Intent.Marker != "" {
		o, e = s.correlated(call, a, &attempt, save)
	} else {
		for retry := 0; retry < 2; retry++ {
			o, e = a.Launch(call, *attempt.Intent, attempt.Checkpoint, save)
			var rejected *execution.Rejected
			if e == nil || errors.As(e, &rejected) || call.Err() != nil {
				break
			}
		}
	}
	// Record the outcome even after the external timeout.
	record, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer done()
	if e == nil {
		e = s.checkObservation(t.Harness, o)
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
// correlated dispatches a correlated launch once: the dispatch is recorded
// (the attempt leaves prepared) before the harness creates anything. Once
// dispatched, the outcome is found by the intent's marker and never launched
// again; not found keeps the attempt unconfirmed until the user links it.
func (s *Service) correlated(ctx context.Context, a execution.Adapter, attempt *db.Attempt, save execution.SaveCheckpoint) (execution.Observation, error) {
	if attempt.Phase == "prepared" {
		if err := save(ctx, attempt.Checkpoint); err != nil {
			return execution.Observation{}, err
		}
		return a.Launch(ctx, *attempt.Intent, attempt.Checkpoint, save)
	}
	c, ok := a.(execution.Correlator)
	if !ok {
		return execution.Observation{}, errors.New("this harness cannot find a correlated launch; link the session")
	}
	o, err := c.Correlate(ctx, *attempt.Intent)
	if errors.Is(err, execution.ErrNotFound) {
		return o, errors.New("no session carries this launch's marker; if it started, link the session")
	}
	return o, err
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
