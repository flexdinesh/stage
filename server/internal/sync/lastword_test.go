package sync_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	t3sync "github.com/flexdinesh/stage/server/internal/sync"
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
	svc := t3sync.New(store, client, slog.New(slog.NewTextHandler(io.Discard, nil)))
	res, err := client.LaunchThread(ctx, t3.LaunchInput{ProjectID: "p", Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	id := res.ThreadID
	mutate := func(fn func(*t3.ThreadDetail)) {
		t.Helper()
		if err := client.SetThread(id, fn); err != nil {
			t.Fatal(err)
		}
	}
	refresh := func(wantCalls int) {
		t.Helper()
		if err := svc.RefreshSession(ctx, id); err != nil {
			t.Fatal(err)
		}
		if client.calls != wantCalls {
			t.Fatalf("LatestReply calls = %d, want %d", client.calls, wantCalls)
		}
	}
	text := func() string {
		t.Helper()
		lw, err := store.GetThreadLastWord(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if lw == nil {
			return ""
		}
		return lw.Text
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
	mutate(func(d *t3.ThreadDetail) { d.ActiveRunID = "run-2"; d.Status = t3.StatusWaiting; d.PendingRequestCount = 1 })
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
	lw, err := store.GetThreadLastWord(ctx, id)
	if err != nil || lw == nil {
		t.Fatalf("capped last word: %v %v", lw, err)
	}
	if len(lw.Text) != 4000 || !lw.Truncated {
		t.Fatalf("capped last word: len %d truncated %v", len(lw.Text), lw.Truncated)
	}
}
