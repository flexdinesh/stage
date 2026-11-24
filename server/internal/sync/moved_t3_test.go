package sync_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	coordinator "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// workerT3 is a T3 on a worker: idempotent launches that report a parent
// (T3 records the thread that launched it over MCP).
type workerT3 struct {
	fakeBase
	parent string
}

func (f *workerT3) Features(context.Context) (execution.Capabilities, error) {
	return execution.Capabilities{Operations: map[string]execution.Availability{"launch": {Available: true}}, Models: []execution.Model{{Choice: execution.Choice{ID: "m", Availability: execution.Availability{Available: true}}}}}, nil
}
func (f *workerT3) observation(conn int64) execution.Observation {
	now := time.Now().UTC()
	o := execution.Observation{Session: execution.SessionRef{ConnectionID: conn, ExternalSessionID: "thread-1"}, Project: "proj", Title: "Work", State: "running", Active: true, CreatedAt: now, UpdatedAt: now}
	if f.parent != "" {
		o.Parent = &execution.SessionRef{ConnectionID: conn, ExternalSessionID: f.parent}
	}
	return o
}
func (f *workerT3) Observe(_ context.Context, ref execution.SessionRef) (execution.Observation, error) {
	return f.observation(ref.ConnectionID), nil
}
func (f *workerT3) Launch(ctx context.Context, in execution.Intent, _ execution.Checkpoint, save execution.SaveCheckpoint) (execution.Observation, error) {
	if err := save(ctx, execution.Checkpoint{Version: 1}); err != nil {
		return execution.Observation{}, err
	}
	return f.observation(in.ConnectionID), nil
}

// movedT3Setup: the local machine's T3 is retired onto connection 3, a T3 on
// the remote machine, with one bound project and one backlog task.
func movedT3Setup(t *testing.T, p placement, a execution.Adapter) (*db.Store, *coordinator.Service, db.Task) {
	t.Helper()
	pool := testDB.Reset(t)
	store := db.NewStore(pool)
	remote := placement{name: "remote", remote: true}.machine(t, pool)
	if _, err := pool.ExecContext(t.Context(), "INSERT INTO harness_connections(id,harness,machine_id) VALUES(3,'t3',$1)", remote); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.ExecContext(t.Context(), "UPDATE harness_connections SET active=0,retired_at=stage_now() WHERE machine_id=1 AND harness='t3'"); err != nil {
		t.Fatal(err)
	}
	project, err := store.InsertProject(t.Context(), db.Project{Title: "Moved", Bindings: []db.ProjectBinding{{ConnectionID: 3, ExternalProjectID: "proj", Workspace: "volume://moved"}}})
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(t.Context(), db.NewTask{ProjectID: project.ID, ConnectionID: 3, Title: "Work", Prompt: "Do it", RuntimeMode: "full-access", InteractionMode: "default", Workspace: db.Workspace{Type: "root"}, ExecutionConfig: &execution.Config{Model: "m"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := p.coordinator(t, store, map[int64]execution.Adapter{3: a}, nil)
	svc.Harnesses = harness.MustRegistry(t3.Descriptor)
	return store, svc, task
}

// A launch on a T3 that moved to a worker is confirmed, with or without a
// parent thread, and its later state changes are recorded.
func TestMovedT3LaunchConfirmsAndTransitions(t *testing.T) {
	for _, parent := range []string{"", "mcp-parent"} {
		t.Run("parent="+parent, func(t *testing.T) {
			forPlacements(t, func(t *testing.T, p placement) {
				f := &workerT3{fakeBase: fakeBase{workspace: "volume://moved"}, parent: parent}
				store, svc, task := movedT3Setup(t, p, f)
				ctx := t.Context()
				got, err := svc.StartTask(ctx, task.ID)
				if err != nil || got.State != db.StateRunning || got.Session == nil || got.Session.ExternalSessionID != "thread-1" {
					t.Fatalf("launch %+v %v", got, err)
				}
				if _, err := store.TransitionTask(ctx, db.Transition{TaskID: task.ID, From: []db.TaskState{db.StateRunning}, To: db.StateDone, Detail: "finished"}); err != nil {
					t.Fatalf("transition: %v", err)
				}
				if got, err = store.GetTask(ctx, task.ID); err != nil || got.State != db.StateDone {
					t.Fatalf("after transition %+v %v", got, err)
				}
			})
		})
	}
}

// When Stage cannot record the session a launch returned, the attempt keeps
// the cause, a backoff and the session, so the user sees it launched.
func TestMovedT3UnrecordedLaunchKeepsSession(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		f := &workerT3{fakeBase: fakeBase{workspace: "volume://moved"}}
		store, svc, task := movedT3Setup(t, p, f)
		ctx := t.Context()
		// Another task already holds the session the harness returns.
		other, err := store.CreateTask(ctx, db.NewTask{ProjectID: task.ProjectID, ConnectionID: 3, Title: "Other", Prompt: "p", RuntimeMode: "full-access", InteractionMode: "default", Workspace: db.Workspace{Type: "root"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := testDB.Pool.ExecContext(ctx, "INSERT INTO session_identities(connection_id,external_id) VALUES(3,'thread-1') ON CONFLICT DO NOTHING"); err != nil {
			t.Fatal(err)
		}
		if _, err := testDB.Pool.ExecContext(ctx, "UPDATE tasks SET state='running',session_id=(SELECT id FROM session_identities WHERE connection_id=3 AND external_id='thread-1') WHERE id=$1", other.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.StartTask(ctx, task.ID); err == nil {
			t.Fatal("unrecorded launch reported success")
		}
		a, err := store.Attempt(ctx, task.ID, 1)
		if err != nil || a.Phase != "unconfirmed" || !strings.Contains(a.Error, "another task") || a.RetryAt == nil || a.RetryCount != 1 {
			t.Fatalf("attempt %+v %v", a, err)
		}
		if a.Session == nil || a.Session.ConnectionID != 3 || a.Session.ExternalSessionID != "thread-1" || a.Session.Title != "Work" || a.Session.State != "running" {
			t.Fatalf("observed session %+v", a.Session)
		}
	})
}
