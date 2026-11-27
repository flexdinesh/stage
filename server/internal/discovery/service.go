// Package discovery refreshes harness workspace evidence and reconciles projects.
package discovery

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/machine"
)

type Poller interface {
	PollTask(context.Context, db.Task) error
}

type Service struct {
	*Reader
	store  *db.Store
	conns  *machine.Connections
	poller Poller
	log    *slog.Logger
	syncs  sync.Map // connection ID to *syncState (full syncs)
	kick   chan struct{}

	FullSyncEvery time.Duration
	OnFullSync    func() // a full sync (Sync now) finished
	// OnConnectionSynced runs after a connection's full sync succeeded
	// (the composition records it where a harness's setup shows it).
	OnConnectionSynced func(ctx context.Context, connection int64, at time.Time) error
}

type Options struct {
	Store       *db.Store
	Connections *machine.Connections
	Reader      *Reader
	Poller      Poller
	Log         *slog.Logger
}

func New(o Options) *Service {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	return &Service{
		Reader: o.Reader, store: o.Store, conns: o.Connections,
		poller: o.Poller, log: o.Log, kick: make(chan struct{}, 1), FullSyncEvery: 5 * time.Minute,
	}
}

type Result struct {
	Unavailable []int64   `json:"unavailableConnections,omitempty"`
	Projects    int       `json:"projects"`
	Sessions    int       `json:"sessions"`
	FinishedAt  time.Time `json:"finishedAt"`
}

var ErrSyncRunning error = &db.ConflictError{Msg: "sync already running"}

type Progress struct {
	Running            bool
	StartedAt          *time.Time
	Projects, Sessions int
}

// syncState is one connection's full sync: at most one runs per connection.
type syncState struct {
	running  atomic.Bool
	progress atomic.Pointer[Progress]
}

func (s *Service) syncState(id int64) *syncState {
	v, _ := s.syncs.LoadOrStore(id, &syncState{})
	st, _ := v.(*syncState)
	return st
}

// Progress is connection id's full sync progress (a harness's setup may
// show it).
func (s *Service) Progress(id int64) Progress {
	if p := s.syncState(id).progress.Load(); p != nil {
		return *p
	}
	return Progress{}
}

func (s *Service) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

func (s *Service) Run(ctx context.Context) {
	var wg sync.WaitGroup
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
	// Each connection owns its discovery timer, independent of other connections.
	workers := map[int64]chan struct{}{}
	ensure := func() {
		for id := range s.conns.All() {
			if _, ok := workers[id]; ok {
				continue
			}
			refresh := make(chan struct{}, 1)
			workers[id] = refresh
			wg.Add(1)
			go loop(s.FullSyncEvery, refresh, func(ctx context.Context) error { _, err := s.syncConnection(ctx, id, false); return err })
		}
	}
	ensure()
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
		case <-s.kick:
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
func (s *Service) FullSync(ctx context.Context, skip ...int64) (Result, error) {
	connections := s.conns.All()
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
		go func() { r, e := s.syncConnection(ctx, id, true); results <- response{id, r, e} }()
	}
	var out Result
	var errs []error
	for range connections {
		r := <-results
		out.Projects += r.result.Projects
		out.Sessions += r.result.Sessions
		if r.err != nil {
			out.Unavailable = append(out.Unavailable, r.id)
			errs = append(errs, r.err)
		}
	}
	out.FinishedAt = time.Now().UTC()
	slices.Sort(out.Unavailable)
	if s.OnFullSync != nil {
		s.OnFullSync()
	}
	if len(errs) < len(connections) {
		return out, nil
	}
	return out, errors.Join(errs...)
}

func (s *Service) syncConnection(ctx context.Context, id int64, freshSessions bool) (Result, error) {
	now := time.Now().UTC()
	st := s.syncState(id)
	if !st.running.CompareAndSwap(false, true) {
		return Result{}, ErrSyncRunning
	}
	st.progress.Store(&Progress{Running: true, StartedAt: &now})
	defer func() { st.progress.Store(&Progress{}); st.running.Store(false) }()
	r := Result{}
	var errs []error
	_, projects, err := s.RefreshInventory(ctx, id)
	r.Projects = projects
	if err != nil {
		errs = append(errs, err)
	} else {
		st.progress.Store(&Progress{Running: true, StartedAt: &now, Projects: r.Projects})
		if imported, err := s.store.ImportWorkspaces(ctx, id); err != nil {
			errs = append(errs, err)
		} else if imported != (db.ImportResult{}) {
			s.log.Info("project import", "connection", id, "created", imported.Created, "bound", imported.Bound, "moved", imported.Moved)
		}
	}
	// Sync now can request a fresh session read in addition to inventory.
	// Background discovery leaves session sampling to the watch runner.
	if freshSessions {
		tasks, err := s.store.ListTasks(ctx, db.TaskFilter{Closed: "all"})
		if err != nil {
			return r, errors.Join(append(errs, err)...)
		}
		for _, t := range tasks {
			if t.ConnectionID != id || t.Session == nil {
				continue
			}
			if err := s.poller.PollTask(ctx, t); err != nil {
				errs = append(errs, err)
			} else {
				r.Sessions++
			}
		}
	}
	r.FinishedAt = time.Now().UTC()
	if len(errs) == 0 {
		machine, kind := s.store.ConnectionMachine(id), s.store.Harness(id)
		if machine != 0 && s.store.ActiveID(machine, kind) == id {
			errs = append(errs, s.store.SetHarnessLastFullSync(ctx, machine, kind, r.FinishedAt))
		}
		if s.OnConnectionSynced != nil {
			errs = append(errs, s.OnConnectionSynced(ctx, id, r.FinishedAt))
		}
	}
	return r, errors.Join(errs...)
}
