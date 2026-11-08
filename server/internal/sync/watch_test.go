package sync_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/t3-stage/server/internal/db"
	"github.com/flexdinesh/t3-stage/server/internal/db/dbtest"
	t3sync "github.com/flexdinesh/t3-stage/server/internal/sync"
	"github.com/flexdinesh/t3-stage/server/internal/t3"
)

var testDB *dbtest.DB

func TestMain(m *testing.M) {
	testDB = dbtest.Open("t3stage_test_sync")
	if testDB.Skip != "" {
		fmt.Println("sync integration tests skipped:", testDB.Skip)
	}
	code := m.Run()
	testDB.Close()
	os.Exit(code)
}

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
	if _, err := store.InsertProject(ctx, db.Project{T3ProjectID: "proj-1", Title: "app", WorkspaceRoot: "/w", Source: db.SourceApp}); err != nil {
		t.Fatal(err)
	}
	launch := func(title string) string {
		t.Helper()
		res, err := fake.LaunchThread(ctx, t3.LaunchInput{ProjectID: "proj-1", Title: title})
		if err != nil {
			t.Fatal(err)
		}
		return res.ThreadID
	}
	setStatus := func(id string, st t3.ThreadStatus) {
		t.Helper()
		if err := fake.SetStatus(id, st); err != nil {
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
	extWaiting := launch("external waiting")
	old(extWaiting)
	setStatus(extWaiting, t3.StatusWaiting)
	oldIdle := launch("old finished")
	old(oldIdle)
	setStatus(oldIdle, t3.StatusCompleted)

	svc := t3sync.New(store, client, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	observed := func(id string) string {
		t.Helper()
		obs, err := store.ThreadObservations(ctx, []string{id})
		if err != nil {
			t.Fatal(err)
		}
		if obs[id].ObservedState == nil {
			return ""
		}
		return string(*obs[id].ObservedState)
	}

	// Task-owned thread and a subagent thread (both never watched).
	task, err := store.CreateTask(ctx, db.NewTask{ProjectID: 1, Title: "task", Prompt: "p",
		RuntimeMode: "full-access", InteractionMode: "default", Workspace: db.Workspace{Type: "root"}})
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
		d.Status = t3.StatusWaiting
	}); err != nil {
		t.Fatal(err)
	}

	// First pass records silently, even threads created after start.
	early := launch("early")
	setStatus(early, t3.StatusWaiting)
	pass()
	for id, want := range map[string]string{ext: "running", extWaiting: "needs_input", early: "needs_input", oldIdle: "", taskThread: "", sub: ""} {
		if o := observed(id); o != want {
			t.Fatalf("observed %s = %q, want %q", id, o, want)
		}
	}

	// running → needs_input; task-owned and subagent changes are ignored.
	setStatus(ext, t3.StatusWaiting)
	setStatus(taskThread, t3.StatusWaiting)
	setStatus(sub, t3.StatusCompleted)
	pass(change{ext, "running", db.StateNeedsInput})

	// needs_input → done; needs_input → failed (watched, no longer listed).
	setStatus(ext, t3.StatusCompleted)
	setStatus(extWaiting, t3.StatusFailed)
	pass(change{ext, "needs_input", db.StateDone}, change{extWaiting, "needs_input", db.StateFailed})
	pass() // stable

	// A thread created before start, first seen now: recorded silently.
	setStatus(oldIdle, t3.StatusWaiting)
	pass()
	if o := observed(oldIdle); o != "needs_input" {
		t.Fatalf("old thread observed %q", o)
	}

	// A new thread (created after start) needing input notifies on first sight.
	fresh := launch("fresh")
	setStatus(fresh, t3.StatusWaiting)
	pass(change{fresh, "", db.StateNeedsInput})

	// A new thread that finished between list and read notifies done.
	racy := launch("racy")
	client.afterList = func() { setStatus(racy, t3.StatusCompleted) }
	pass(change{racy, "", db.StateDone})

	// T3 forgets a watched thread → cancelled, silently.
	fake.RemoveThread(fresh)
	pass()
	if o := observed(fresh); o != "cancelled" {
		t.Fatalf("removed thread observed %q", o)
	}
	if o := observed(taskThread); o != "" {
		t.Fatalf("task thread observed %q", o)
	}
}
