package codex_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/codex"
	"github.com/flexdinesh/stage/server/internal/codex/codextest"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// launched starts a thread through the adapter, as a Stage launch does
// (Stage unsubscribes after the first turn starts).
func launched(t *testing.T, r rig) execution.SessionRef {
	t.Helper()
	var cps checkpoints
	o, err := r.adapter.Launch(t.Context(), intent("/w/app"), execution.Checkpoint{}, cps.save)
	if err != nil {
		t.Fatal(err)
	}
	return o.Session
}

func commandApproval() map[string]any {
	return map[string]any{
		"kind": "command", "itemId": "exec-1", "startedAtMs": 1795860205938,
		"reason": "Create the scratch file?", "command": "touch x", "cwd": "/w/app",
		"availableDecisions": []string{"accept", "decline", "cancel"},
	}
}

func track(t *testing.T, r rig, refs ...execution.SessionRef) <-chan error {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- r.adapter.TrackedEvents(ctx, refs, func(context.Context, execution.Observation) error { return nil })
	}()
	t.Cleanup(cancel)
	return done
}

func until(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("never observed %s", what)
}

func requests(t *testing.T, r rig, ref execution.SessionRef, count int) []execution.Request {
	t.Helper()
	var out []execution.Request
	until(t, "pending requests", func() bool {
		var err error
		out, err = r.adapter.Requests(t.Context(), ref)
		if err != nil {
			t.Fatal(err)
		}
		return len(out) == count
	})
	return out
}

func TestTrackedApprovalsReplayAndKeepTheTurn(t *testing.T) {
	r := newRig(t)
	ref := launched(t, r)
	other := r.srv.AddThread(codextest.Thread{ID: "019a0000-0000-7000-8000-00000000ffff", Cwd: "/w/app", Status: "active"})
	r.srv.Request(other.ID, "item/commandExecution/requestApproval", commandApproval())
	id := r.srv.Request(ref.ExternalSessionID, "item/commandExecution/requestApproval", commandApproval())
	requests(t, r, ref, 0) // a query alone never resumes
	if n := r.srv.Calls("thread/resume"); n != 0 {
		t.Fatalf("query resumed %d threads", n)
	}
	before := r.srv.Thread(ref.ExternalSessionID)
	track(t, r, ref)
	cmd := requests(t, r, ref, 1)[0]
	if cmd.ID != "exec-1" || cmd.Title != "Run a command" || cmd.Preview != "touch x" ||
		!strings.Contains(cmd.Detail, "Working directory /w/app") || !strings.Contains(cmd.Detail, "Create the scratch file?") {
		t.Fatalf("command %+v", cmd)
	}
	if !slices.Equal(choiceIDs(cmd.Options), []string{"accept", "decline", "cancel"}) {
		t.Fatalf("command options %+v", cmd.Options)
	}
	for range 3 {
		requests(t, r, ref, 1)
	}
	after := r.srv.Thread(ref.ExternalSessionID)
	if len(after.Turns) != 1 || after.Turns[0].ID != before.Turns[0].ID || after.Model != before.Model ||
		after.Approval != before.Approval || after.Sandbox != before.Sandbox || r.srv.Subscribed(other.ID) != 0 ||
		r.srv.Subscribed(ref.ExternalSessionID) != 1 || r.srv.Calls("thread/resume") != 1 {
		t.Fatalf("resumed thread %+v, resumes %d", after, r.srv.Calls("thread/resume"))
	}
	var invalid *execution.Invalid
	if err := r.adapter.Respond(t.Context(), ref, cmd.ID, execution.Response{Option: "acceptForSession"}); !errors.As(err, &invalid) {
		t.Fatal("unexpected option", err)
	}
	if err := r.adapter.Respond(t.Context(), ref, cmd.ID, execution.Response{Option: "accept"}); err != nil {
		t.Fatal(err)
	}
	until(t, "answer", func() bool { return len(r.srv.Answers()) == 1 })
	if got := r.srv.Answers()[0]; got.ID != id || string(got.Result) != `{"decision":"accept"}` {
		t.Fatalf("answer %+v", got)
	}
	if err := r.adapter.Respond(t.Context(), ref, cmd.ID, execution.Response{Option: "accept"}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatal("second answer", err)
	}
	r.srv.Finish(ref.ExternalSessionID, "completed", "Done.")
	until(t, "terminal unsubscribe", func() bool { return r.srv.Subscribed(ref.ExternalSessionID) == 0 })
}

