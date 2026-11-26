package coordinator_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest/fakestate"
	"github.com/flexdinesh/stage/server/internal/coordinator"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// The real Claude Code adapter against a fake claude CLI, through the
// coordinator, in process and behind a worker channel.

const claudeConn = 3

func claudeSetup(t *testing.T, p placement, f *claudetest.Fake) (*db.Store, *coordinator.Service, db.Task, *claudecode.Adapter) {
	t.Helper()
	return claudeSetupWith(t, p, f, f.Adapter(claudeConn))
}

// claudeSetupWith is claudeSetup on adapter a (one with hooks).
func claudeSetupWith(t *testing.T, p placement, f *claudetest.Fake, a *claudecode.Adapter) (*db.Store, *coordinator.Service, db.Task, *claudecode.Adapter) {
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
	task, err := store.CreateTask(t.Context(), db.NewTask{ProjectID: project.ID, ConnectionID: claudeConn, Title: "Work", Prompt: "Do it", ExecutionConfig: &execution.Config{Model: "haiku"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := p.coordinator(t, store, harness.MustRegistry(t3.Descriptor, claudecode.Descriptor), map[int64]execution.Adapter{claudeConn: a}, nil)
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

// A follow-up wakes a done session under its id: the task runs again and
// its timeline records the send; a working session is 409 session_busy
// (across a worker channel too), and an oversized message is invalid.
func TestContractClaudeFollowUp(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		f := claudetest.New(t)
		store, svc, task, _ := claudeSetup(t, p, f)
		ctx := t.Context()
		done := claudeFinish(t, store, svc, f, task)
		got, err := svc.Send(ctx, task.ID, execution.Message{Text: "now add tests"})
		if err != nil || got.State != db.StateRunning || *got.Session != *done.Session {
			t.Fatalf("send %+v %v", got, err)
		}
		if n := len(f.Read().Sessions); n != 1 {
			t.Fatalf("a copy started: %d sessions", n)
		}
		evs, err := store.TaskEvents(ctx, task.ID)
		if err != nil || !slices.ContainsFunc(evs, func(e db.TaskEvent) bool { return e.Detail == coordinator.FollowUpSent }) {
			t.Fatalf("events %+v %v", evs, err)
		}
		var ce *db.ConflictError
		if _, err := svc.Send(ctx, task.ID, execution.Message{Text: "and docs"}); !errors.As(err, &ce) || ce.Code != "session_busy" || !strings.Contains(ce.Msg, "is working") {
			t.Fatalf("busy: %v", err)
		}
		var invalid *db.InvalidError
		if _, err := svc.Send(ctx, task.ID, execution.Message{Text: strings.Repeat("x", coordinator.MaxMessage+1)}); !errors.As(err, &invalid) {
			t.Fatalf("oversized: %v", err)
		}
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

// The worktree launch option: Claude makes a worktree per attempt from the
// project checkout (claude -w); a dependent no longer starts from its
// predecessor's worktree. Stage makes none.
func TestContractClaudeWorktreeOption(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		f := claudetest.New(t)
		f.GitInit()
		store, svc, first, _ := claudeSetup(t, p, f)
		ctx := t.Context()
		worktree := &execution.Config{Model: "haiku", Options: []execution.ModelOption{{ID: "worktree", Value: execution.BoolValue(true)}}}
		pred, err := store.CreateTask(ctx, db.NewTask{ProjectID: first.ProjectID, ConnectionID: claudeConn, Title: "Build", Prompt: "Build it", ExecutionConfig: worktree})
		if err != nil {
			t.Fatal(err)
		}
		dep, err := store.CreateTask(ctx, db.NewTask{ProjectID: first.ProjectID, ConnectionID: claudeConn, Title: "Use", Prompt: "Use it", Needs: []int64{pred.ID}, ExecutionConfig: worktree})
		if err != nil {
			t.Fatal(err)
		}
		done := claudeFinish(t, store, svc, f, pred)
		o, err := store.Observation(ctx, *done.Session)
		if err != nil || o.Directory == f.Workspace || o.Project != f.Workspace || o.Branch == "" {
			t.Fatalf("predecessor observation %+v %v", o, err)
		}
		preview, err := store.CompletionPreview(ctx, pred.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.ChangeAcceptance(ctx, pred.ID, db.AcceptanceRequest{RequestID: "accept-pred", Revision: preview.Revision, EvidenceVersion: preview.EvidenceVersion}, false); err != nil {
			t.Fatal(err)
		}
		got, err := svc.StartTask(ctx, dep.ID)
		if err != nil || got.State != db.StateRunning || got.Session == nil {
			t.Fatalf("dependent start %+v %v", got, err)
		}
		ls := f.Launches()
		if last := ls[len(ls)-1]; last.Dir != f.Workspace || !slices.Contains(last.Args, "-w") {
			t.Fatalf("dependent launched in %s with %q", last.Dir, last.Args)
		}
		do, err := store.Observation(ctx, *got.Session)
		if err != nil || do.Project != f.Workspace || do.Directory == f.Workspace || do.Directory == o.Directory {
			t.Fatalf("dependent observation %+v %v", do, err)
		}
	})
}

// A session T3 started (its entrypoint, which T3's descriptor wraps) is
// refused with a conflict that says where to track it; Claude Code declares
// every operation through the coordinator, in process and behind a worker.
func TestContractClaudeT3SessionRefused(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		f := claudetest.New(t)
		_, svc, _, _ := claudeSetup(t, p, f)
		a, err := svc.Adapter(claudeConn)
		if err != nil {
			t.Fatal(err)
		}
		if c, err := a.Features(t.Context()); err != nil || c.DeclaresOperations() != nil {
			t.Fatal(c.Operations, err, c.DeclaresOperations())
		}
		id := "55555555-5555-4555-8555-555555555555"
		f.AddInteractive(id, f.Workspace, t3.OriginEntrypoint, "from T3")
		err = svc.RefreshScopedSession(t.Context(), execution.SessionRef{ConnectionID: claudeConn, ExternalSessionID: id})
		var conflict *db.ConflictError
		if !errors.As(err, &conflict) || conflict.Code != "session_refused" || conflict.Msg != "This Claude Code session was started by T3. Track it through T3." {
			t.Fatalf("%v", err)
		}
	})
}

// Discovery surfaces, adoption tracks: a waiting Claude session Stage did
// not launch shows as a silent waiting row and in the needs-input count,
// never as an inbox event, and leaves once answered. T3's sessions stay
// T3's.
func TestContractClaudeDiscoveredSessionsAreSilent(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		f := claudetest.New(t)
		store, svc, _, _ := claudeSetup(t, p, f)
		ctx := t.Context()
		var events []coordinator.SessionChange
		svc.OnSessionChange = func(_ context.Context, c coordinator.SessionChange) { events = append(events, c) }
		watch := func() {
			t.Helper()
			if err := svc.WatchAttention(ctx); err != nil {
				t.Fatal(err)
			}
		}
		waiting := func(want ...string) {
			t.Helper()
			rows, err := store.WaitingWithoutNotifications(ctx, 0)
			if err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, r := range rows {
				got = append(got, r.ExternalSessionID)
				if r.Directory == nil || *r.Directory != f.Workspace || r.ProjectTitle == nil || *r.ProjectTitle != "Repo" {
					t.Fatalf("row %+v", r)
				}
			}
			if !slices.Equal(got, want) {
				t.Fatalf("waiting %v, want %v", got, want)
			}
		}
		watch() // seeds: the first pass is silent for every harness
		term, t3 := "44444444-4444-4444-8444-444444444444", "33333333-3333-4333-8333-333333333333"
		f.AddInteractive(term, f.Workspace, "cli", "from a terminal")
		f.AddInteractive(t3, f.Workspace, "t3-code", "from T3")
		f.SetState(term, "", "waiting", "permission prompt")
		f.SetState(t3, "", "waiting", "input needed")
		watch()
		waiting(term)
		if n, err := store.NeedsInputCount(ctx); err != nil || n != 1 {
			t.Fatalf("needs input %d %v", n, err)
		}
		f.SetState(term, "", "busy", "")
		watch()
		waiting()
		f.SetState(term, "", "waiting", "permission prompt")
		watch()
		waiting(term)
		if len(events) != 0 {
			t.Fatalf("discovered sessions recorded events: %+v", events)
		}
	})
}

