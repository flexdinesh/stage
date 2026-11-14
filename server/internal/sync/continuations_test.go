package sync_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	t3sync "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
)

type continuationClient struct {
	*t3.Fake
	offline     bool
	onRead      func(string)
	onSend      func()
	loseReceipt bool
}

func (c *continuationClient) Status(ctx context.Context) t3.ConnectionStatus {
	st := c.Fake.Status(ctx)
	st.Connected = !c.offline
	return st
}
func (c *continuationClient) ReadThread(ctx context.Context, id string) (t3.ThreadDetail, error) {
	if c.onRead != nil {
		c.onRead(id)
	}
	if c.offline {
		return t3.ThreadDetail{}, t3.ErrNotConnected
	}
	return c.Fake.ReadThread(ctx, id)
}
func (c *continuationClient) QueueContinuation(ctx context.Context, id, message, request string) (t3.SendResult, error) {
	if c.onSend != nil {
		c.onSend()
	}
	out, err := c.Fake.QueueContinuation(ctx, id, message, request)
	if err == nil && c.loseReceipt {
		return t3.SendResult{}, errors.New("connection reset after accepted send")
	}
	return out, err
}

type continuationEnv struct {
	t      *testing.T
	ctx    context.Context
	store  *db.Store
	client *continuationClient
	svc    *t3sync.Service
}

func newContinuationEnv(t *testing.T) *continuationEnv {
	t.Helper()
	store := db.NewStore(testDB.Reset(t))
	client := &continuationClient{Fake: t3.NewFake()}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := t3sync.New(store, client, logger)
	svc.SetPublicURL("https://t3.test")
	return &continuationEnv{t: t, ctx: context.Background(), store: store, client: client, svc: svc}
}
func (e *continuationEnv) thread(title string, settled bool) string {
	e.t.Helper()
	res, err := e.client.LaunchThread(e.ctx, t3.LaunchInput{ProjectID: "p", Title: title})
	if err != nil {
		e.t.Fatal(err)
	}
	e.status(res.ThreadID, t3.StatusCompleted)
	if settled {
		if err := e.client.Settle(res.ThreadID); err != nil {
			e.t.Fatal(err)
		}
	}
	if err := e.svc.RefreshSession(e.ctx, res.ThreadID); err != nil {
		e.t.Fatal(err)
	}
	return res.ThreadID
}
func (e *continuationEnv) status(id string, status t3.ThreadStatus) {
	e.t.Helper()
	if err := e.client.SetStatus(id, status); err != nil {
		e.t.Fatal(err)
	}
}
func (e *continuationEnv) create(a, b string) db.Continuation {
	e.t.Helper()
	c, err := e.store.CreateContinuation(e.ctx, a, b, "  saved prompt\n")
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}
func (e *continuationEnv) run() {
	e.t.Helper()
	if err := e.svc.ReconcileContinuations(e.ctx); err != nil {
		e.t.Fatal(err)
	}
}
func (e *continuationEnv) get(id int64) db.Continuation {
	e.t.Helper()
	c, err := e.store.GetContinuation(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

func TestContinuationReadinessFreshAndRecoverable(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*continuationEnv, string, string)
		reason db.WaitReason
	}{
		{"predecessor unfinished", func(e *continuationEnv, a, b string) {
			_ = e.client.SetThread(a, func(d *t3.ThreadDetail) { d.Settled = false; d.SettledAt = nil })
		}, db.WaitPredecessorUnsettled},
		{"predecessor restarted with stale settled flag", func(e *continuationEnv, a, b string) { e.status(a, t3.StatusRunning) }, db.WaitPredecessorUnsettled},
		{"target restarted with stale mirror", func(e *continuationEnv, a, b string) { e.status(b, t3.StatusRunning) }, db.WaitTargetRunning},
		{"target input", func(e *continuationEnv, a, b string) {
			_ = e.client.SetThread(b, func(d *t3.ThreadDetail) { d.PendingRequestCount = 1 })
		}, db.WaitTargetNeedsInput},
		{"predecessor input despite settled", func(e *continuationEnv, a, b string) {
			_ = e.client.SetThread(a, func(d *t3.ThreadDetail) { d.PendingRequestCount = 1 })
		}, db.WaitPredecessorUnsettled},
		{"predecessor failed", func(e *continuationEnv, a, b string) { e.status(a, t3.StatusFailed) }, db.WaitPredecessorFailed},
		{"predecessor cancelled", func(e *continuationEnv, a, b string) { e.status(a, t3.StatusCancelled) }, db.WaitPredecessorFailed},
		{"target failed", func(e *continuationEnv, a, b string) { e.status(b, t3.StatusFailed) }, db.WaitTargetFailed},
		{"target interrupted", func(e *continuationEnv, a, b string) { e.status(b, t3.StatusInterrupted) }, db.WaitTargetFailed},
		{"target cancelled", func(e *continuationEnv, a, b string) { e.status(b, t3.StatusCancelled) }, db.WaitTargetFailed},
		{"target missing", func(e *continuationEnv, a, b string) { e.client.RemoveThread(b) }, db.WaitThreadMissing},
		{"offline", func(e *continuationEnv, a, b string) { e.client.offline = true }, db.WaitT3Offline},
		{"archived", func(e *continuationEnv, a, b string) {
			_ = e.client.SetThread(b, func(d *t3.ThreadDetail) { d.Archived = true })
		}, db.WaitThreadMissing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newContinuationEnv(t)
			a, b := e.thread("A", true), e.thread("B", false)
			c := e.create(a, b)
			tc.mutate(e, a, b)
			e.run()
			c = e.get(c.ID)
			if !slices.Contains(c.WaitReasons, tc.reason) || len(e.client.Sent) != 0 {
				t.Fatalf("state=%s reasons=%v sent=%v", c.State, c.WaitReasons, e.client.Sent)
			}
			if tc.name == "target failed" {
				e.status(b, t3.StatusCompleted)
				e.run()
				if len(e.client.Sent) != 1 {
					t.Fatal("recovered target not delivered")
				}
			}
		})
	}
}

