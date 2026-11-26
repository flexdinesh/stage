package codex_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/codex"
	"github.com/flexdinesh/stage/server/internal/codex/codextest"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/machine"
)

const conn = 4

type rig struct {
	srv     *codextest.Server
	daemon  *codextest.Daemon
	client  *codex.Client
	adapter *codex.Adapter
}

func newRig(t *testing.T) rig {
	t.Helper()
	srv := codextest.New(t)
	d := &codextest.Daemon{Srv: srv, T: t}
	c := codex.NewClient(d, "test")
	t.Cleanup(c.Close)
	return rig{srv: srv, daemon: d, client: c, adapter: codex.New(c, conn, "")}
}

// checkpoints records saves; fail makes the next save fail.
type checkpoints struct {
	mu   sync.Mutex
	last execution.Checkpoint
	n    int
	fail bool
}

func (c *checkpoints) save(_ context.Context, cp execution.Checkpoint) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail {
		return errors.New("disk full")
	}
	c.last, c.n = cp, c.n+1
	return nil
}

func intent(dir string) execution.Intent {
	return execution.Intent{
		RequestID: "req-1", ConnectionID: conn, Project: dir, Directory: dir, Title: "Fix the bug", Prompt: "Fix it",
		Config: execution.Config{Model: "gpt-test", Permission: "auto", Options: []execution.ModelOption{{ID: "effort", Value: execution.StringValue("high")}}},
	}
}

func phase(t *testing.T, cp execution.Checkpoint) (string, string) {
	t.Helper()
	var r struct{ Phase, Thread string }
	if err := json.Unmarshal(cp.Data, &r); err != nil {
		t.Fatal(err)
	}
	return r.Phase, r.Thread
}

func TestLaunchStartsThreadThenTurnAndObservesToDone(t *testing.T) {
	r := newRig(t)
	ctx := t.Context()
	var cps checkpoints
	in := intent("/w/app")
	in.Images = []execution.Image{{Name: "shot.png", MimeType: "image/png", Data: []byte{0x89, 'P', 'N', 'G'}}}
	o, err := r.adapter.Launch(ctx, in, execution.Checkpoint{}, cps.save)
	if err != nil {
		t.Fatal(err)
	}
	if o.State != "running" || !o.Active || o.Project != "/w/app" || o.Title != "Fix the bug" || o.Config.Model != "gpt-test" {
		t.Fatalf("launched %+v", o)
	}
	th := r.srv.Thread(o.Session.ExternalSessionID)
	if th == nil || len(th.Turns) != 1 || th.Turns[0].ClientID != "req-1" || th.Turns[0].Images != 1 || th.Effort != "high" || th.Approval != "on-request" || th.Sandbox != "workspace-write" {
		t.Fatalf("daemon thread %+v", th)
	}
	if p, id := phase(t, cps.last); p != "turn" || id != th.ID || cps.n != 2 {
		t.Fatalf("checkpoint %s %s after %d saves", p, id, cps.n)
	}
	if got := r.srv.Calls("thread/unsubscribe"); got != 1 {
		t.Fatalf("unsubscribe calls %d", got)
	}
	if o.Links.Command != "cd /w/app && codex resume "+th.ID {
		t.Fatalf("resume %q", o.Links.Command)
	}

	r.srv.Wait(th.ID)
	if o, err = r.adapter.Observe(ctx, o.Session); err != nil || o.State != "needs_input" {
		t.Fatal(o, err)
	}
	refs, err := r.adapter.Attention(ctx)
	if err != nil || len(refs) != 1 || refs[0] != o.Session {
		t.Fatal(refs, err)
	}
	r.srv.Finish(th.ID, "completed", "All fixed.")
	if o, err = r.adapter.Observe(ctx, o.Session); err != nil || o.State != "done" || o.Reply == nil || o.Reply.Text != "All fixed." || o.EndedAt == nil || o.ExecutionID == "" {
		t.Fatalf("done %+v %v", o, err)
	}
	opening, err := r.adapter.Opening(ctx, o.Session, false)
	if err != nil || opening.Text != "Fix it" {
		t.Fatal(opening, err)
	}
	if o.Links.Command != "cd /w/app && codex resume "+th.ID {
		t.Fatalf("resume %q", o.Links.Command)
	}
}

