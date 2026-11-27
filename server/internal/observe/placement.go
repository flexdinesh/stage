package observe

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
)

// WatchTransport connects the planner to machine-scoped observation workers.
type WatchTransport interface {
	Attachments() map[int64]WatchAttachment
	ReplaceWatch(context.Context, int64, string, Watch) error
}

type WatchAttachment struct {
	Session     string
	Supported   bool
	Connections []int64
}

// SetWatchTransport configures worker observation before Run starts.
func (s *Service) SetWatchTransport(workers WatchTransport) {
	s.workers = workers
}

func (s *Service) RequestResample(machine int64) {
	v, _ := s.resamples.LoadOrStore(machine, &atomic.Uint64{})
	v.(*atomic.Uint64).Add(1)
	s.Kick()
}

func (s *Service) BindWorkerProducer(ctx context.Context, machine int64, producer string) error {
	if !validProducer(producer) {
		return db.ErrObservationProducer
	}
	return s.store.BindObservationProducer(ctx, machine, producer)
}

// Run maintains desired watches and reconciles unfinished launch commands.
// All periodic session reads and event subscriptions belong to the runner
// on the adapter's machine.
func (s *Service) Run(ctx context.Context) {
	local := NewRunner(s.adapters.Adapter, s.emitLocal, RunnerOptions{
		PollEvery: s.PollEvery, StalePollEvery: s.StalePollEvery, Log: s.log,
	})
	s.runner.Store(local)
	defer s.runner.Store(nil)
	var wg sync.WaitGroup
	wg.Go(func() { local.Run(ctx) })
	defer wg.Wait()

	epoch := rand.Text()
	var generation int64
	remotes := make(map[int64]*remoteWatch)
	var lastLocal []WatchConnection
	var reconciling sync.Map
	ticker := time.NewTicker(s.PollEvery)
	defer ticker.Stop()
	for ctx.Err() == nil {
		generation++
		var attachments map[int64]WatchAttachment
		if s.workers != nil {
			attachments = s.workers.Attachments()
		}
		here, there, launches, err := s.desired(ctx, attachments)
		if err != nil {
			if ctx.Err() == nil {
				s.log.Warn("build observation watch", "err", err)
			}
		} else {
			if !slices.EqualFunc(lastLocal, here, sameWatchConnection) {
				local.Replace(Watch{Epoch: epoch, Generation: generation, Connections: here})
				lastLocal = here
			}
			s.updateRemoteWatches(ctx, &wg, remotes, attachments, there, epoch, generation)
			for id, tasks := range launches {
				if _, loaded := reconciling.LoadOrStore(id, true); loaded {
					continue
				}
				wg.Go(func() {
					defer reconciling.Delete(id)
					for _, task := range tasks {
						if ctx.Err() != nil {
							return
						}
						if err := s.coord.ReconcileLaunch(ctx, task); err != nil && ctx.Err() == nil {
							s.log.Warn("reconcile launch", "task", task.ID, "err", err)
						}
					}
				})
			}
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		case <-s.kick:
		}
	}
}

func (s *Service) emitLocal(ctx context.Context, r Record) error {
	_, err := s.ingestLocal(ctx, r)
	var invalid *db.InvalidError
	var conflict *db.ConflictError
	if errors.As(err, &invalid) || errors.As(err, &conflict) && conflict.Code == "connection_retired" || errors.Is(err, db.ErrNotFound) {
		return nil
	}
	return err
}

