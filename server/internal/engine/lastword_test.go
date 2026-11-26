package engine_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/engine"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// replyClient counts LatestReply calls and can fail them.
type replyClient struct {
	*t3.Fake
	calls int
	fail  error
}

func (c *replyClient) LatestReply(ctx context.Context, in t3.LatestReplyInput) (t3.Reply, error) {
	c.calls++
	if c.fail != nil {
		return t3.Reply{}, c.fail
	}
	return c.Fake.LatestReply(ctx, in)
}

func TestLastWordReadOnlyWhenAtRestAndChanged(t *testing.T) {
	store := db.NewStore(testDB.Reset(t))
	ctx := context.Background()
	client := &replyClient{Fake: t3.NewFake()}
	svc := engine.NewWithAdapters(store, nil, map[int64]execution.Adapter{dbtest.T3Connection: t3.NewAdapter(client)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	res, err := client.LaunchThread(ctx, t3.LaunchInput{ProjectID: "p", Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	id := res.ThreadID
	if err := svc.Coordinator.RefreshScopedSession(ctx, execution.SessionRef{ConnectionID: store.ActiveID(db.LocalMachine, "t3"), ExternalSessionID: id}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertProject(ctx, db.Project{Title: "p", Bindings: []db.ProjectBinding{{ConnectionID: 1, ExternalProjectID: "p", Workspace: "/w/p"}}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AdoptSession(ctx, db.AdoptInput{Session: db.SessionRef{ConnectionID: dbtest.T3Connection, ExternalSessionID: id}}); err != nil {
		t.Fatal(err)
	}
	mutate := func(fn func(*t3.ThreadDetail)) {
		t.Helper()
		if err := client.SetThread(id, fn); err != nil {
			t.Fatal(err)
		}
	}
	refresh := func(wantCalls int) {
		t.Helper()
		if err := svc.Coordinator.RefreshScopedSession(ctx, execution.SessionRef{ConnectionID: store.ActiveID(db.LocalMachine, "t3"), ExternalSessionID: id}); err != nil {
			t.Fatal(err)
		}
		if client.calls != wantCalls {
			t.Fatalf("LatestReply calls = %d, want %d", client.calls, wantCalls)
		}
	}
	reply := func() *execution.Reply {
		t.Helper()
		o, err := store.Observation(ctx, db.SessionRef{ConnectionID: dbtest.T3Connection, ExternalSessionID: id})
		if err != nil {
			t.Fatal(err)
		}
		return o.Reply
	}
	text := func() string {
		t.Helper()
		if r := reply(); r != nil {
			return r.Text
		}
		return ""
	}

	// Running: never read, even with items and a reply.
	mutate(func(d *t3.ThreadDetail) { d.ActiveRunID = "run-1"; d.Status = t3.StatusRunning; d.ItemCount = 4 })
	if err := client.SetReply(id, t3.Reply{ItemID: "i1", RunID: "run-1", Text: "working on it"}); err != nil {
		t.Fatal(err)
	}
	refresh(0)

	// At rest: read once; unchanged updatedAt is not read again.
	if err := client.SetStatus(id, t3.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	refresh(1)
	refresh(1)
	if got := text(); got != "working on it" {
		t.Fatalf("stored %q", got)
	}

	// New activity at rest: read again.
	if err := client.SetReply(id, t3.Reply{ItemID: "i2", RunID: "run-1", Text: "All done."}); err != nil {
		t.Fatal(err)
	}
	mutate(func(d *t3.ThreadDetail) { d.ItemCount = 6 })
	refresh(2)
	if got := text(); got != "All done." {
		t.Fatalf("stored %q", got)
	}

	// Needs input counts as at rest despite the active run.
	mutate(func(d *t3.ThreadDetail) {
		d.ActiveRunID = "run-2"
		d.Status = t3.StatusWaiting
		d.PendingRequestCount = 1
	})
	refresh(3)

	// A failed read never fails the refresh and retries on the next one.
	client.fail = errors.New("boom")
	mutate(func(d *t3.ThreadDetail) { d.PendingRequestCount = 0; d.ActiveRunID = ""; d.Status = t3.StatusCompleted })
	refresh(4)
	refresh(5)
	client.fail = nil
	refresh(6)
	refresh(6)

	// No reply: the marker advances (no re-read) and the stored reply is kept.
	client.fail = t3.ErrNoReply
	mutate(func(d *t3.ThreadDetail) { d.ItemCount = 7 })
	refresh(7)
	refresh(7)
	if got := text(); got != "All done." {
		t.Fatalf("no-reply check cleared stored reply: %q", got)
	}

	// Long replies are capped at 4000 chars and marked truncated.
	client.fail = nil
	long := make([]rune, 4500)
	for i := range long {
		long[i] = 'x'
	}
	if err := client.SetReply(id, t3.Reply{ItemID: "i3", Text: string(long)}); err != nil {
		t.Fatal(err)
	}
	mutate(func(d *t3.ThreadDetail) { d.ItemCount = 9 })
	refresh(8)
	lw := reply()
	if lw == nil {
		t.Fatal("capped last word missing")
	}
	if len(lw.Text) != 4000 || !lw.Truncated {
		t.Fatalf("capped last word: len %d truncated %v", len(lw.Text), lw.Truncated)
	}
}
