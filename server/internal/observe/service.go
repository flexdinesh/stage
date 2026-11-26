// Package observe reconciles active sessions, streams and attention.
package observe

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/workevent"
)

type Coordinator interface {
	PollTask(context.Context, db.Task) error
	ReconcileLaunch(context.Context, db.Task) error
	Apply(context.Context, db.Task, execution.Observation) error
}

type Adapters interface {
	Adapter(int64) (execution.Adapter, error)
	All() map[int64]execution.Adapter
	Descriptor(string) (harness.Descriptor, bool)
	Harnesses() *harness.Registry
	Online(machineID int64) bool
}

type Options struct {
	Store       *db.Store
	Adapters    Adapters
	Coordinator Coordinator
	Log         *slog.Logger
}

type Service struct {
	store         *db.Store
	adapters      Adapters
	coord         Coordinator
	log           *slog.Logger
	streamErrors  sync.Map // connection ID to *atomic.Pointer[string]
	lastStalePoll sync.Map // connection ID to *atomic.Int64
	watchSeeded   sync.Map // connection IDs with a successful first observation pass
	started       time.Time
	kick          chan struct{}

	PollEvery       time.Duration
	StalePollEvery  time.Duration
	OnSessionChange func(context.Context, workevent.SessionChange)
}

func New(o Options) *Service {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	return &Service{
		store: o.Store, adapters: o.Adapters, coord: o.Coordinator, log: o.Log,
		started: time.Now(), kick: make(chan struct{}, 1),
		PollEvery: 5 * time.Second, StalePollEvery: time.Minute,
	}
}

// ConnectionsChanged follows a machine's adapters after its harness moved
// from connection old to next (an install, a replaced instance or a
// disconnect): old is no longer polled, watched or launched on.
func (s *Service) ConnectionsChanged(old, next int64) {
	s.streamErrors.Delete(old)
	if old != next {
		s.watchSeeded.Delete(old)
	}
	s.Kick()
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
	// Each connection owns its observation timers, independent of other connections.
	workers := map[int64]struct{}{}
	ensure := func() {
		for id := range s.adapters.All() {
			if _, ok := workers[id]; ok {
				continue
			}
			workers[id] = struct{}{}
			wg.Add(3)
			go loop(s.PollEvery, nil, func(ctx context.Context) error {
				_ = s.Stream(ctx, id) // Stream records and logs its own failures.
				return nil
			})
			go loop(s.PollEvery, nil, func(ctx context.Context) error { return s.pollConnection(ctx, id) })
			go loop(s.PollEvery, nil, func(ctx context.Context) error { return s.watchAttention(ctx, id) })
		}
	}
	ensure()
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
		case <-s.kick:
			ensure()
		}
	}
	wg.Wait()
}