func TestContinuationChecksAgainImmediatelyBeforeClaim(t *testing.T) {
	e := newContinuationEnv(t)
	a, b := e.thread("A", true), e.thread("B", false)
	c := e.create(a, b)
	reads := map[string]int{}
	e.client.onRead = func(id string) {
		reads[id]++
		if id == b && reads[id] == 2 {
			e.status(b, t3.StatusRunning)
		}
	}
	e.run()
	if len(e.client.Sent) != 0 || !slices.Contains(e.get(c.ID).WaitReasons, db.WaitTargetRunning) || reads[a] != 2 || reads[b] != 2 {
		t.Fatalf("reads=%v continuation=%+v sends=%v", reads, e.get(c.ID), e.client.Sent)
	}
}

func TestContinuationTracksNewRunAndBothCompositionOrders(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprint(reverse), func(t *testing.T) {
			e := newContinuationEnv(t)
			a, b, c := e.thread("A", true), e.thread("B", true), e.thread("C", false)
			var ab, bc db.Continuation
			if reverse {
				bc = e.create(b, c)
				ab = e.create(a, b)
			} else {
				ab = e.create(a, b)
				bc = e.create(b, c)
			}
			if err := e.svc.RecheckContinuation(e.ctx, ab.ID); err != nil {
				t.Fatal(err)
			}
			if len(e.client.Sent) != 0 {
				t.Fatal("save/recheck sent")
			}
			e.run()
			ab = e.get(ab.ID)
			bc = e.get(bc.ID)
			if len(e.client.Sent) != 1 || ab.RunID == nil || ab.MessageID == nil || ab.RequestID == nil || ab.State != db.ContinuationRunning || !slices.Contains(bc.WaitReasons, db.WaitUpstreamPending) {
				t.Fatalf("ab=%+v bc=%+v sends=%v", ab, bc, e.client.Sent)
			}
			want := t3sync.ContinuationEnvelope("  saved prompt\n", "A", a, "https://t3.test/fake-env/"+a)
			send := e.client.Sent[0]
			if send.Mode != t3.SendQueue || send.Message != want || send.RequestID != *ab.RequestID {
				t.Fatalf("send=%+v want=%q", send, want)
			}
			e.status(b, t3.StatusCompleted)
			e.run()
			if e.get(ab.ID).State != db.ContinuationAwaitingSettle || len(e.client.Sent) != 1 {
				t.Fatal("completion released without explicit settlement")
			}
			// Settlement from the old target run is not sufficient evidence.
			old := ab.CreatedAt.Add(-time.Hour)
			_ = e.client.SetThread(b, func(d *t3.ThreadDetail) { d.Settled = true; d.SettledAt = &old })
			e.run()
			if len(e.client.Sent) != 1 {
				t.Fatal("preexisting settlement released downstream")
			}
			_ = e.client.Settle(b)
			e.run()
			if len(e.client.Sent) != 2 || e.get(ab.ID).State != db.ContinuationSettled || e.get(bc.ID).State != db.ContinuationRunning {
				t.Fatalf("ab=%+v bc=%+v sends=%v", e.get(ab.ID), e.get(bc.ID), e.client.Sent)
			}
			e.status(c, t3.StatusCancelled)
			e.run()
			if e.get(bc.ID).State != db.ContinuationBlocked || !slices.Contains(e.get(bc.ID).WaitReasons, db.WaitTargetFailed) {
				t.Fatal("cancelled delivered run did not block")
			}
		})
	}
}

