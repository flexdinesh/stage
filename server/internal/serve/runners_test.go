package serve

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
)

type fakeConnections struct {
	mu     sync.Mutex
	active []db.ActiveConnection
}

func (f *fakeConnections) ActiveConnections() []db.ActiveConnection {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]db.ActiveConnection(nil), f.active...)
}

func (f *fakeConnections) set(active ...db.ActiveConnection) {
	f.mu.Lock()
	f.active = active
	f.mu.Unlock()
}

// fakeRunner records whether it is running.
type fakeRunner struct {
	connection int64
	live       *sync.Map // connection to running
}

func (r fakeRunner) Run(ctx context.Context) {
	r.live.Store(r.connection, true)
	<-ctx.Done()
	r.live.Delete(r.connection)
}

func liveSet(live *sync.Map) map[int64]bool {
	out := map[int64]bool{}
	live.Range(func(k, _ any) bool {
		id, _ := k.(int64)
		out[id] = true
		return true
	})
	return out
}

func waitLive(t *testing.T, live *sync.Map, want ...int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := liveSet(live)
		ok := len(got) == len(want)
		for _, id := range want {
			ok = ok && got[id]
		}
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("running %v, want %v", got, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestPerConnectionRunsOneRunnerPerActiveConnection(t *testing.T) {
	conns := &fakeConnections{}
	// Two machines, each with an active connection of the same kind.
	conns.set(db.ActiveConnection{ID: 1, Machine: 1, Harness: "t3"}, db.ActiveConnection{ID: 4, Machine: 2, Harness: "t3"})
	var live sync.Map
	var mu sync.Mutex
	built := map[int64]int{}
	p := newPerConnection(conns, func(id int64) fakeRunner {
		mu.Lock()
		built[id]++
		mu.Unlock()
		return fakeRunner{connection: id, live: &live}
	})
	p.Every = time.Hour // only Resync moves the set
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	waitLive(t, &live, 1, 4)

	// The laptop's instance is replaced: its old runner stops, the new
	// connection gets one; the local runner keeps running.
	conns.set(db.ActiveConnection{ID: 1, Machine: 1, Harness: "t3"}, db.ActiveConnection{ID: 5, Machine: 2, Harness: "t3"})
	p.Resync()
	waitLive(t, &live, 1, 5)
	if got := p.Connections(); len(got) != 2 || !got[1] || !got[5] {
		t.Fatalf("connections %v", got)
	}
	n := 0
	p.Each(func(fakeRunner) { n++ })
	if n != 2 {
		t.Fatalf("each visited %d", n)
	}
	mu.Lock()
	if built[1] != 1 || built[4] != 1 || built[5] != 1 {
		t.Fatalf("built %v", built)
	}
	mu.Unlock()

	// Shutdown stops every runner before Run returns.
	cancel()
	<-done
	if got := liveSet(&live); len(got) != 0 {
		t.Fatalf("running after shutdown %v", got)
	}
}
