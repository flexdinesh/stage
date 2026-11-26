package sync_test

import (
	"context"
	gosync "sync"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	coordinator "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// laptop is a second machine (a future worker's).
const laptop int64 = 2

// twoMachineSetup: connection 2 on the local machine and 3 on the laptop,
// both of the fake document kind, one project bound on both, one backlog
// task per connection.
func twoMachineSetup(t *testing.T) (*db.Store, *coordinator.Service, map[int64]*documentHarness, map[int64]db.Task) {
	t.Helper()
	pool := testDB.Reset(t)
	store := db.NewStore(pool)
	for _, q := range []string{
		`INSERT INTO machines(id,label) VALUES(2,'Laptop')`,
		`INSERT INTO harness_connections(id,machine_id,harness) VALUES(2,1,'document'),(3,2,'document')`,
	} {
		if _, err := pool.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	if store.ActiveID(db.LocalMachine, "document") != 2 || store.ActiveID(laptop, "document") != 3 {
		t.Fatal("each machine has its own active document connection")
	}
	p, err := store.InsertProject(t.Context(), db.Project{Title: "Shared", Bindings: []db.ProjectBinding{
		{ConnectionID: 2, ExternalProjectID: "collection-z", Workspace: "volume://checkout"},
		{ConnectionID: 3, ExternalProjectID: "collection-z", Workspace: "volume://checkout"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	fakes := map[int64]*documentHarness{2: {}, 3: {}}
	tasks := map[int64]db.Task{}
	for id := range fakes {
		task, err := store.CreateTask(t.Context(), db.NewTask{ProjectID: p.ID, ConnectionID: id, Title: "Write", Prompt: "p", Workspace: db.Workspace{Type: "root"}, ExecutionConfig: &execution.Config{Model: "brain-7", Permission: "reviewed"}})
		if err != nil {
			t.Fatal(err)
		}
		tasks[id] = task
	}
	svc := coordinator.NewWithAdapters(store, map[int64]execution.Adapter{2: fakes[2], 3: fakes[3]}, nil)
	svc.Harnesses = documentHarnesses
	return store, svc, fakes, tasks
}

func TestTwoMachinesLaunchPollAndScheduleIndependently(t *testing.T) {
	store, svc, fakes, tasks := twoMachineSetup(t)
	ctx := t.Context()
	// Each launch reaches its own machine's harness only.
	for id, task := range tasks {
		got, err := svc.StartTask(ctx, task.ID)
		if err != nil || got.State != db.StateRunning || got.Session == nil || got.Session.ConnectionID != id {
			t.Fatalf("launch on %d: %+v %v", id, got, err)
		}
		if fakes[id].created != 1 || fakes[id].input.ConnectionID != id {
			t.Fatalf("harness %d: %+v", id, fakes[id])
		}
	}

	// The laptop goes offline: the local machine's poll still settles its
	// task; the laptop's task is unchanged.
	fakes[3].offline = true
	fakes[2].phase = "published"
	if err := svc.PollActive(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.GetTask(ctx, tasks[2].ID); got.State != db.StateDone {
		t.Fatalf("local task %s", got.State)
	}
	if got, _ := store.GetTask(ctx, tasks[3].ID); got.State != db.StateRunning {
		t.Fatalf("offline laptop task %s", got.State)
	}
	fakes[3].offline = false
	fakes[3].phase = "published"
	if err := svc.PollActive(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.GetTask(ctx, tasks[3].ID); got.State != db.StateDone {
		t.Fatalf("laptop task %s", got.State)
	}

	// A schedule targets a machine and harness through its connection: its
	// task launches on that machine only.
	sc, err := store.CreateSchedule(ctx, db.NewSchedule{ConnectionID: 3, ProjectID: tasks[3].ProjectID, Title: "Nightly", Prompt: "p", Workspace: db.Workspace{Type: "root"}, ExecutionConfig: tasks[3].ExecutionConfig, Cron: "0 3 * * *", Tz: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	spawned, err := store.RunScheduleNow(ctx, sc.ID)
	if err != nil || spawned.ConnectionID != 3 {
		t.Fatalf("spawned %+v %v", spawned, err)
	}
	if got, err := svc.StartTask(ctx, spawned.ID); err != nil || got.Session == nil || got.Session.ConnectionID != 3 {
		t.Fatalf("scheduled launch %+v %v", got, err)
	}
	if fakes[3].created != 2 || fakes[2].created != 1 {
		t.Fatalf("scheduled launch reached local=%d laptop=%d", fakes[2].created, fakes[3].created)
	}
}

// barrierAdapter holds each workspace listing until both connections list:
// both full syncs must run at once.
type barrierAdapter struct {
	execution.Adapter
	arrived *gosync.WaitGroup
}

func (b barrierAdapter) Workspaces(ctx context.Context) ([]execution.WorkspaceInfo, error) {
	b.arrived.Done()
	done := make(chan struct{})
	go func() { b.arrived.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		return nil, execution.ErrUnavailable
	}
	return b.Adapter.Workspaces(ctx)
}

func TestTwoMachinesT3FullSyncsRunTogether(t *testing.T) {
	pool := testDB.Reset(t)
	store := db.NewStore(pool)
	ctx := t.Context()
	if _, err := pool.ExecContext(ctx, `INSERT INTO machines(id,label) VALUES(2,'Laptop')`); err != nil {
		t.Fatal(err)
	}
	local := store.ActiveID(db.LocalMachine, "t3")
	sw, err := store.SwitchInstance(ctx, laptop, t3.Descriptor, "env-laptop", "laptop", 0)
	if err != nil || sw.New == 0 || sw.New == local {
		t.Fatalf("laptop T3 %+v %v", sw, err)
	}
	var arrived gosync.WaitGroup
	arrived.Add(2)
	svc := coordinator.NewWithAdapters(store, map[int64]execution.Adapter{
		local:  barrierAdapter{t3.NewConnectionAdapter(t3.NewFake(), local), &arrived},
		sw.New: barrierAdapter{t3.NewConnectionAdapter(t3.NewFake(), sw.New), &arrived},
	}, nil)
	// Each connection's successful full sync reaches the hook (serve
	// records T3's last full sync per machine from it).
	var mu gosync.Mutex
	synced := map[int64]bool{}
	svc.OnConnectionSynced = func(_ context.Context, connection int64, at time.Time) error {
		mu.Lock()
		defer mu.Unlock()
		synced[connection] = !at.IsZero()
		return nil
	}
	r, err := svc.FullSync(ctx)
	if err != nil || len(r.Unavailable) != 0 {
		t.Fatalf("full sync %+v %v", r, err)
	}
	if !synced[local] || !synced[sw.New] {
		t.Fatalf("synced connections %v", synced)
	}
	if svc.Progress(local).Running || svc.Progress(sw.New).Running {
		t.Fatal("progress after sync")
	}
}
