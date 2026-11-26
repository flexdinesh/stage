package sync_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/codex"
	"github.com/flexdinesh/stage/server/internal/codex/codextest"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	coordinator "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// The real Codex adapter against a fake daemon, driven by the coordinator
// in process and behind a worker channel.

const codexDir = "/w/app"

func codexSetup(t *testing.T, p placement) (*db.Store, *codextest.Server, *coordinator.Service, db.Task) {
	t.Helper()
	pool := testDB.Reset(t)
	store := db.NewStore(pool)
	if _, err := pool.ExecContext(t.Context(), "INSERT INTO harness_connections(id,harness,machine_id) VALUES(3,$1,$2)", codex.Kind, p.machine(t, pool)); err != nil {
		t.Fatal(err)
	}
	project, err := store.InsertProject(t.Context(), db.Project{Title: "App", Bindings: []db.ProjectBinding{{ConnectionID: 3, ExternalProjectID: codexDir, Workspace: codexDir}}})
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(t.Context(), db.NewTask{ProjectID: project.ID, ConnectionID: 3, Title: "Fix", Prompt: "Fix it", Workspace: db.Workspace{Type: "root"}, ExecutionConfig: &execution.Config{Model: "gpt-test", Permission: "auto"}})
	if err != nil {
		t.Fatal(err)
	}
	srv := codextest.New(t)
	srv.Trust(codexDir, "trusted")
	client := codex.NewClient(&codextest.Daemon{Srv: srv, T: t}, "test")
	t.Cleanup(client.Close)
	svc := p.coordinator(t, store, map[int64]execution.Adapter{3: codex.New(client, 3, "")}, nil)
	svc.Harnesses = harness.MustRegistry(t3.Descriptor, codex.Descriptor)
	return store, srv, svc, task
}

// A thread T3 started (its originator, which T3's descriptor wraps) is
// refused with a conflict that says where to track it; Codex declares every
// operation through the coordinator, in process and behind a worker.
func TestContractCodexWrappedThreadRefusedAndOperationsDeclared(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		_, srv, svc, _ := codexSetup(t, p)
		ctx := t.Context()
		a, err := svc.Adapter(3)
		if err != nil {
			t.Fatal(err)
		}
		if f, err := a.Features(ctx); err != nil || f.DeclaresOperations() != nil {
			t.Fatal(f.Operations, err, f.DeclaresOperations())
		}
		wrapped := srv.AddThread(codextest.Thread{ID: "019a0000-0000-7000-8000-00000000bbbb", Cwd: codexDir, Originator: t3.OriginOriginator})
		err = svc.RefreshScopedSession(ctx, execution.SessionRef{ConnectionID: 3, ExternalSessionID: wrapped.ID})
		var conflict *db.ConflictError
		if !errors.As(err, &conflict) || conflict.Code != "session_refused" || conflict.Msg != "This Codex session was started by T3. Track it through T3." {
			t.Fatalf("%v", err)
		}
		own := srv.AddThread(codextest.Thread{ID: "019a0000-0000-7000-8000-00000000aaaa", Cwd: codexDir, Originator: "codex-tui"})
		if err := svc.RefreshScopedSession(ctx, execution.SessionRef{ConnectionID: 3, ExternalSessionID: own.ID}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestContractCodexLaunchToDoneWithReplyAndResumeCommand(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		store, srv, svc, task := codexSetup(t, p)
		ctx := t.Context()
		got, err := svc.StartTask(ctx, task.ID)
		if err != nil || got.State != db.StateRunning || got.Session == nil {
			t.Fatal(got, err)
		}
		id := got.Session.ExternalSessionID
		srv.Wait(id)
		if err := svc.PollTask(ctx, got); err != nil {
			t.Fatal(err)
		}
		if got, _ = store.GetTask(ctx, task.ID); got.State != db.StateNeedsInput {
			t.Fatal("needs input", got.State)
		}
		srv.Finish(id, "completed", "Fixed.")
		if err := svc.PollTask(ctx, got); err != nil {
			t.Fatal(err)
		}
		got, _ = store.GetTask(ctx, task.ID)
		if got.State != db.StateDone {
			t.Fatal("done", got.State)
		}
		o, err := store.Observation(ctx, *got.Session)
		if err != nil || o.Reply == nil || o.Reply.Text != "Fixed." || o.Links.Command != "cd "+codexDir+" && codex resume "+id {
			t.Fatalf("stored observation %+v %v", o, err)
		}
	})
}

// A lost turn/start reply leaves the attempt unconfirmed; recovery
// reconciles the same thread and turn, never a second one.
func TestContractCodexLostTurnReplyReconcilesOnce(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		store, srv, svc, task := codexSetup(t, p)
		ctx := t.Context()
		srv.DropReply("turn/start", 1)
		short, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		got, err := svc.StartTask(short, task.ID)
		cancel()
		if err == nil {
			t.Fatal("lost reply confirmed", got)
		}
		clearRetry(t)
		if err := svc.PollActive(ctx); err != nil {
			t.Fatal(err)
		}
		got, err = store.GetTask(ctx, task.ID)
		if err != nil || got.State != db.StateRunning || got.Session == nil || srv.Calls("turn/start") != 1 || len(srv.Threads()) != 1 {
			t.Fatalf("recovered %+v %v turn/start %d threads %v", got, err, srv.Calls("turn/start"), srv.Threads())
		}
	})
}

func TestContractCodexArchiveInfersCompletion(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		store, srv, svc, task := codexSetup(t, p)
		ctx := t.Context()
		got, err := svc.StartTask(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		srv.Finish(got.Session.ExternalSessionID, "completed", "Saved result")
		srv.Archive(got.Session.ExternalSessionID)
		for range 2 {
			if err = svc.PollTask(ctx, got); err != nil {
				t.Fatal(err)
			}
			got, _ = store.GetTask(ctx, task.ID)
		}
		evidence, err := store.Acceptances(ctx, task.ID)
		if err != nil || len(evidence) != 1 || evidence[0].Provenance != "harness_archived" || *evidence[0].Evidence.Reply != "Saved result" {
			t.Fatalf("archive inference %+v %v", evidence, err)
		}
	})
}

// Status changes reach Stage through the daemon-wide notifications, with
// no polling.
func TestContractCodexPushIngest(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		store, srv, svc, task := codexSetup(t, p)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		got, err := svc.StartTask(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		go func() { _ = svc.Stream(ctx, 3) }()
		deadline := time.Now().Add(5 * time.Second)
		for i := 0; time.Now().Before(deadline); i++ {
			if i%10 == 0 {
				srv.Finish(got.Session.ExternalSessionID, "completed", "pushed")
			}
			time.Sleep(50 * time.Millisecond)
			if cur, _ := store.GetTask(ctx, task.ID); cur.State == db.StateDone {
				return
			}
		}
		t.Fatal("pushed status never ingested")
	})
}

func TestContractCodexStop(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		store, _, svc, task := codexSetup(t, p)
		ctx := t.Context()
		got, err := svc.StartTask(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		a, err := svc.Adapter(3)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.Stop(ctx, *got.Session); err != nil {
			t.Fatal(err)
		}
		if err := svc.PollTask(ctx, got); err != nil {
			t.Fatal(err)
		}
		if got, _ = store.GetTask(ctx, task.ID); got.State != db.StateInterrupted {
			t.Fatal("stop", got.State)
		}
	})
}
