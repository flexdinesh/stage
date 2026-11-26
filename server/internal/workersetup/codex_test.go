package workersetup_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest"
	"github.com/flexdinesh/stage/server/internal/codex"
	"github.com/flexdinesh/stage/server/internal/codex/codextest"
	"github.com/flexdinesh/stage/server/internal/codexsetup"
	"github.com/flexdinesh/stage/server/internal/coordinator"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/remote"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/worker"
)

// codexOptions run Codex's setup against the fake daemon.
func codexOptions(t *testing.T, dir string) (*codextest.Server, *codexsetup.Options) {
	t.Helper()
	srv := codextest.New(t)
	srv.Trust(dir, "trusted")
	d := &codextest.Daemon{Srv: srv, T: t}
	return srv, &codexsetup.Options{Version: "test", Daemon: func(string) codex.Daemon { return d }}
}

// A worker that allows local harnesses sets up Codex end to end: connect
// records the machine's connection and its non-secret settings on the main
// server (nothing in the worker's credentials file) and installs the
// adapter there; a task launched through the coordinator runs over the
// worker channel and its stored observation carries the resume command
// with the configured CLI. Disconnect is recorded on the main server and
// leaves the daemon running.
func TestRemoteCodexConnectsAndRunsATaskOverTheChannel(t *testing.T) {
	codexDir := t.TempDir()
	srv, opts := codexOptions(t, codexDir)
	r := newRigWith(t, map[string]string{}, rigOptions{profile: localHarnesses, codex: opts})
	ctx := t.Context()
	m := r.machine()
	if a := m.Allows(codex.Descriptor); !a.Available {
		t.Fatalf("allows %+v", a)
	}
	conns, err := r.set.Connectors(registry)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := conns.Get(r.id, codex.Kind); !ok {
		t.Fatal("the connectors index left out Codex")
	}
	const bin = "/opt/codex/bin/codex"
	c := r.connector(codex.Kind)
	if _, err := c.Configure(ctx, json.RawMessage(`{"binary":"`+bin+`"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(ctx, codex.ActionConnect, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	id := r.store.ActiveID(r.id, codex.Kind)
	if id == 0 || r.store.ConnectionMachine(id) != r.id {
		t.Fatalf("connect created connection %d on machine %d", id, r.store.ConnectionMachine(id))
	}
	if hc, err := r.store.GetHarnessConnection(ctx, id); err != nil || hc.Label != "Codex · u@example.com" {
		t.Fatalf("connection %+v %v", hc, err)
	}
	if local := r.store.ActiveID(db.LocalMachine, codex.Kind); local != 0 {
		t.Fatalf("connected the local machine: %d", local)
	}
	saved := savedSettings[codexsetup.SetupSettings](r, r.id, codex.Kind)
	if saved != (codexsetup.SetupSettings{Binary: bin, Connected: true}) {
		t.Fatalf("main server record %+v", saved)
	}
	if local := savedSettings[codexsetup.SetupSettings](r, db.LocalMachine, codex.Kind); local != (codexsetup.SetupSettings{}) {
		t.Fatalf("set up the local machine's Codex: %+v", local)
	}
	if creds, err := os.ReadFile(worker.NewCredentialStore(r.cfgPath).Path()); err == nil && strings.Contains(string(creds), codex.Kind) {
		t.Fatalf("Codex wrote to the worker's credentials: %q", creds)
	}
	r.wait("adapter on the new connection", func() bool {
		_, ok := m.Adapter(id)
		return ok && m.Online()
	})
	if h := m.Adapters()[id].Health(ctx); !h.Available {
		t.Fatalf("adapter health %+v", h)
	}

	// A task on the remote connection, through the coordinator.
	project, err := r.store.InsertProject(ctx, db.Project{Title: "App", Bindings: []db.ProjectBinding{{ConnectionID: id, ExternalProjectID: codexDir, Workspace: codexDir}}})
	if err != nil {
		t.Fatal(err)
	}
	task, err := r.store.CreateTask(ctx, db.NewTask{ProjectID: project.ID, ConnectionID: id, Title: "Fix", Prompt: "Fix it", ExecutionConfig: &execution.Config{Model: "gpt-test", Permission: "auto"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := coordinator.NewForMachines(r.store, registry, r.set, quiet())
	got, err := svc.StartTask(ctx, task.ID)
	if err != nil || got.State != db.StateRunning || got.Session == nil {
		t.Fatalf("start %+v %v", got, err)
	}
	thread := got.Session.ExternalSessionID
	srv.Finish(thread, "completed", "Fixed.")
	if err := svc.PollTask(ctx, got); err != nil {
		t.Fatal(err)
	}
	if got, _ = r.store.GetTask(ctx, task.ID); got.State != db.StateDone {
		t.Fatalf("state %s", got.State)
	}
	o, err := r.store.Observation(ctx, *got.Session)
	if err != nil || o.Reply == nil || o.Reply.Text != "Fixed." || o.Links.Command != "cd "+codexDir+" && "+bin+" resume "+thread {
		t.Fatalf("stored observation %+v %v", o, err)
	}
	if err := c.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if saved := savedSettings[codexsetup.SetupSettings](r, r.id, codex.Kind); saved.Connected {
		t.Fatalf("disconnect not recorded: %+v", saved)
	}
	// The worker removed its adapter: the main server's proxy answers
	// unavailable. The daemon keeps running (Stage never stops it).
	r.wait("adapter removed", func() bool { return !m.Adapters()[id].Health(ctx).Available })
	if !srv.Running() {
		t.Fatal("disconnect stopped the Codex daemon")
	}
}

// A foreground worker without --local-harnesses never sets up Codex: its
// machine refuses the kind with the fix, and the connectors index leaves
// it out.
func TestRemoteCodexNeedsAWorkerThatAllowsLocalHarnesses(t *testing.T) {
	_, opts := codexOptions(t, t.TempDir())
	r := newRigWith(t, map[string]string{}, rigOptions{profile: harness.Profile{Kind: harness.ProfileServer}, codex: opts})
	a := r.machine().Allows(codex.Descriptor)
	if a.Available || a.Reason != "Codex runs on this machine only when its Stage worker allows local harnesses: use the worker service, or run stage worker run --local-harnesses." {
		t.Fatalf("allows %+v", a)
	}
	conns, err := r.set.Connectors(registry)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := conns.Get(r.id, codex.Kind); ok {
		t.Fatal("the connectors index kept Codex")
	}
	if _, ok := conns.Get(r.id, t3.Kind); !ok {
		t.Fatal("the connectors index left out T3")
	}
}

// A main server without Codex omits it from the advertised kinds:
// the worker still composes the other harnesses and leaves Codex out; the
// main server reports the machine needs an update for Codex, and Codex
// reads unavailable, never as a connect that fails.
func TestRemoteCodexSkippedWhenServerOmitsKind(t *testing.T) {
	_, opts := codexOptions(t, t.TempDir())
	r := newRigWith(t, map[string]string{}, rigOptions{profile: localHarnesses, claude: claudeOptions(claudetest.New(t)), codex: opts, omittedKinds: []string{codex.Kind}})
	ctx := t.Context()
	if _, err := r.connector(claudecode.Kind).Status(ctx, false); err != nil {
		t.Fatalf("Claude Code setup: %v", err)
	}
	if _, err := r.connector(codex.Kind).Status(ctx, false); !isUnavailable(err, remote.CodeMachineNeedsUpdate) {
		t.Fatalf("Codex status: %v", err)
	}
	notSetUp := "Codex can't run on this machine: its Stage worker doesn't set it up. Update Stage on that machine."
	r.wait("Codex unavailable", func() bool { return r.machine().Allows(codex.Descriptor).Reason == notSetUp })
}