// A lost turn/start reply: the turn ran. Replay finds it by its client
// message id and never starts a second turn.
func TestLaunchReplayAfterLostTurnReplyFindsTheTurn(t *testing.T) {
	r := newRig(t)
	ctx := t.Context()
	var cps checkpoints
	in := intent("/w/app")
	r.srv.DropReply("turn/start", 1)
	tctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	_, err := r.adapter.Launch(tctx, in, execution.Checkpoint{}, cps.save)
	cancel()
	if err == nil {
		t.Fatal("lost reply reported success")
	}
	var rej *execution.Rejected
	if errors.As(err, &rej) {
		t.Fatal("lost reply certified as rejected")
	}
	o, err := r.adapter.Launch(ctx, in, cps.last, cps.save)
	if err != nil || o.State != "running" {
		t.Fatal(o, err)
	}
	if n := r.srv.Calls("turn/start"); n != 1 {
		t.Fatalf("turn/start ran %d times", n)
	}
	if ids := r.srv.Threads(); len(ids) != 1 {
		t.Fatalf("threads %v", ids)
	}
}

// A lost thread/start reply: no id was recorded, and the orphan has no
// turn. Replay starts a new thread and one turn.
func TestLaunchReplayAfterLostThreadReplyStartsAgain(t *testing.T) {
	r := newRig(t)
	ctx := t.Context()
	var cps checkpoints
	in := intent("/w/app")
	r.srv.DropReply("thread/start", 1)
	tctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	_, err := r.adapter.Launch(tctx, in, execution.Checkpoint{}, cps.save)
	cancel()
	if err == nil {
		t.Fatal("lost reply reported success")
	}
	if p, id := phase(t, cps.last); p != "thread" || id != "" {
		t.Fatal(p, id)
	}
	o, err := r.adapter.Launch(ctx, in, cps.last, cps.save)
	if err != nil || o.State != "running" || r.srv.Calls("turn/start") != 1 {
		t.Fatal(o, err)
	}
	for _, id := range r.srv.Threads() {
		if th := r.srv.Thread(id); id != o.Session.ExternalSessionID && len(th.Turns) != 0 {
			t.Fatalf("orphan thread ran a turn: %+v", th)
		}
	}
}

// The daemon restarted between creating the thread and its turn: the
// thread never persisted, so replay starts over on a new thread.
func TestLaunchReplayAfterDaemonRestartBeforeTurn(t *testing.T) {
	r := newRig(t)
	ctx := t.Context()
	var cps checkpoints
	in := intent("/w/app")
	r.srv.Unsupported("turn/start")
	if _, err := r.adapter.Launch(ctx, in, execution.Checkpoint{}, cps.save); err == nil {
		t.Fatal("turn/start failure reported success")
	}
	_, first := phase(t, cps.last)
	r.srv.Restart(t)
	r.srv.Supported("turn/start")
	if r.srv.Thread(first) != nil {
		t.Fatal("unpersisted thread survived the restart")
	}
	o, err := r.adapter.Launch(ctx, in, cps.last, cps.save)
	if err != nil || o.State != "running" || o.Session.ExternalSessionID == first {
		t.Fatalf("%+v %v (first %s)", o, err, first)
	}
	if _, id := phase(t, cps.last); id != o.Session.ExternalSessionID {
		t.Fatal("new thread not checkpointed", id)
	}
}