func TestCancelledContinuationBlocksWithoutSplicing(t *testing.T) {
	e := newContinuationEnv(t)
	a, b, c := e.thread("A", true), e.thread("B", true), e.thread("C", false)
	ab := e.create(a, b)
	bc := e.create(b, c)
	if _, err := e.store.MutateContinuation(e.ctx, ab.ID, "cancel", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.MutateContinuation(e.ctx, bc.ID, "resume", nil); err != nil {
		t.Fatal(err)
	}
	e.run()
	child := e.get(bc.ID)
	if len(e.client.Sent) != 0 || child.State != db.ContinuationBlocked || !slices.Contains(child.WaitReasons, db.WaitUpstreamCancelled) || child.PreviousContinuationID == nil || *child.PreviousContinuationID != ab.ID {
		t.Fatalf("child=%+v sends=%v", child, e.client.Sent)
	}
}

func TestContinuationAmbiguousSendNeverRetried(t *testing.T) {
	e := newContinuationEnv(t)
	a, b := e.thread("A", true), e.thread("B", false)
	c := e.create(a, b)
	e.client.loseReceipt = true
	e.run()
	e.run()
	c = e.get(c.ID)
	if len(e.client.Sent) != 1 || c.State != db.ContinuationUncertain || c.RequestID == nil || c.LastError == nil {
		t.Fatalf("c=%+v sends=%v", c, e.client.Sent)
	}
	if _, err := e.store.MutateContinuation(e.ctx, c.ID, "resume", nil); err == nil {
		t.Fatal("uncertain resume accepted")
	}
	e.status(b, t3.StatusCompleted)
	_ = e.client.Settle(b)
	e.run()
	if len(e.client.Sent) != 1 || e.get(c.ID).State != db.ContinuationUncertain {
		t.Fatal("settlement guessed ambiguous delivery outcome")
	}
}

func TestContinuationCrashAroundSend(t *testing.T) {
	for _, delivered := range []bool{false, true} {
		t.Run(fmt.Sprint(delivered), func(t *testing.T) {
			e := newContinuationEnv(t)
			a, b := e.thread("A", true), e.thread("B", false)
			c := e.create(a, b)
			c, claimed, err := e.store.ClaimContinuation(e.ctx, c, "persisted envelope")
			if err != nil || !claimed {
				t.Fatalf("claim=%v err=%v", claimed, err)
			}
			if delivered {
				if _, err := e.client.QueueContinuation(e.ctx, b, "persisted envelope", *c.RequestID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := testDB.Pool.Exec(e.ctx, `UPDATE continuations SET claim_expires_at=now()-interval '1 second' WHERE id=$1`, c.ID); err != nil {
				t.Fatal(err)
			}
			e.run()
			e.run()
			want := 0
			if delivered {
				want = 1
			}
			if len(e.client.Sent) != want || e.get(c.ID).State != db.ContinuationUncertain {
				t.Fatalf("c=%+v sends=%v", e.get(c.ID), e.client.Sent)
			}
		})
	}
}

func TestContinuationConcurrentWorkersAndClaimedMutation(t *testing.T) {
	e := newContinuationEnv(t)
	a, b := e.thread("A", true), e.thread("B", false)
	c := e.create(a, b)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	e.client.onSend = func() { once.Do(func() { close(entered) }); <-release }
	errs := make(chan error, 2)
	go func() { errs <- e.svc.ReconcileContinuations(e.ctx) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("send not reached")
	}
	for _, action := range []string{"edit", "pause", "cancel"} {
		p := "new"
		if _, err := e.store.MutateContinuation(e.ctx, c.ID, action, &p); err == nil {
			t.Fatalf("claimed %s accepted", action)
		}
	}
	other := t3sync.New(e.store, e.client, slog.New(slog.NewTextHandler(io.Discard, nil)))
	go func() { errs <- other.ReconcileContinuations(e.ctx) }()
	close(release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if len(e.client.Sent) != 1 {
		t.Fatalf("duplicate sends=%v", e.client.Sent)
	}
	var events int
	if err := testDB.Pool.QueryRow(e.ctx, `SELECT count(*) FROM continuation_events WHERE continuation_id=$1`, c.ID).Scan(&events); err != nil || events < 3 {
		t.Fatalf("events=%d err=%v", events, err)
	}
}

func TestContinuationRunMissingCannotSettle(t *testing.T) {
	e := newContinuationEnv(t)
	a, b := e.thread("A", true), e.thread("B", false)
	c := e.create(a, b)
	e.run()
	e.status(b, t3.StatusCompleted)
	_ = e.client.Settle(b)
	_ = e.client.SetThread(b, func(d *t3.ThreadDetail) { d.RecentRuns = []t3.Run{{RunID: "different", Status: t3.StatusCompleted}} })
	e.run()
	c = e.get(c.ID)
	if c.State != db.ContinuationBlocked || !slices.Contains(c.WaitReasons, db.WaitDeliveryUncertain) {
		t.Fatalf("unrelated run settled continuation: %+v", c)
	}
}

func TestContinuationEnvelope(t *testing.T) {
	prompt := "\n  exact\n\n"
	got := t3sync.ContinuationEnvelope(prompt, "Title", "source/%", "")
	if !strings.HasPrefix(got, prompt+"\n\n---\nPrerequisite session\nTitle: Title\nThread ID: source/%") || strings.Contains(got, "T3:") {
		t.Fatalf("envelope=%q", got)
	}
}
