package sync_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	t3sync "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
)

var testDB dbtest.DB

// t3Harnesses is the production registry's T3 descriptor (serve wires it).
var t3Harnesses = harness.MustRegistry(t3.Descriptor)

// hookT3 runs afterList after every ListThreads (simulates a thread
// finishing between list and read).
type hookT3 struct {
	*t3.Fake
	afterList func()
}

func (h *hookT3) ListThreads(ctx context.Context, in t3.ListThreadsInput) (t3.ThreadPage, error) {
	page, err := h.Fake.ListThreads(ctx, in)
	if h.afterList != nil {
		h.afterList()
		h.afterList = nil
	}
	return page, err
}

type change struct {
	id   string
	from string // "" = first observation
	to   db.TaskState
}

func TestWatchThreads(t *testing.T) {
	store := db.NewStore(testDB.Reset(t))
	ctx := context.Background()
	fake := t3.NewFake()
	client := &hookT3{Fake: fake}
	external := fake.AddProject("app", "/w")
	if _, err := store.InsertProject(ctx, db.Project{Title: "app", Bindings: []db.ProjectBinding{{ConnectionID: 1, ExternalProjectID: external.ID, Workspace: "/w"}}}); err != nil {
		t.Fatal(err)
	}
	launch := func(title string) string {
		t.Helper()
		res, err := fake.LaunchThread(ctx, t3.LaunchInput{ProjectID: external.ID, Title: title})
		if err != nil {
			t.Fatal(err)
		}
		return res.ThreadID
	}
	// setStatus clears pending requests; needInput opens one on a running thread.
	setStatus := func(id string, st t3.ThreadStatus) {
		t.Helper()
		if err := fake.SetThread(id, func(d *t3.ThreadDetail) { d.PendingRequestCount = 0 }); err != nil {
			t.Fatal(err)
		}
		if err := fake.SetStatus(id, st); err != nil {
			t.Fatal(err)
		}
	}
	needInput := func(id string) {
		t.Helper()
		setStatus(id, t3.StatusRunning)
		if err := fake.SetThread(id, func(d *t3.ThreadDetail) { d.PendingRequestCount = 1 }); err != nil {
			t.Fatal(err)
		}
	}
	hourAgo := time.Now().Add(-time.Hour)
	old := func(id string) {
		t.Helper()
		if err := fake.SetThread(id, func(d *t3.ThreadDetail) { d.CreatedAt = hourAgo }); err != nil {
			t.Fatal(err)
		}
	}

	// Threads that exist before the service starts.
	ext := launch("external")
	old(ext)
	extNeeds := launch("external needs input")
	old(extNeeds)
	needInput(extNeeds)
	oldIdle := launch("old finished")
	old(oldIdle)
	setStatus(oldIdle, t3.StatusCompleted)

	svc := t3sync.NewWithAdapters(store, map[int64]execution.Adapter{dbtest.T3Connection: t3.NewAdapter(client)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.Harnesses = t3Harnesses
	var got []change
	svc.OnThreadChange = func(_ context.Context, c t3sync.ThreadChange) {
		from := ""
		if c.From != nil {
			from = string(*c.From)
		}
		got = append(got, change{c.ThreadID, from, c.To})
	}
	pass := func(want ...change) {
		t.Helper()
		got = nil
		if err := svc.WatchThreads(ctx); err != nil {
			t.Fatal(err)
		}
		byID := func(a, b change) int { return strings.Compare(a.id, b.id) }
		slices.SortFunc(got, byID)
		slices.SortFunc(want, byID)
		if !slices.Equal(got, want) {
			t.Fatalf("changes %+v, want %+v", got, want)
		}
	}
	// observed is the watched state of a session no task owns ("" when
	// task-owned or never observed).
	observed := func(id string) string {
		t.Helper()
		ref := db.SessionRef{ConnectionID: dbtest.T3Connection, ExternalSessionID: id}
		if _, err := store.TaskBySession(ctx, ref); err == nil {
			return ""
		}
		o, err := store.Observation(ctx, ref)
		if errors.Is(err, db.ErrNotFound) || err == nil && o.Child {
			return ""
		}
		if err != nil {
			t.Fatal(err)
		}
		return o.State
	}

	// Task-owned thread and a subagent thread (both never watched).
	task, err := store.CreateTask(ctx, db.NewTask{ConnectionID: dbtest.T3Connection, ProjectID: 1, Title: "task", Prompt: "p",
		Workspace: db.Workspace{Type: "root"}})
	if err != nil {
		t.Fatal(err)
	}
	started, err := svc.StartTask(ctx, task.ID)
	if err != nil || started.T3ThreadID == nil {
		t.Fatalf("start task: %+v %v", started, err)
	}
	taskThread := *started.T3ThreadID
	sub := launch("subagent")
	if err := fake.SetThread(sub, func(d *t3.ThreadDetail) {
		d.ParentThreadID, d.RelationshipToParent = ext, "subagent"
		d.PendingRequestCount = 1
	}); err != nil {
		t.Fatal(err)
	}

	// First pass records silently, even threads created after start.
	early := launch("early")
	needInput(early)
	pass()
	for id, want := range map[string]string{ext: "running", extNeeds: "needs_input", early: "needs_input", oldIdle: "", taskThread: "", sub: ""} {
		if o := observed(id); o != want {
			t.Fatalf("observed %s = %q, want %q", id, o, want)
		}
	}

	// T3 "waiting" (checkpoint gap) stays running, silently.
	setStatus(ext, t3.StatusWaiting)
	pass()
	if o := observed(ext); o != "running" {
		t.Fatalf("waiting thread observed %q", o)
	}

	// running → needs_input; task-owned and subagent changes are ignored.
	needInput(ext)
	needInput(taskThread)
	setStatus(sub, t3.StatusCompleted)
	pass(change{ext, "running", db.StateNeedsInput})

	// needs_input → done; needs_input → failed (watched, no longer listed).
	setStatus(ext, t3.StatusCompleted)
	setStatus(extNeeds, t3.StatusFailed)
	pass(change{ext, "needs_input", db.StateDone}, change{extNeeds, "needs_input", db.StateFailed})
	pass() // stable

	// done → settled notifies (done stays watched until settled).
	if err := fake.SetThread(ext, func(d *t3.ThreadDetail) { d.Settled = true }); err != nil {
		t.Fatal(err)
	}
	pass() // Harness settlement does not change Stage execution or acceptance.
	pass() // settled: no longer watched

	// A thread created before start, first seen now: recorded silently.
	needInput(oldIdle)
	pass()
	if o := observed(oldIdle); o != "needs_input" {
		t.Fatalf("old thread observed %q", o)
	}

	// A new thread (created after start) needing input notifies on first sight.
	fresh := launch("fresh")
	needInput(fresh)
	pass(change{fresh, "", db.StateNeedsInput})

	// A new thread that finished between list and read notifies done.
	racy := launch("racy")
	client.afterList = func() { setStatus(racy, t3.StatusCompleted) }
	pass(change{racy, "", db.StateDone})

	// Missing sessions preserve the last execution state.
	fake.RemoveThread(fresh)
	pass()
	if o := observed(fresh); o != "needs_input" {
		t.Fatalf("removed thread observed %q", o)
	}
	if o := observed(taskThread); o != "" {
		t.Fatalf("task thread observed %q", o)
	}
}
