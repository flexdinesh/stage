package coordinator_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	gosync "sync"
	"testing"

	"github.com/flexdinesh/stage/server/internal/coordinator"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// readsT3 records every thread read against one T3.
type readsT3 struct {
	*t3.Fake
	mu    gosync.Mutex
	reads []string
}

func (r *readsT3) ReadThread(ctx context.Context, id string) (t3.ThreadDetail, error) {
	r.mu.Lock()
	r.reads = append(r.reads, id)
	r.mu.Unlock()
	return r.Fake.ReadThread(ctx, id)
}

func (r *readsT3) read(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, got := range r.reads {
		if got == id {
			return true
		}
	}
	return false
}

func TestReplacedT3IsNeverPolledAgainstItsReplacement(t *testing.T) {
	store := db.NewStore(testDB.Reset(t))
	ctx := context.Background()
	a := &readsT3{Fake: t3.NewFake()}
	first := store.ActiveID(db.LocalMachine, "t3")
	if _, err := store.SwitchInstance(ctx, db.LocalMachine, t3.Descriptor, "env-a", "box a", 0); err != nil {
		t.Fatal(err)
	}
	external := a.AddProject("app", "/w")
	p, err := store.InsertProject(ctx, db.Project{Title: "app", Bindings: []db.ProjectBinding{{ConnectionID: first, ExternalProjectID: external.ID, Workspace: "/w"}}})
	if err != nil {
		t.Fatal(err)
	}
	svc := coordinator.NewWithAdapters(store, t3Harnesses, map[int64]execution.Adapter{dbtest.T3Connection: t3.NewConnectionAdapter(a, first)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	task, err := store.CreateTask(ctx, db.NewTask{ProjectID: p.ID, ConnectionID: first, Title: "run", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if task, err = svc.StartTask(ctx, task.ID); err != nil || task.Session == nil {
		t.Fatalf("start %+v %v", task, err)
	}
	thread := task.Session.ExternalSessionID
	if err := a.SetStatus(thread, t3.StatusRunning); err != nil {
		t.Fatal(err)
	}
	if err := svc.PollActive(ctx); err != nil {
		t.Fatal(err)
	}
	if err := svc.WatchAttention(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := store.GetTask(ctx, task.ID)
	if err != nil || before.State != db.StateRunning {
		t.Fatalf("before %+v %v", before, err)
	}

	// T3 B replaces A. B happens to serve a thread with A's id.
	sw, err := store.SwitchInstance(ctx, db.LocalMachine, t3.Descriptor, "env-b", "box b", 0)
	if err != nil || !sw.Changed {
		t.Fatalf("switch %+v %v", sw, err)
	}
	b := &readsT3{Fake: t3.NewFake()}
	local, _ := svc.Machines().Local(db.LocalMachine)
	local.Install(sw.Old, sw.New, t3.NewConnectionAdapter(b, sw.New))
	if err := a.SetStatus(thread, t3.StatusIdle); err != nil {
		t.Fatal(err)
	}

	if err := svc.PollActive(ctx); err != nil {
		t.Fatal(err)
	}
	if err := svc.WatchAttention(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.FullSync(ctx); err != nil {
		t.Fatal(err)
	}
	if b.read(thread) {
		t.Fatal("A's thread read on B")
	}
	after, err := store.GetTask(ctx, task.ID)
	if err != nil || after.State != db.StateRunning || after.ConnectionID != first || after.ConnectionActive {
		t.Fatalf("replaced task changed %+v %v", after, err)
	}
	var conflict *db.ConflictError
	if _, err := svc.Adapter(first); !errors.As(err, &conflict) || conflict.Code != "connection_retired" {
		t.Fatalf("adapter for replaced connection: %v", err)
	}
	if err := svc.PollTask(ctx, after); !errors.As(err, &conflict) || conflict.Code != "connection_retired" {
		t.Fatalf("poll replaced task: %v", err)
	}
}
