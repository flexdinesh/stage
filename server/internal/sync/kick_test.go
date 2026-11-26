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
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/execution"
	t3sync "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// gateT3 counts full syncs; while gate is set, every ListThreads page
// after the first waits for it.
type gateT3 struct {
	*t3.Fake
	projectLists atomic.Int32
	pages        atomic.Int32
	mu           gosync.Mutex
	gate         chan struct{}
	paused       chan struct{} // receives once a page waits on gate
}

func (g *gateT3) ListProjects(ctx context.Context) ([]t3.Project, error) {
	g.projectLists.Add(1)
	return g.Fake.ListProjects(ctx)
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

func TestKickRunsFullSyncNow(t *testing.T) {
	store := db.NewStore(testDB.Reset(t))
	client := &gateT3{Fake: t3.NewFake()}
	svc := t3sync.NewWithAdapters(store, nil, map[int64]execution.Adapter{dbtest.T3Connection: t3.NewAdapter(client)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.FullSyncEvery, svc.PollEvery = time.Hour, time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { svc.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	waitFor(t, "startup sync", func() bool { return client.projectLists.Load() == 1 })
	svc.Kick()
	svc.Kick() // coalesces with the pending kick
	waitFor(t, "kicked sync", func() bool { return client.projectLists.Load() >= 2 })
	time.Sleep(50 * time.Millisecond)
	if n := client.projectLists.Load(); n > 3 {
		t.Fatalf("kicks ran %d syncs", n)
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
	svc := t3sync.NewWithAdapters(store, nil, map[int64]execution.Adapter{dbtest.T3Connection: t3.NewAdapter(client)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r, err := svc.FullSync(t.Context())
	if err != nil || r.Sessions != 0 || r.Projects != 1 || client.pages.Load() != 0 {
		t.Fatalf("sync=%+v pages=%d err=%v", r, client.pages.Load(), err)
	}
	if pr := svc.Progress(store.ActiveID(db.LocalMachine, "t3")); pr.Running || pr.Sessions != 0 {
		t.Fatalf("after sync %+v", pr)
	}
	// The sync pass imports the discovered project once.
	ps, err := store.ListProjects(t.Context(), "all")
	if err != nil || len(ps) != 1 || ps[0].Title != "app" || len(ps[0].Bindings) != 1 || ps[0].Bindings[0].Rule != "new" {
		t.Fatalf("projects %+v %v", ps, err)
	}
	if _, err = svc.FullSync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if ps, err = store.ListProjects(t.Context(), "all"); err != nil || len(ps) != 1 {
		t.Fatalf("reimported %+v %v", ps, err)
	}
}