func TestLaunchRejectsBeforeAnyEffect(t *testing.T) {
	r := newRig(t)
	ctx := t.Context()
	for name, edit := range map[string]func(*execution.Intent){
		// Codex declares no launch options: a worktree is an unknown option.
		"worktree": func(in *execution.Intent) {
			in.Config.Options = append(in.Config.Options, execution.ModelOption{ID: "worktree", Value: execution.BoolValue(true)})
		},
		"unknown model":  func(in *execution.Intent) { in.Config.Model = "nope" },
		"unknown effort": func(in *execution.Intent) { in.Config.Options[0].Value = execution.StringValue("ultra") },
		"permission":     func(in *execution.Intent) { in.Config.Permission = "yolo" },
		"not an image": func(in *execution.Intent) {
			in.Images = []execution.Image{{Name: "a.txt", MimeType: "text/plain", Data: []byte("x")}}
		},
		"no directory": func(in *execution.Intent) { in.Directory = "" },
	} {
		t.Run(name, func(t *testing.T) {
			in := intent("/w/app")
			edit(&in)
			var cps checkpoints
			_, err := r.adapter.Launch(ctx, in, execution.Checkpoint{}, cps.save)
			var rej *execution.Rejected
			if !errors.As(err, &rej) || cps.n != 0 {
				t.Fatalf("%v (saves %d)", err, cps.n)
			}
		})
	}
	if r.srv.Calls("thread/start") != 0 {
		t.Fatal("a rejected launch started a thread")
	}
}

func TestLaunchStopsWhenCheckpointFails(t *testing.T) {
	r := newRig(t)
	cps := checkpoints{fail: true}
	if _, err := r.adapter.Launch(t.Context(), intent("/w/app"), execution.Checkpoint{}, cps.save); err == nil {
		t.Fatal("launched without a checkpoint")
	}
	if r.srv.Calls("thread/start") != 0 {
		t.Fatal("effect before the checkpoint")
	}
}

// A replayed checkpoint whose thread holds a turn Stage did not start is
// not this launch's: the user decides.
func TestLaunchReplayRefusesAForeignTurn(t *testing.T) {
	r := newRig(t)
	ctx := t.Context()
	var cps checkpoints
	o, err := r.adapter.Launch(ctx, intent("/w/app"), execution.Checkpoint{}, cps.save)
	if err != nil {
		t.Fatal(err)
	}
	other := intent("/w/app")
	other.RequestID = "req-2"
	r.srv.Finish(o.Session.ExternalSessionID, "completed", "x")
	// A checkpoint for req-2 pointing at req-1's thread (digest differs, so
	// it is refused outright).
	if _, err := r.adapter.Launch(ctx, other, cps.last, cps.save); err == nil || !strings.Contains(err.Error(), "cannot be verified") {
		t.Fatal(err)
	}
}

func TestStopInterruptsTheRunningTurn(t *testing.T) {
	r := newRig(t)
	ctx := t.Context()
	var cps checkpoints
	o, err := r.adapter.Launch(ctx, intent("/w/app"), execution.Checkpoint{}, cps.save)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.adapter.Stop(ctx, o.Session); err != nil {
		t.Fatal(err)
	}
	if o, err = r.adapter.Observe(ctx, o.Session); err != nil || o.State != "interrupted" {
		t.Fatal(o, err)
	}
	if err := r.adapter.Stop(ctx, o.Session); err != nil {
		t.Fatal("stopping an idle thread", err)
	}
}

func TestArchivedThreadReportsClosure(t *testing.T) {
	r := newRig(t)
	ctx := t.Context()
	var cps checkpoints
	o, err := r.adapter.Launch(ctx, intent("/w/app"), execution.Checkpoint{}, cps.save)
	if err != nil {
		t.Fatal(err)
	}
	r.srv.Finish(o.Session.ExternalSessionID, "completed", "done")
	r.srv.Archive(o.Session.ExternalSessionID)
	o, err = r.adapter.Observe(ctx, o.Session)
	if err != nil || o.State != "done" || o.Closed == nil || o.Closed.Signal != execution.ClosedArchived || !o.Archived {
		t.Fatalf("%+v %v", o, err)
	}
}