func TestTrackedApprovalsReconnectAndCompetingAnswer(t *testing.T) {
	r := newRig(t)
	ref := launched(t, r)
	done := track(t, r, ref)
	until(t, "running subscription", func() bool { return r.srv.Subscribed(ref.ExternalSessionID) == 1 })
	id := r.srv.Request(ref.ExternalSessionID, "item/commandExecution/requestApproval", commandApproval())
	cmd := requests(t, r, ref, 1)[0]
	r.srv.DropClients()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("watch stayed open")
	}
	requests(t, r, ref, 0)
	track(t, r, ref)
	if got := requests(t, r, ref, 1)[0]; got.ID != cmd.ID {
		t.Fatalf("replay changed request ID: %s", got.ID)
	}
	if err := r.srv.AnswerFromAnotherClient(t.Context(), ref.ExternalSessionID, id, map[string]any{"decision": "decline"}); err != nil {
		t.Fatal(err)
	}
	requests(t, r, ref, 0)
	if err := r.adapter.Respond(t.Context(), ref, cmd.ID, execution.Response{Option: "accept"}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatal(err)
	}
	if got := r.srv.Answers(); len(got) != 1 || string(got[0].Result) != `{"decision":"decline"}` {
		t.Fatalf("answers %+v", got)
	}
}

func TestTrackedApprovalsFileAndPermissionDetails(t *testing.T) {
	r := newRig(t)
	ref := launched(t, r)
	track(t, r, ref)
	until(t, "running subscription", func() bool { return r.srv.Subscribed(ref.ExternalSessionID) == 1 })
	const fileID = "file-1"
	r.srv.StartFileChange(ref.ExternalSessionID, fileID, []map[string]any{{
		"path": "src/main.go", "kind": map[string]any{"type": "update"},
		"diff": "--- a/src/main.go\n+++ b/src/main.go\n@@ -1 +1,2 @@\n-old\n+new\n+another\n",
	}})
	r.srv.Request(ref.ExternalSessionID, "item/fileChange/requestApproval", map[string]any{"itemId": fileID})
	permissions := map[string]any{"network": map[string]bool{"enabled": true}, "fileSystem": nil}
	r.srv.Request(ref.ExternalSessionID, "item/permissions/requestApproval", map[string]any{"itemId": "perm-1", "permissions": permissions})
	for _, method := range []string{"item/tool/requestUserInput", "mcpServer/elicitation/request", "applyPatchApproval", "execCommandApproval"} {
		r.srv.Request(ref.ExternalSessionID, method, map[string]any{"itemId": method})
	}
	rs := requests(t, r, ref, 2)
	if rs[0].Title != "Apply file changes" || rs[0].Preview != "src/main.go  +2 −1" ||
		!slices.Equal(choiceIDs(rs[0].Options), []string{"accept", "acceptForSession", "decline", "cancel"}) {
		t.Fatalf("file request %+v", rs[0])
	}
	if rs[1].Title != "Grant permissions" || !strings.Contains(rs[1].Preview, "network") ||
		!slices.Equal(choiceIDs(rs[1].Options), []string{"turn", "session", "decline"}) {
		t.Fatalf("permissions request %+v", rs[1])
	}
	if err := r.adapter.Respond(t.Context(), ref, rs[0].ID, execution.Response{Option: "decline"}); err != nil {
		t.Fatal(err)
	}
	if err := r.adapter.Respond(t.Context(), ref, rs[1].ID, execution.Response{Option: "session"}); err != nil {
		t.Fatal(err)
	}
	if err := r.adapter.Respond(t.Context(), ref, "item/tool/requestUserInput", execution.Response{Option: "accept"}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatal(err)
	}
	until(t, "two supported answers", func() bool { return len(r.srv.Answers()) == 2 })
	as := r.srv.Answers()
	if string(as[0].Result) != `{"decision":"decline"}` || string(as[1].Result) != `{"permissions":{"network":{"enabled":true}},"scope":"session"}` {
		t.Fatalf("answers %s / %s", as[0].Result, as[1].Result)
	}
	r.srv.Request(ref.ExternalSessionID, "item/commandExecution/requestApproval", commandApproval())
	cmd := requests(t, r, ref, 1)[0]
	r.srv.Finish(ref.ExternalSessionID, "interrupted", "")
	requests(t, r, ref, 0)
	if err := r.adapter.Respond(t.Context(), ref, cmd.ID, execution.Response{Option: "accept"}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatal("answer after turn completion", err)
	}
}

