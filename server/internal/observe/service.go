// Package observe reconciles active sessions, streams and attention.
package observe

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/workevent"
)

type Coordinator interface {
	PollTask(context.Context, db.Task) error
	ReconcileLaunch(context.Context, db.Task) error
	CheckObservation(string, execution.Observation) error
	ApplyObservation(context.Context, *db.Store, db.Task, execution.Observation, time.Time, time.Time) (*workevent.TaskChange, bool, error)
	PublishObservation(context.Context, *workevent.TaskChange, bool, bool)
}

type Adapters interface {
	Adapter(int64) (execution.Adapter, error)
	All() map[int64]execution.Adapter
	Descriptor(string) (harness.Descriptor, bool)
	Online(machineID int64) bool
	InProcess(machineID int64) bool
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
	runner        atomic.Pointer[Runner]
	workers       WatchTransport
	resamples     sync.Map // machine ID to a resample request counter

	PollEvery          time.Duration
	StalePollEvery     time.Duration
	OnSessionChange    func(context.Context, workevent.SessionChange)
	RecordNotification func(context.Context, *db.Store, workevent.ObservationChange) (func(), error)
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
	if runner := s.runner.Load(); runner != nil && old == next {
		runner.Reset(next)
	}
	s.Kick()
}
func (s *Service) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}