func TestResolveLinkAdoptsOnlyFollowableThreads(t *testing.T) {
	r := newRig(t)
	ctx := t.Context()
	own := r.srv.AddThread(codextest.Thread{ID: "019a0000-0000-7000-8000-00000000aaaa", Cwd: "/w", Originator: "codex-tui"})
	wrapped := r.srv.AddThread(codextest.Thread{ID: "019a0000-0000-7000-8000-00000000bbbb", Cwd: "/w", Originator: "Wrapper"})
	eph := r.srv.AddThread(codextest.Thread{ID: "019a0000-0000-7000-8000-00000000cccc", Cwd: "/w", Ephemeral: true})
	sub := r.srv.AddThread(codextest.Thread{ID: "019a0000-0000-7000-8000-00000000dddd", Cwd: "/w", Parent: own.ID})
	if ref, err := r.adapter.ResolveLink(ctx, "codex resume "+strings.ToUpper(own.ID)); err != nil || ref.ExternalSessionID != own.ID || ref.ConnectionID != conn {
		t.Fatal(ref, err)
	}
	// A thread another client started resolves; it reports its origin raw
	// for the coordinator to refuse a wrapped one.
	if ref, err := r.adapter.ResolveLink(ctx, wrapped.ID); err != nil || ref.ExternalSessionID != wrapped.ID {
		t.Fatal(ref, err)
	}
	if o, err := r.adapter.Observe(ctx, execution.SessionRef{ConnectionID: conn, ExternalSessionID: wrapped.ID}); err != nil || o.Origin != "Wrapper" {
		t.Fatalf("%+v %v", o, err)
	}
	for _, id := range []string{eph.ID, sub.ID} {
		if _, err := r.adapter.ResolveLink(ctx, id); err == nil {
			t.Fatal("adopted", id)
		}
	}
	if _, err := r.adapter.ResolveLink(ctx, "019a0000-0000-7000-8000-00000000ffff"); !errors.Is(err, execution.ErrNotFound) {
		t.Fatal("unknown thread", err)
	}
	if _, err := r.adapter.ResolveLink(ctx, "not a thread"); err == nil {
		t.Fatal("garbage adopted")
	}
}

func TestWorkspacesAreTrustedProjectsWithGitImportable(t *testing.T) {
	r := newRig(t)
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "config"), []byte("[remote \"origin\"]\n\turl = git@github.com:acme/app.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "web")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	plain := t.TempDir()
	r.srv.Trust(sub, "trusted")
	r.srv.Trust(plain, "trusted")
	r.srv.Trust("/untrusted", "untrusted")
	r.srv.Trust("relative/path", "trusted")
	ws, err := r.adapter.Workspaces(t.Context())
	if err != nil || len(ws) != 2 {
		t.Fatalf("%+v %v", ws, err)
	}
	// Harness facts only: the machine's ops read the Git evidence.
	for _, w := range ws {
		if !w.Local || w.ImportIf != execution.ImportInCheckout || w.Importable || w.Repo != "" || w.Evidence != nil {
			t.Fatalf("harness facts %+v", w)
		}
	}
	if ws, err = (machine.LocalOps{}).Inventory(t.Context(), ws); err != nil {
		t.Fatal(err)
	}
	for _, w := range ws {
		switch w.WorkspaceRoot {
		case sub:
			e := w.Evidence
			if !w.Importable || w.ID != sub || w.Title != "web" || w.Repo != "" || e == nil || e.Remote != "github.com/acme/app" || e.Subpath != "web" {
				t.Fatalf("repo workspace %+v, evidence %+v", w, e)
			}
		case plain:
			if w.Importable || w.Repo != "" || w.Evidence == nil || w.Evidence.CheckoutKey != "" {
				t.Fatalf("plain directory %+v, evidence %+v", w, w.Evidence)
			}
		default:
			t.Fatalf("unexpected %+v", w)
		}
	}
}

