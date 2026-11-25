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

// claudeFinish runs the task's session to Claude's done with a reply and
// polls it: the task is done, not yet accepted.
func claudeFinish(t *testing.T, store *db.Store, svc *coordinator.Service, f *claudetest.Fake, task db.Task) db.Task {
	t.Helper()
	got, err := svc.StartTask(t.Context(), task.ID)
	if err != nil || got.Session == nil {
		t.Fatalf("start %+v %v", got, err)
	}
	f.Reply(got.Session.ExternalSessionID, "All done.", true)
	f.SetState(got.Session.ExternalSessionID, "done", "idle", "")
	return claudePoll(t, store, svc, got, db.StateDone)
}

func claudePoll(t *testing.T, store *db.Store, svc *coordinator.Service, task db.Task, want db.TaskState) db.Task {
	t.Helper()
	if err := svc.PollTask(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetTask(t.Context(), task.ID)
	if err != nil || got.State != want {
		t.Fatalf("want %s: %+v %v", want, got, err)
	}
	return got
}

// `claude rm` of a done session is the archived closure: the task completes
// once, with Claude Code's provenance and journal event; a reopen is not
// undone by the still-removed session.
func TestContractClaudeRemovalCompletesDoneTask(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		f := claudetest.New(t)
		store, svc, task, _ := claudeSetup(t, p, f)
		ctx := t.Context()
		released := 0
		svc.OnAccepted = func() { released++ }
		got := claudeFinish(t, store, svc, f, task)
		if got.AcceptanceID != nil {
			t.Fatal("done completed before removal")
		}
		f.SetState(got.Session.ExternalSessionID, "done", "", "") // the idle reap
		got = claudePoll(t, store, svc, got, db.StateDone)
		if got.AcceptanceID != nil {
			t.Fatal("the idle reap completed the task")
		}
		f.Rm(got.Session.ExternalSessionID)
		got = claudePoll(t, store, svc, got, db.StateDone)
		evidence, err := store.Acceptances(ctx, task.ID)
		if err != nil || got.AcceptanceID == nil || len(evidence) != 1 || evidence[0].Provenance != "harness_archived" || evidence[0].Evidence.Reply == nil || *evidence[0].Evidence.Reply != "All done." || released != 1 {
			t.Fatalf("removal inference %+v %+v %v released %d", got, evidence, err, released)
		}
		page, err := store.ReadJournal(ctx, db.JournalOwner{TaskID: &task.ID}, db.JournalQuery{Kind: "events", Q: "Completed from Claude Code removal"})
		if err != nil || len(page.Entries) != 1 {
			t.Fatalf("journal %+v %v", page, err)
		}
		preview, err := store.CompletionPreview(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.ChangeAcceptance(ctx, task.ID, db.AcceptanceRequest{RequestID: "reopen", Revision: preview.Revision}, true); err != nil {
			t.Fatal(err)
		}
		got, _ = store.GetTask(ctx, task.ID)
		if got = claudePoll(t, store, svc, got, db.StateDone); got.AcceptanceID != nil {
			t.Fatal("a still-removed session completed the reopened task again")
		}
	})
}

// A removal completes nothing when the task is not done (a session removed
// while it waited for input) or the connection's auto-complete is off.
func TestContractClaudeRemovalWithoutInference(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		t.Run("needs input", func(t *testing.T) {
			f := claudetest.New(t)
			store, svc, task, _ := claudeSetup(t, p, f)
			got, err := svc.StartTask(t.Context(), task.ID)
			if err != nil || got.Session == nil {
				t.Fatalf("start %+v %v", got, err)
			}
			f.Reply(got.Session.ExternalSessionID, "Which file?", true)
			f.SetState(got.Session.ExternalSessionID, "blocked", "idle", "")
			got = claudePoll(t, store, svc, got, db.StateNeedsInput)
			f.Rm(got.Session.ExternalSessionID)
			if got = claudePoll(t, store, svc, got, db.StateNeedsInput); got.AcceptanceID != nil {
				t.Fatal("a removed blocked session completed its task")
			}
		})
		t.Run("auto-complete off", func(t *testing.T) {
			f := claudetest.New(t)
			store, svc, task, _ := claudeSetup(t, p, f)
			if _, err := store.SetAutoComplete(t.Context(), claudeConn, false); err != nil {
				t.Fatal(err)
			}
			got := claudeFinish(t, store, svc, f, task)
			f.Rm(got.Session.ExternalSessionID)
			if got = claudePoll(t, store, svc, got, db.StateDone); got.AcceptanceID != nil {
				t.Fatal("completed with auto-complete off")
			}
		})
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
