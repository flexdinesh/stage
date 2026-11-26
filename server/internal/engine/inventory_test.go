package engine_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/engine"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
)

type inventoryAdapter struct {
	execution.Adapter
	inventory func() ([]execution.WorkspaceInfo, error)
}

func (a inventoryAdapter) Workspaces(context.Context) ([]execution.WorkspaceInfo, error) {
	return a.inventory()
}

type inventoryOps func([]execution.WorkspaceInfo) ([]execution.WorkspaceInfo, error)

func (fn inventoryOps) Inventory(_ context.Context, rows []execution.WorkspaceInfo) ([]execution.WorkspaceInfo, error) {
	return fn(rows)
}

func TestInventoryCallsOpsForHostedLocalWorkspaces(t *testing.T) {
	store := db.NewStore(testDB.Reset(t))
	rows := []execution.WorkspaceInfo{{ID: "network", WorkspaceRoot: "/reported"}}
	var calls int
	host := machine.NewLocal(db.LocalMachine, harness.Profile{}, inventoryOps(func(got []execution.WorkspaceInfo) ([]execution.WorkspaceInfo, error) {
		calls++
		if !reflect.DeepEqual(got, rows) {
			t.Fatalf("machine received %+v, want %+v", got, rows)
		}
		got = slices.Clone(got)
		got[1].Evidence = &execution.WorkspaceEvidence{CanonicalPath: "/canonical"}
		return got, nil
	}))
	a := inventoryAdapter{inventory: func() ([]execution.WorkspaceInfo, error) { return rows, nil }}
	host.Install(dbtest.T3Connection, dbtest.T3Connection, a)
	const unplaced = 999
	host.Install(unplaced, unplaced, a)
	svc := engine.NewForMachines(store, nil, machine.MustSet(host), nil)

	got, err := svc.Coordinator.Inventory(t.Context(), dbtest.T3Connection)
	if err != nil || !reflect.DeepEqual(got, rows) || calls != 0 {
		t.Fatalf("network inventory %+v, ops called %d times: %v", got, calls, err)
	}
	rows = append(rows, execution.WorkspaceInfo{ID: "local", WorkspaceRoot: "/local", Local: true})
	got, err = svc.Coordinator.Inventory(t.Context(), unplaced)
	if err != nil || !reflect.DeepEqual(got, rows) || calls != 0 {
		t.Fatalf("unplaced inventory %+v, ops called %d times: %v", got, calls, err)
	}
	got, err = svc.Coordinator.Inventory(t.Context(), dbtest.T3Connection)
	if err != nil || len(got) != 2 || got[0] != rows[0] || got[1].Evidence == nil || got[1].Evidence.CanonicalPath != "/canonical" || calls != 1 {
		t.Fatalf("local inventory %+v, ops called %d times: %v", got, calls, err)
	}
}

func TestFullSyncPollsLinkedTasksAfterInventoryFailure(t *testing.T) {
	ctx := t.Context()
	store, task := contractTask(t, placement{})
	fake := &documentHarness{}
	inventoryErr := errors.New("inventory source unavailable")
	failed := false
	a := inventoryAdapter{Adapter: fake, inventory: func() ([]execution.WorkspaceInfo, error) {
		if failed {
			return nil, inventoryErr
		}
		return fake.Workspaces(ctx)
	}}
	svc := engine.NewWithAdapters(store, documentHarnesses, map[int64]execution.Adapter{task.ConnectionID: a}, nil)

	started, err := svc.Coordinator.StartTask(ctx, task.ID)
	if err != nil || started.State != db.StateRunning {
		t.Fatalf("launch %+v: %v", started, err)
	}
	if _, err := store.RefreshWorkspaces(ctx, task.ConnectionID, []execution.WorkspaceInfo{
		{ID: "collection-z", WorkspaceRoot: "volume://checkout"},
		{ID: "pending", Title: "Pending import", WorkspaceRoot: "/pending", Importable: true},
	}); err != nil {
		t.Fatal(err)
	}
	failed = true
	fake.phase = "published"
	result, err := svc.Coordinator.FullSync(ctx)
	if !errors.Is(err, inventoryErr) || result.Projects != 0 || result.Sessions != 1 || !slices.Equal(result.Unavailable, []int64{task.ConnectionID}) {
		t.Fatalf("sync result %+v: %v", result, err)
	}
	got, err := store.GetTask(ctx, task.ID)
	if err != nil || got.State != db.StateDone {
		t.Fatalf("linked task %+v: %v", got, err)
	}
	projects, err := store.ListProjects(ctx, "all")
	if err != nil || len(projects) != 1 || len(projects[0].Bindings) != 1 || projects[0].Bindings[0].Status != "available" {
		t.Fatalf("inventory was refreshed or imported: %+v: %v", projects, err)
	}
}