func (s *Service) desired(ctx context.Context, attachments map[int64]WatchAttachment) ([]WatchConnection, map[int64][]WatchConnection, map[int64][]db.Task, error) {
	fast, err := s.store.PollableTasks(ctx, false)
	if err != nil {
		return nil, nil, nil, err
	}
	all, err := s.store.PollableTasks(ctx, true)
	if err != nil {
		return nil, nil, nil, err
	}
	fastIDs := make(map[int64]bool, len(fast))
	for _, task := range fast {
		fastIDs[task.ID] = true
	}
	sessions := make(map[int64]map[string]WatchSession)
	launches := make(map[int64][]db.Task)
	hosted := s.adapters.All()
	for _, task := range all {
		id := task.ConnectionID
		if task.State == db.StateLaunching {
			if _, ok := hosted[id]; ok && s.adapters.Online(s.store.ConnectionMachine(id)) {
				launches[id] = append(launches[id], task)
			}
		} else if task.Session != nil {
			if sessions[id] == nil {
				sessions[id] = make(map[string]WatchSession)
			}
			ref := task.Session.ExternalSessionID
			sessions[id][ref] = WatchSession{ID: ref, Stale: !fastIDs[task.ID]}
		}
	}
	local := []WatchConnection{}
	remote := make(map[int64][]WatchConnection)
	for _, c := range s.store.ActiveConnections() {
		inProcess := s.adapters.InProcess(c.Machine)
		if inProcess {
			if _, ok := hosted[c.ID]; !ok {
				continue
			}
		} else if !s.hasRemoteAdapter(attachments[c.Machine], c.ID) {
			continue
		}
		retained, err := s.store.WatchedSessions(ctx, c.ID)
		if err != nil {
			return nil, nil, nil, err
		}
		w := WatchConnection{ID: c.ID, Attention: true, Sessions: make([]WatchSession, 0, len(sessions[c.ID])+len(retained))}
		for _, session := range sessions[c.ID] {
			w.Sessions = append(w.Sessions, session)
		}
		for _, ref := range retained {
			if _, ok := sessions[c.ID][ref.ExternalSessionID]; !ok {
				w.Sessions = append(w.Sessions, WatchSession{ID: ref.ExternalSessionID, Attention: true})
			}
		}
		slices.SortFunc(w.Sessions, func(a, b WatchSession) int { return cmp.Compare(a.ID, b.ID) })
		if inProcess {
			local = append(local, w)
		} else {
			remote[c.Machine] = append(remote[c.Machine], w)
		}
	}
	sortConnections := func(w []WatchConnection) {
		slices.SortFunc(w, func(a, b WatchConnection) int { return cmp.Compare(a.ID, b.ID) })
	}
	sortConnections(local)
	for _, w := range remote {
		sortConnections(w)
	}
	return local, remote, launches, nil
}

func (s *Service) hasRemoteAdapter(a WatchAttachment, id int64) bool {
	if a.Session == "" {
		return false
	}
	if !a.Supported {
		msg := "Update Stage on the worker machine to enable observation."
		v, _ := s.streamErrors.LoadOrStore(id, &atomic.Pointer[string]{})
		v.(*atomic.Pointer[string]).Store(&msg)
		return false
	}
	return slices.Contains(a.Connections, id)
}

type remoteWatch struct {
	mu       sync.Mutex
	sending  bool
	session  string
	last     []WatchConnection
	resample uint64
}

func (s *Service) updateRemoteWatches(ctx context.Context, wg *sync.WaitGroup, states map[int64]*remoteWatch,
	attachments map[int64]WatchAttachment, desired map[int64][]WatchConnection, epoch string, generation int64) {
	for machine := range states {
		if _, connected := attachments[machine]; !connected {
			delete(states, machine)
		}
	}
	for machine, attachment := range attachments {
		if !attachment.Supported {
			continue
		}
		state := states[machine]
		if state == nil {
			state = &remoteWatch{}
			states[machine] = state
		}
		connections := desired[machine]
		if connections == nil {
			connections = []WatchConnection{}
		}
		var resample uint64
		if v, ok := s.resamples.Load(machine); ok {
			resample = v.(*atomic.Uint64).Load()
		}
		state.mu.Lock()
		force := state.resample != resample
		if state.sending || state.session == attachment.Session && !force && slices.EqualFunc(state.last, connections, sameWatchConnection) {
			state.mu.Unlock()
			continue
		}
		state.sending = true
		state.mu.Unlock()
		watch := Watch{Epoch: epoch, Generation: generation, Connections: connections, Resample: force}
		wg.Go(func() {
			err := s.workers.ReplaceWatch(ctx, machine, attachment.Session, watch)
			state.mu.Lock()
			state.sending = false
			if err == nil {
				state.session, state.last, state.resample = attachment.Session, connections, resample
			}
			state.mu.Unlock()
			if err != nil && ctx.Err() == nil && s.workers.Attachments()[machine].Session == attachment.Session {
				s.log.Warn("send observation watch", "machine", machine, "err", err)
			}
		})
	}
}