func TestFeaturesFromModelCatalog(t *testing.T) {
	r := newRig(t)
	c, err := r.adapter.Features(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Valid(); err != nil {
		t.Fatal(err)
	}
	if err := c.DeclaresOperations(); err != nil {
		t.Fatal(err)
	}
	if len(c.Models) != 1 || c.Models[0].ID != "gpt-test" || len(c.Models[0].Options) != 1 || c.Models[0].Options[0].Default == nil || c.Models[0].Options[0].Default.Str != "low" {
		t.Fatalf("models %+v", c.Models)
	}
	if labels := c.Models[0].Options[0].ValueLabels; labels["low"] != "Low" || labels["high"] != "High" {
		t.Fatalf("reasoning effort labels %+v", labels)
	}
	if !c.Operations[execution.OpImages].Available || c.Operations[execution.OpRespond].Available || len(c.Options) != 0 || len(c.Permissions) != 3 {
		t.Fatalf("%+v", c)
	}
	if err := c.ValidateConfig(execution.Config{}); err != nil {
		t.Fatal("empty config uses Codex defaults", err)
	}
}

func TestEventsPushStatusChangesWithoutSubscribing(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var cps checkpoints
	o, err := r.adapter.Launch(ctx, intent("/w/app"), execution.Checkpoint{}, cps.save)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan execution.Observation, 8)
	go func() {
		_ = r.adapter.Events(ctx, func(_ context.Context, o execution.Observation) error { got <- o; return nil })
	}()
	deadline := time.After(5 * time.Second)
	for i := 0; ; i++ {
		if i%20 == 0 {
			r.srv.Finish(o.Session.ExternalSessionID, "completed", "pushed")
		}
		select {
		case p := <-got:
			if p.State == "done" && p.Reply != nil && p.Reply.Text == "pushed" {
				return
			}
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			t.Fatal("no pushed observation")
		}
	}
}

// The daemon is started when it is not running, at most once per backoff
// window; a missing CLI and a signed-out login are reported as such.
func TestClientStartsTheDaemonAndReportsProblems(t *testing.T) {
	r := newRig(t)
	ctx := t.Context()
	r.srv.Stop()
	h := r.adapter.Health(ctx)
	if !h.Available || r.daemon.Starts() != 1 || h.Identity != r.srv.CodexHome {
		t.Fatalf("health %+v starts %d", h, r.daemon.Starts())
	}
	r.client.Close()
	r.srv.Stop()
	r.daemon.Missing = false
	stopped := codex.NewClient(failingStart{r.daemon}, "test")
	if _, err := stopped.Probe(ctx); err == nil {
		t.Fatal("probe with no daemon")
	}
	if _, err := stopped.Probe(ctx); err == nil || !strings.Contains(codex.Problem(err), "retries starting it shortly") {
		t.Fatal("second start inside the backoff window", err)
	}

	missing := codex.NewClient(&codextest.Daemon{Srv: codextest.New(t), T: t, Missing: true}, "test")
	_, err := missing.Probe(ctx)
	if !errors.Is(err, codex.ErrNotInstalled) || !strings.Contains(codex.Problem(err), "not found") {
		t.Fatal(err)
	}

	so := newRig(t)
	so.srv.SetSignedOut(true)
	p, err := so.client.Probe(ctx)
	if err != nil || p.SignedIn() {
		t.Fatal(p, err)
	}
	if h := so.adapter.Health(ctx); h.Available || !strings.Contains(h.Reason, "codex login") {
		t.Fatal(h)
	}
}

// failingStart reports the daemon down and fails every start.
type failingStart struct{ d *codextest.Daemon }

func (f failingStart) Version(ctx context.Context) (codex.DaemonInfo, error) { return f.d.Version(ctx) }
func (f failingStart) Start(context.Context) (codex.DaemonInfo, error) {
	return codex.DaemonInfo{}, errors.New("exit status 1")
}

func TestUnsupportedMethodNamesTheUpdate(t *testing.T) {
	r := newRig(t)
	r.srv.Unsupported("model/list")
	_, err := r.adapter.Features(t.Context())
	if err == nil || !strings.Contains(codex.Problem(err), "Update Codex") {
		t.Fatal(err)
	}
}