// A worker restarted without local harnesses no longer hosts its Claude
// Code connection: the coordinator answers connection_unavailable with
// the fix, not that the worker hosts no adapter.
func TestRemoteClaudeConnectionOnAWorkerWithoutLocalHarnesses(t *testing.T) {
	f := claudetest.New(t)
	pool := testDB.Reset(t)
	store := db.NewStore(pool)
	if _, err := pool.ExecContext(t.Context(), "INSERT INTO harness_connections(id,harness,machine_id) VALUES($1,$2,$3)", claudeConn, claudecode.Kind, placement{remote: true}.machine(t, pool)); err != nil {
		t.Fatal(err)
	}
	rig := newContractWorker(t, store, map[int64]execution.Adapter{claudeConn: f.Adapter(claudeConn)})
	svc := coordinator.NewForMachines(store, harness.MustRegistry(claudecode.Descriptor), rig.Set(), nil)
	if _, err := svc.Adapter(claudeConn); err != nil {
		t.Fatalf("hosted: %v", err)
	}
	rig.StopWorker()
	rig.SetProfile(harness.Profile{Kind: harness.ProfileServer})
	rig.StartWorker()
	var conflict *db.ConflictError
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := svc.Adapter(claudeConn)
		if errors.As(err, &conflict) && conflict.Code == "connection_unavailable" && strings.Contains(conflict.Msg, "stage worker run --local-harnesses") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("adapter: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A task's images reach Claude as files: the launch writes them to its own
// folder under the machine's image root, never the project (fetched by
// index over a worker channel), lets Claude read the root through an allow
// rule in its settings (no --add-dir) and lists each file after the prompt.
// Claude reads it without a permission prompt.
func TestContractClaudeImages(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		f := claudetest.New(t)
		store, svc, task, _ := claudeSetup(t, p, f)
		ctx := t.Context()
		var buf bytes.Buffer
		if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 1, 1))); err != nil {
			t.Fatal(err)
		}
		att, err := store.CreateAttachment(ctx, "shot.png", "image/png", buf.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.UpdateTask(ctx, task.ID, db.TaskPatch{AttachmentIDs: db.Optional[[]int64]{Set: true, V: []int64{att.ID}}}); err != nil {
			t.Fatal(err)
		}
		got, err := svc.StartTask(ctx, task.ID)
		if err != nil || got.State != db.StateRunning {
			t.Fatalf("start %+v %v", got, err)
		}
		args := f.Launches()[0].Args
		i := slices.Index(args, "--settings")
		if i < 0 || slices.Contains(args, "--add-dir") || args[i+1] != `{"permissions":{"allow":["Read(/`+f.ImageRoot()+`/**)"]}}` {
			t.Fatalf("args %q", args)
		}
		prompt := args[len(args)-1]
		_, file, _ := strings.Cut(prompt, "\n1. shot.png: ")
		if filepath.Dir(filepath.Dir(file)) != f.ImageRoot() || strings.HasPrefix(file, f.Workspace) {
			t.Fatalf("image file %q not under %s", file, f.ImageRoot())
		}
		if b, err := os.ReadFile(file); err != nil || !bytes.Equal(b, buf.Bytes()) {
			t.Fatalf("image file: %v", err)
		}
		if got, err := store.GetTask(ctx, task.ID); err != nil || got.State != db.StateRunning {
			t.Fatalf("task %+v %v", got.State, err)
		}
		if s := f.Read().Sessions; len(s) != 1 || s[0].State != "working" {
			t.Fatalf("session %+v", s)
		}
	})
}

