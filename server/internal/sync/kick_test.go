package sync_test

import (
	"context"
	"io"
	"log/slog"
	gosync "sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	t3sync "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// gateT3 counts full syncs and catalog reads; while gate is set, every
// ListThreads page after the first waits for it.
type gateT3 struct {
	*t3.Fake
	projectLists atomic.Int32
	catalogReads atomic.Int32
	pages        atomic.Int32
	mu           gosync.Mutex
	gate         chan struct{}
	paused       chan struct{} // receives once a page waits on gate
}

func (g *gateT3) ListProjects(ctx context.Context) ([]t3.Project, error) {
	g.projectLists.Add(1)
	return g.Fake.ListProjects(ctx)
}

func (g *gateT3) Capabilities(ctx context.Context) (t3.Catalog, error) {
	g.catalogReads.Add(1)
	return g.Fake.Capabilities(ctx)
}

func (g *gateT3) ListThreads(ctx context.Context, in t3.ListThreadsInput) (t3.ThreadPage, error) {
	if g.pages.Add(1) > 1 {
		g.mu.Lock()
		gate, paused := g.gate, g.paused
		g.mu.Unlock()
		if gate != nil {
			paused <- struct{}{}
			<-gate
		}
	}
	return g.Fake.ListThreads(ctx, in)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestKickRunsFullSyncAndCatalogNow(t *testing.T) {
	store := db.NewStore(testDB.Reset(t))
	client := &gateT3{Fake: t3.NewFake()}
	svc := t3sync.New(store, t3.NewAdapter(client), slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.FullSyncEvery, svc.CatalogEvery, svc.PollEvery = time.Hour, time.Hour, time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { svc.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	waitFor(t, "startup sync", func() bool { return client.projectLists.Load() == 1 && client.catalogReads.Load() == 1 })
	svc.Kick()
	svc.Kick() // coalesces with the pending kick
	waitFor(t, "kicked sync", func() bool { return client.projectLists.Load() >= 2 && client.catalogReads.Load() >= 2 })
	time.Sleep(50 * time.Millisecond)
	if n, c := client.projectLists.Load(), client.catalogReads.Load(); n > 3 || c > 3 {
		t.Fatalf("kicks ran %d syncs and %d catalog reads", n, c)
	}
}

func TestFullSyncDoesNotEnumerateHistory(t *testing.T) {
	store := db.NewStore(testDB.Reset(t))
	client := &gateT3{Fake: t3.NewFake()}
	p := client.AddProject("app", "/w/app")
	for range 150 {
		if _, err := client.LaunchThread(t.Context(), t3.LaunchInput{ProjectID: p.ID, Title: "external"}); err != nil {
			t.Fatal(err)
		}
	}
	svc := t3sync.New(store, t3.NewAdapter(client), slog.New(slog.NewTextHandler(io.Discard, nil)))
	r, err := svc.FullSync(t.Context())
	if err != nil || r.Threads != 0 || r.Projects != 1 || client.pages.Load() != 0 {
		t.Fatalf("sync=%+v pages=%d err=%v", r, client.pages.Load(), err)
	}
	if pr := svc.Progress(); pr.Running || pr.Threads != 0 {
		t.Fatalf("after sync %+v", pr)
	}
}
