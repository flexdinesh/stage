package serve

import (
	"context"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
)

// activeConnections lists the active connections of every machine
// (db.Store.ActiveConnections).
type activeConnections interface {
	ActiveConnections() []db.ActiveConnection
}

// perConnection runs one runner per active harness connection, on every
// machine: release and schedule runners reconcile one connection each, so
// machines and harness kinds never wait on each other. The set follows the
// active connections: on start, on Resync and every Every, a runner starts
// for each new active connection and stops when its connection retires.
type perConnection[R interface{ Run(context.Context) }] struct {
	store activeConnections
	build func(connection int64) R
	// Every is how often the set is checked against the active connections.
	Every time.Duration

	mu      sync.Mutex
	running map[int64]runningRunner[R]
	resync  chan struct{}
}

type runningRunner[R any] struct {
	runner R
	stop   context.CancelFunc
	done   chan struct{}
}

func newPerConnection[R interface{ Run(context.Context) }](store activeConnections, build func(connection int64) R) *perConnection[R] {
	return &perConnection[R]{store: store, build: build, Every: 10 * time.Second, running: map[int64]runningRunner[R]{}, resync: make(chan struct{}, 1)}
}

// Resync schedules a check of the set (an instance switch). Never blocks.
func (p *perConnection[R]) Resync() {
	select {
	case p.resync <- struct{}{}:
	default:
	}
}

// Each calls fn for each running runner.
func (p *perConnection[R]) Each(fn func(R)) {
	p.mu.Lock()
	runners := make([]R, 0, len(p.running))
	for _, r := range p.running {
		runners = append(runners, r.runner)
	}
	p.mu.Unlock()
	for _, r := range runners {
		fn(r)
	}
}

// Connections lists the connections with a running runner.
func (p *perConnection[R]) Connections() map[int64]bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[int64]bool, len(p.running))
	for id := range p.running {
		out[id] = true
	}
	return out
}

// Run keeps the set in step until ctx is done, then stops every runner and
// waits for them.
func (p *perConnection[R]) Run(ctx context.Context) {
	t := time.NewTicker(p.Every)
	defer t.Stop()
	for {
		p.sync(ctx)
		select {
		case <-ctx.Done():
			p.stopAll()
			return
		case <-p.resync:
		case <-t.C:
		}
	}
}

func (p *perConnection[R]) sync(ctx context.Context) {
	active := map[int64]bool{}
	for _, c := range p.store.ActiveConnections() {
		active[c.ID] = true
	}
	var stopped []runningRunner[R]
	p.mu.Lock()
	for id, r := range p.running {
		if !active[id] {
			r.stop()
			stopped = append(stopped, r)
			delete(p.running, id)
		}
	}
	for id := range active {
		if _, ok := p.running[id]; ok || ctx.Err() != nil {
			continue
		}
		runner := p.build(id)
		rctx, stop := context.WithCancel(ctx)
		done := make(chan struct{})
		p.running[id] = runningRunner[R]{runner: runner, stop: stop, done: done}
		go func() {
			defer close(done)
			runner.Run(rctx)
		}()
	}
	p.mu.Unlock()
	for _, r := range stopped {
		<-r.done
	}
}

func (p *perConnection[R]) stopAll() {
	p.mu.Lock()
	running := p.running
	p.running = map[int64]runningRunner[R]{}
	p.mu.Unlock()
	for _, r := range running {
		r.stop()
	}
	for _, r := range running {
		<-r.done
	}
}
