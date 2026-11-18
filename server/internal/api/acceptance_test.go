package api_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/t3"
)

func (e *env) complete(id int64) db.AcceptanceResult {
	e.t.Helper()
	var p db.CompletionPreview
	e.must("GET", fmt.Sprintf("/api/tasks/%d/completion-preview", id), nil, 200, &p)
	var out db.AcceptanceResult
	e.must("POST", fmt.Sprintf("/api/tasks/%d/complete", id), db.AcceptanceRequest{RequestID: fmt.Sprintf("accept-%d-%d", id, p.Revision), Revision: p.Revision, EvidenceVersion: p.EvidenceVersion}, 200, &out)
	return out
}
func (e *env) reopen(id int64) {
	e.t.Helper()
	p, err := e.store.CompletionPreview(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	e.must("POST", fmt.Sprintf("/api/tasks/%d/reopen", id), db.AcceptanceRequest{RequestID: fmt.Sprintf("reopen-%d-%d", id, p.Revision), Revision: p.Revision}, 200, nil)
}

func TestAcceptanceAloneReleasesAndFreezesEvidence(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.project("/w/acceptance")
	a := e.task(p.ID, "source", map[string]any{"start": true})
	b := e.task(p.ID, "dependent", map[string]any{"needs": []int64{a.ID}, "startMode": "auto"})
	_ = e.fake.SetReply(*a.T3ThreadID, t3.Reply{Text: "accepted reply", ItemID: "reply-1"})
	for _, status := range []t3.ThreadStatus{t3.StatusCompleted, t3.StatusIdle} {
		_ = e.fake.SetStatus(*a.T3ThreadID, status)
		e.poll()
		_ = e.runner.Reconcile(ctx)
		if got := e.getTask(b.ID); got.LaunchAttempt != 0 {
			t.Fatal("execution released dependency")
		}
	}
	_ = e.fake.Settle(*a.T3ThreadID)
	e.poll()
	_ = e.runner.Reconcile(ctx)
	if got := e.getTask(b.ID); got.LaunchAttempt != 0 {
		t.Fatal("settlement released dependency")
	}
	var preview db.CompletionPreview
	e.t3.failReadFor.Store(a.T3ThreadID)
	e.t3.failReply.Store(true)
	e.must("GET", fmt.Sprintf("/api/tasks/%d/completion-preview", a.ID), nil, 200, &preview)
	request := db.AcceptanceRequest{RequestID: "accept-once", Revision: preview.Revision, EvidenceVersion: preview.EvidenceVersion, Note: "Reviewed locally."}
	var first, again db.AcceptanceResult
	e.must("POST", fmt.Sprintf("/api/tasks/%d/complete", a.ID), request, 200, &first)
	e.must("POST", fmt.Sprintf("/api/tasks/%d/complete", a.ID), request, 200, &again)
	var detail struct {
		Acceptances []db.Acceptance `json:"acceptances"`
	}
	e.must("GET", fmt.Sprintf("/api/tasks/%d", a.ID), nil, 200, &detail)
	if len(detail.Acceptances) != 1 || detail.Acceptances[0].Evidence.Reply == nil || *detail.Acceptances[0].Evidence.Reply != "accepted reply" {
		t.Fatal("accepted result unavailable with source offline")
	}
	if first.AcceptanceID == nil || again.AcceptanceID == nil || *first.AcceptanceID != *again.AcceptanceID {
		t.Fatal("duplicate acceptance")
	}
	e.t3.failReply.Store(true)
	if err := e.runner.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.runner.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	got := e.handoffTask(b.ID)
	if got.LaunchAttempt != 1 || got.one(t) == nil || !strings.Contains(got.one(t).Text, "accepted reply") || !strings.Contains(got.one(t).Text, "Reviewed locally.") {
		t.Fatalf("accepted handoff: %+v", got)
	}
	e.reopen(a.ID)
	if got := e.getTask(b.ID); got.State != "running" || got.LaunchAttempt != 1 {
		t.Fatal("reopen disturbed claimed work")
	}
	e.must("POST", fmt.Sprintf("/api/tasks/%d/complete", a.ID), request, 200, nil)
	task, err := e.store.GetTask(ctx, a.ID)
	if err != nil || task.AcceptanceID != nil {
		t.Fatal("retry of old completion reaccepted reopened task")
	}
}

func TestAcceptanceDoesNotReopenClosedTree(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/closed-acceptance")
	a := e.task(p.ID, "source", nil)
	b := e.task(p.ID, "dependent", map[string]any{"needs": []int64{a.ID}, "startMode": "auto"})
	e.must("PATCH", fmt.Sprintf("/api/trees/%d", *b.TreeID), map[string]any{"status": "done"}, 200, nil)
	e.complete(a.ID)
	e.reopen(a.ID)
	e.complete(a.ID)
	if err := e.runner.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := e.store.GetTask(context.Background(), b.ID)
	if err != nil || got.TreeStatus == nil || *got.TreeStatus != db.TreeDone || got.LaunchAttempt != 0 {
		t.Fatal("acceptance reopened closed tree")
	}
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", b.ID), nil, 409, nil)
	e.must("PATCH", fmt.Sprintf("/api/trees/%d", *b.TreeID), map[string]any{"status": "open"}, 200, nil)
	if err := e.runner.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := e.getTask(b.ID); got.LaunchAttempt != 1 {
		t.Fatal("explicit tree reopen did not resume release")
	}
}

func TestManualReadinessReopenAndMissingEvidence(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.project("/w/manual")
	a := e.task(p.ID, "no execution", nil)
	b := e.task(p.ID, "dependent", map[string]any{"needs": []int64{a.ID}})
	e.complete(a.ID)
	_ = e.runner.Reconcile(ctx)
	if got := e.getTask(b.ID); readinessOf(got) != "ready" || got.LaunchAttempt != 0 {
		t.Fatal("manual completion must only become ready")
	}
	e.reopen(a.ID)
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", b.ID), nil, 409, nil)
	e.complete(a.ID)
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", b.ID), nil, 200, nil)
	if h := e.handoffTask(b.ID).one(t); h == nil || !strings.Contains(h.Text, "Final reply: missing at acceptance.") {
		t.Fatal("missing evidence was not handed off")
	}
}

func TestConcurrentAcceptanceAndClaimReopen(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.project("/w/race")
	a := e.task(p.ID, "a", nil)
	b := e.task(p.ID, "b", map[string]any{"needs": []int64{a.ID}})
	preview, err := e.store.CompletionPreview(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	in := db.AcceptanceRequest{RequestID: "same", Revision: preview.Revision, EvidenceVersion: preview.EvidenceVersion}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() { _, err := e.store.ChangeAcceptance(ctx, a.ID, in, false); errs <- err })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	records, err := e.store.Acceptances(ctx, a.ID)
	if err != nil || len(records) != 1 {
		t.Fatalf("acceptances: %v %v", records, err)
	}
	// A claim first pins inputs even when reopen precedes prompt composition.
	claim, err := e.store.TransitionTask(ctx, db.Transition{TaskID: b.ID, From: []db.TaskState{db.StateBacklog}, To: db.StateLaunching, BeginAttempt: true})
	if err != nil {
		t.Fatal(err)
	}
	e.reopen(a.ID)
	inputs, err := e.store.AcceptedLaunchInputs(ctx, b.ID, claim.Attempt)
	if err != nil || len(inputs) != 1 || inputs[0].ID != records[0].ID {
		t.Fatal("claim lost accepted input")
	}
	// Restart recovery retains that same claim and request ID.
	e.sync.RecoverLaunching(ctx)
	retry, err := e.store.TransitionTask(ctx, db.Transition{TaskID: b.ID, From: []db.TaskState{db.StateFailed}, To: db.StateLaunching, BeginAttempt: true})
	if err != nil || retry.Attempt != claim.Attempt || retry.RequestID != claim.RequestID {
		t.Fatalf("retry changed claim: %+v %v", retry, err)
	}
}

func TestAcceptanceEligibilityAndStaleEvidence(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/eligibility")
	a := e.task(p.ID, "a", map[string]any{"start": true})
	var preview db.CompletionPreview
	e.must("GET", fmt.Sprintf("/api/tasks/%d/completion-preview", a.ID), nil, 200, &preview)
	e.must("POST", fmt.Sprintf("/api/tasks/%d/complete", a.ID), db.AcceptanceRequest{RequestID: "active", Revision: preview.Revision, EvidenceVersion: preview.EvidenceVersion}, 409, nil)
	_ = e.fake.SetStatus(*a.T3ThreadID, t3.StatusInterrupted)
	e.poll()
	e.must("GET", fmt.Sprintf("/api/tasks/%d/completion-preview", a.ID), nil, 200, &preview)
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", a.ID), map[string]any{"title": "changed"}, 200, nil)
	e.must("POST", fmt.Sprintf("/api/tasks/%d/complete", a.ID), db.AcceptanceRequest{RequestID: "stale", Revision: preview.Revision, EvidenceVersion: preview.EvidenceVersion}, 409, nil)
	e.complete(a.ID)
	_ = e.fake.SetStatus(*a.T3ThreadID, t3.StatusRunning)
	e.poll()
	got, err := e.store.GetTask(context.Background(), a.ID)
	if err != nil || got.AcceptanceID == nil || got.State != db.StateRunning {
		t.Fatalf("activity revoked acceptance or froze observation: %+v %v", got, err)
	}
}
