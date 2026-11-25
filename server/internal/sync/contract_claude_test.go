package sync_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest/fakestate"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	coordinator "github.com/flexdinesh/stage/server/internal/sync"
)

// The real Claude Code adapter against a fake claude CLI, through the
// coordinator, in process and behind a worker channel.

const claudeConn = 3

func claudeSetup(t *testing.T, p placement, f *claudetest.Fake) (*db.Store, *coordinator.Service, db.Task, *claudecode.Adapter) {
	t.Helper()
	pool := testDB.Reset(t)
	store := db.NewStore(pool)
	if _, err := pool.ExecContext(t.Context(), "INSERT INTO harness_connections(id,harness,machine_id) VALUES($1,$2,$3)", claudeConn, claudecode.Kind, p.machine(t, pool)); err != nil {
		t.Fatal(err)
	}
	project, err := store.InsertProject(t.Context(), db.Project{Title: "Repo", Bindings: []db.ProjectBinding{{ConnectionID: claudeConn, ExternalProjectID: f.Workspace, Workspace: f.Workspace}}})
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(t.Context(), db.NewTask{ProjectID: project.ID, ConnectionID: claudeConn, Title: "Work", Prompt: "Do it", RuntimeMode: "full-access", InteractionMode: "default", Workspace: db.Workspace{Type: "root"}, ExecutionConfig: &execution.Config{Model: "haiku"}})
	if err != nil {
		t.Fatal(err)
	}
	a := f.Adapter(claudeConn)
	svc := p.coordinator(t, store, map[int64]execution.Adapter{claudeConn: a}, nil)
	svc.Harnesses = harness.MustRegistry(claudecode.Descriptor)
	return store, svc, task, a
}

// A lost `claude --bg` reply leaves the attempt unconfirmed; recovery finds
// the session by its marker and never launches again.
func TestContractClaudeLostReplyDispatchesOnce(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		f := claudetest.New(t)
		store, svc, task, _ := claudeSetup(t, p, f)
		ctx := t.Context()
		f.Update(func(s *fakestate.State) { s.Launch = fakestate.CrashAfterCreate })
		if got, err := svc.StartTask(ctx, task.ID); err == nil || got.State != db.StateLaunching {
			t.Fatalf("lost reply %+v %v", got, err)
		}
		for range 2 {
			clearRetry(t)
			if err := svc.PollActive(ctx); err != nil {
				t.Fatal(err)
			}
		}
		got, err := store.GetTask(ctx, task.ID)
		if err != nil || got.State != db.StateRunning || got.Session == nil || len(f.Launches()) != 1 {
			t.Fatalf("recovered %+v %v launches %d", got, err, len(f.Launches()))
		}
		if !strings.Contains(f.Launches()[0].Args[len(f.Launches()[0].Args)-1], execution.LaunchMarker("")) {
			t.Fatal("the prompt carries no launch marker")
		}
	})
}

// Claude's session states drive the task: blocked is needs input, done
// carries the transcript's reply, stop interrupts.
func TestContractClaudeStatesReplyAndStop(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		f := claudetest.New(t)
		store, svc, task, a := claudeSetup(t, p, f)
		ctx := t.Context()
		got, err := svc.StartTask(ctx, task.ID)
		if err != nil || got.State != db.StateRunning || got.Session == nil {
			t.Fatalf("start %+v %v", got, err)
		}
		ref := *got.Session
		poll := func(want db.TaskState) db.Task {
			t.Helper()
			if err := svc.PollActive(ctx); err != nil {
				t.Fatal(err)
			}
			got, err := store.GetTask(ctx, task.ID)
			if err != nil || got.State != want {
				t.Fatalf("want %s: %+v %v", want, got, err)
			}
			return got
		}
		f.SetState(ref.ExternalSessionID, "blocked", "waiting", "permission prompt")
		poll(db.StateNeedsInput)
		f.Reply(ref.ExternalSessionID, "All done.", true)
		f.SetState(ref.ExternalSessionID, "done", "idle", "")
		poll(db.StateDone)
		o, err := store.Observation(ctx, ref)
		if err != nil || o.Reply == nil || o.Reply.Text != "All done." || !strings.HasPrefix(o.Links.Command, "claude attach ") {
			t.Fatalf("observation %+v %v", o, err)
		}
		f.SetState(ref.ExternalSessionID, "working", "busy", "")
		poll(db.StateRunning)
		if err := a.Stop(ctx, ref); err != nil {
			t.Fatal(err)
		}
		poll(db.StateInterrupted)
	})
}

// Claude's refusal of an untrusted folder certifies nothing started: the
// task returns to backlog with the reason.
func TestContractClaudeUntrustedFolderReturnsToBacklog(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		f := claudetest.New(t)
		f.Update(func(s *fakestate.State) { s.Trusted = nil })
		store, svc, task, _ := claudeSetup(t, p, f)
		if _, err := svc.StartTask(t.Context(), task.ID); err == nil {
			t.Fatal("untrusted launch succeeded")
		}
		got, err := store.GetTask(t.Context(), task.ID)
		if err != nil || got.State != db.StateBacklog || got.LastError == nil || !strings.Contains(*got.LastError, "doesn't trust") {
			t.Fatalf("%+v %v", got, err)
		}
	})
}

// A session T3 started is refused with a conflict that says where to track
// it.
func TestContractClaudeT3SessionRefused(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		f := claudetest.New(t)
		_, svc, _, _ := claudeSetup(t, p, f)
		id := "55555555-5555-4555-8555-555555555555"
		f.AddInteractive(id, f.Workspace, "t3-code", "from T3")
		err := svc.RefreshScopedSession(t.Context(), execution.SessionRef{ConnectionID: claudeConn, ExternalSessionID: id})
		var conflict *db.ConflictError
		if !errors.As(err, &conflict) || conflict.Code != "session_refused" || !strings.Contains(conflict.Msg, "Track it through T3") {
			t.Fatalf("%v", err)
		}
	})
}