func TestRespondAvailabilityRequiresVerifiedDaemon(t *testing.T) {
	r := newRig(t)
	r.daemon.VersionTag = "0.171.0"
	ctx := t.Context()
	c, err := r.adapter.Features(ctx)
	if op := c.Operations[execution.OpRespond]; err != nil || op.Available || op.Reason != "Update Codex to answer in Stage." {
		t.Fatal(op, err)
	}
	client := codex.NewClient(failingStart{r.daemon}, "test")
	t.Cleanup(client.Close)
	r.srv.Stop()
	c, err = codex.New(client, conn, "").Features(ctx)
	if op := c.Operations[execution.OpRespond]; err != nil || op.Available || !strings.Contains(op.Reason, "could not start the Codex daemon") {
		t.Fatal(op, err)
	}
}

func choiceIDs(cs []execution.Choice) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}

func TestSendIsBusyWhileATurnRuns(t *testing.T) {
	r := newRig(t)
	ref := launched(t, r)
	var busy *execution.Busy
	if err := r.adapter.Send(t.Context(), ref, execution.Message{Text: "more"}); !errors.As(err, &busy) || busy.Reason != "Codex is working. Send once it finishes." {
		t.Fatal("running", err)
	}
	r.srv.Request(ref.ExternalSessionID, "item/commandExecution/requestApproval", commandApproval())
	if err := r.adapter.Send(t.Context(), ref, execution.Message{Text: "more"}); !errors.As(err, &busy) || busy.Reason != "Codex is waiting on an approval or question. Answer that first." {
		t.Fatal("waiting", err)
	}
	if n := len(r.srv.Thread(ref.ExternalSessionID).Turns); n != 1 || r.srv.Calls("thread/resume") != 0 {
		t.Fatalf("turns %d resumes %d", n, r.srv.Calls("thread/resume"))
	}
}

func TestSendStartsATurnOnAnIdleThread(t *testing.T) {
	r := newRig(t)
	ref := launched(t, r)
	r.srv.Finish(ref.ExternalSessionID, "completed", "Done.")
	if err := r.adapter.Send(t.Context(), ref, execution.Message{Text: "also run lint", RequestID: "steering-7-12"}); err != nil {
		t.Fatal(err)
	}
	th := r.srv.Thread(ref.ExternalSessionID)
	if len(th.Turns) != 2 || th.Turns[1].Prompt != "also run lint" || th.Turns[1].ClientID != "steering-7-12" || th.Status != "active" {
		t.Fatalf("thread %+v turn %+v", th, th.Turns[len(th.Turns)-1])
	}
	if r.srv.Calls("thread/resume") != 1 || r.srv.Subscribed(ref.ExternalSessionID) != 0 {
		t.Fatalf("resumes %d subscribed %d", r.srv.Calls("thread/resume"), r.srv.Subscribed(ref.ExternalSessionID))
	}
	// A retry of the same send finds it delivered.
	r.srv.Finish(ref.ExternalSessionID, "completed", "Linted.")
	if err := r.adapter.Send(t.Context(), ref, execution.Message{Text: "also run lint", RequestID: "steering-7-12"}); err != nil {
		t.Fatal(err)
	}
	if n := len(r.srv.Thread(ref.ExternalSessionID).Turns); n != 2 {
		t.Fatalf("retry started turn %d", n)
	}
	// Without a request id, Stage still names the message.
	if err := r.adapter.Send(t.Context(), ref, execution.Message{Text: "thanks"}); err != nil {
		t.Fatal(err)
	}
	if tu := r.srv.Thread(ref.ExternalSessionID).Turns[2]; tu.ClientID == "" || tu.Prompt != "thanks" {
		t.Fatalf("turn %+v", tu)
	}
}