// Claude's hooks push the task's session through the coordinator's stream
// (in process and over the worker channel) with no poll: the permission
// prompt is needs input, the finished turn done with its reply. A hook for
// a session Stage does not own changes nothing.
func TestContractClaudeHooksPushWithoutPolling(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		f := claudetest.New(t)
		store, svc, task, a := claudeSetupWith(t, p, f, f.HookedAdapter(claudeConn, f.Hooks()))
		got, err := svc.StartTask(t.Context(), task.ID)
		if err != nil || got.State != db.StateRunning || got.Session == nil {
			t.Fatalf("start %+v %v", got, err)
		}
		ref := *got.Session
		id := ref.ExternalSessionID
		ctx, cancel := context.WithCancel(t.Context())
		streamed := make(chan struct{})
		go func() {
			defer close(streamed)
			_ = svc.Stream(ctx, claudeConn)
		}()
		t.Cleanup(func() {
			cancel()
			<-streamed
		})
		// The stream subscribes asynchronously: hook until the task moves.
		await := func(want db.TaskState, event string) db.Task {
			t.Helper()
			deadline := time.Now().Add(10 * time.Second)
			for {
				if code := f.Hook(id, event, nil); code != 204 {
					t.Fatalf("%s: %d", event, code)
				}
				time.Sleep(50 * time.Millisecond)
				got, err := store.GetTask(t.Context(), task.ID)
				if err == nil && got.State == want {
					return got
				}
				if time.Now().After(deadline) {
					t.Fatalf("%s: want %s, task %+v %v", event, want, got, err)
				}
			}
		}
		f.SetState(id, "blocked", "waiting", "permission prompt")
		await(db.StateNeedsInput, "PermissionRequest")
		f.Reply(id, "All done.", true)
		f.SetState(id, "done", "idle", "")
		await(db.StateDone, "Stop")
		o, err := store.Observation(t.Context(), ref)
		if err != nil || o.Reply == nil || o.Reply.Text != "All done." {
			t.Fatalf("observation %+v %v", o, err)
		}
		// Another Claude session, launched outside Stage's task, finishes.
		other, err := a.Launch(t.Context(), execution.Intent{
			RequestID: "other", ConnectionID: claudeConn, Project: f.Workspace, Directory: f.Workspace, Title: "Other",
			Prompt: "x",
		}, execution.Checkpoint{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		f.SetState(other.Session.ExternalSessionID, "done", "idle", "")
		f.Hook(other.Session.ExternalSessionID, "Stop", nil)
		time.Sleep(200 * time.Millisecond)
		if got, err := store.GetTask(t.Context(), task.ID); err != nil || got.State != db.StateDone {
			t.Fatalf("unowned hook moved the task %+v %v", got, err)
		}
		if _, err := store.TaskBySession(t.Context(), other.Session); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("unowned session adopted: %v", err)
		}
	})
}