// For a moment after turn/start and unsubscribe, Codex reports the thread
// idle and the new turn interrupted without completedAt (live in #555,
// every send). Observe waits it out and reports the running turn: an
// interrupted reading would release a steered tree's holds.
func TestObserveAfterSendWaitsForTheTurnToSettle(t *testing.T) {
	r := newRig(t)
	ref := launched(t, r)
	r.srv.Finish(ref.ExternalSessionID, "completed", "Done.")
	r.srv.StartSettles(3)
	if err := r.adapter.Send(t.Context(), ref, execution.Message{Text: "digest", RequestID: "steering-1-1"}); err != nil {
		t.Fatal(err)
	}
	o, err := r.adapter.Observe(t.Context(), ref)
	if err != nil || o.State != "running" || o.ExecutionID != r.srv.Thread(ref.ExternalSessionID).Turns[1].ID {
		t.Fatalf("%+v %v", o, err)
	}
}

// A real interrupt carries completedAt and reads interrupted at once; an
// unclosed one that persists past the settle window reads interrupted too.
func TestObserveReadsInterruptedTurns(t *testing.T) {
	r := newRig(t)
	ref := launched(t, r)
	r.srv.Finish(ref.ExternalSessionID, "interrupted", "")
	o, err := r.adapter.Observe(t.Context(), ref)
	if err != nil || o.State != "interrupted" || o.EndedAt == nil {
		t.Fatalf("closed %+v %v", o, err)
	}
	r.srv.StartSettles(-1)
	if err := r.adapter.Send(t.Context(), ref, execution.Message{Text: "again"}); err != nil {
		t.Fatal(err)
	}
	o, err = r.adapter.Observe(t.Context(), ref)
	if err != nil || o.State != "interrupted" || o.EndedAt != nil || o.ExecutionID != r.srv.Thread(ref.ExternalSessionID).Turns[1].ID {
		t.Fatalf("unclosed %+v %v", o, err)
	}
}

func TestSendResumesAThreadTheDaemonUnloaded(t *testing.T) {
	r := newRig(t)
	ref := launched(t, r)
	r.srv.Finish(ref.ExternalSessionID, "completed", "Done.")
	r.srv.Restart(t)
	if th := r.srv.Thread(ref.ExternalSessionID); th.Status != "notLoaded" {
		t.Fatalf("status %s", th.Status)
	}
	if err := r.adapter.Send(t.Context(), ref, execution.Message{Text: "continue"}); err != nil {
		t.Fatal(err)
	}
	if th := r.srv.Thread(ref.ExternalSessionID); len(th.Turns) != 2 || th.Status != "active" {
		t.Fatalf("thread %+v", th)
	}
}

func TestSendToAnArchivedThreadIsRefusedWithCodexsFix(t *testing.T) {
	r := newRig(t)
	ref := launched(t, r)
	r.srv.Finish(ref.ExternalSessionID, "completed", "Done.")
	r.srv.Archive(ref.ExternalSessionID)
	var refused *execution.Refused
	if err := r.adapter.Send(t.Context(), ref, execution.Message{Text: "more"}); !errors.As(err, &refused) || !strings.Contains(refused.Reason, "codex unarchive") {
		t.Fatal("archived", err)
	}
	if err := r.adapter.Send(t.Context(), execution.SessionRef{ConnectionID: conn, ExternalSessionID: "019a0000-0000-7000-8000-00000000ffff"}, execution.Message{Text: "hi"}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatal("unknown thread", err)
	}
}
