package workersetup_test

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest"
	"github.com/flexdinesh/stage/server/internal/claudecodesetup"
	"github.com/flexdinesh/stage/server/internal/codex"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/remote"
	coordinator "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/worker"
)

// claudeOptions run Claude Code's setup against the fake claude CLI.
func claudeOptions(f *claudetest.Fake) *claudecodesetup.Options {
	return &claudecodesetup.Options{Home: f.Home, Environ: f.Environ(), CheckTimeout: 5 * time.Second, LookPath: func(p string) (string, error) {
		if p == f.Bin {
			return p, nil
		}
		return "", errors.New("not found")
	}}
}

// localHarnesses is a foreground worker run with --local-harnesses.
var localHarnesses = harness.Profile{Kind: harness.ProfileServer, LocalProcesses: true}

// A worker that allows local harnesses sets up Claude Code end to end:
// connect records the machine's connection and its non-secret settings on
// the main server (nothing in the worker's credentials file) and installs
// the adapter there; a task launched through the coordinator runs over the
// worker channel and its stored observation carries the resume command
// reported by Claude. Disconnect is recorded on the main server too.
func TestRemoteClaudeConnectsAndRunsATaskOverTheChannel(t *testing.T) {
	f := claudetest.New(t)
	r := newRigWith(t, map[string]string{}, rigOptions{profile: localHarnesses, claude: claudeOptions(f)})
	ctx := t.Context()
	m := r.machine()
	if a := m.Allows(claudecode.Descriptor); !a.Available {
		t.Fatalf("allows %+v", a)
	}
	conns, err := r.set.Connectors(registry)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := conns.Get(r.id, claudecode.Kind); !ok {
		t.Fatal("the connectors index left out Claude Code")
	}
	c := r.connector(claudecode.Kind)
	if _, err := c.Configure(ctx, json.RawMessage(`{"binary":"`+f.Bin+`"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(ctx, claudecode.ActionConnect, nil); err != nil {
		t.Fatal(err)
	}
	id := r.store.ActiveID(r.id, claudecode.Kind)
	if id == 0 || r.store.ConnectionMachine(id) != r.id {
		t.Fatalf("connect created connection %d on machine %d", id, r.store.ConnectionMachine(id))
	}
	if local := r.store.ActiveID(db.LocalMachine, claudecode.Kind); local != 0 {
		t.Fatalf("connected the local machine: %d", local)
	}
	saved := savedSettings[claudecodesetup.SetupSettings](r, r.id, claudecode.Kind)
	if saved.Binary != f.Bin || saved.ConfigDir != f.ConfigDir() || saved.Disconnected {
		t.Fatalf("main server record %+v", saved)
	}
	if creds, err := os.ReadFile(worker.NewCredentialStore(r.cfgPath).Path()); err == nil && strings.Contains(string(creds), claudecode.Kind) {
		t.Fatalf("Claude Code wrote to the worker's credentials: %q", creds)
	}
	r.wait("adapter on the new connection", func() bool {
		_, ok := m.Adapter(id)
		return ok && m.Online()
	})
	if h := m.Adapters()[id].Health(ctx); !h.Available {
		t.Fatalf("adapter health %+v", h)
	}

	// A task on the remote connection, through the coordinator.
	project, err := r.store.InsertProject(ctx, db.Project{Title: "Repo", Bindings: []db.ProjectBinding{{ConnectionID: id, ExternalProjectID: f.Workspace, Workspace: f.Workspace}}})
	if err != nil {
		t.Fatal(err)
	}
	task, err := r.store.CreateTask(ctx, db.NewTask{ProjectID: project.ID, ConnectionID: id, Title: "Work", Prompt: "Do it", ExecutionConfig: &execution.Config{Model: "haiku"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := coordinator.NewForMachines(r.store, registry, r.set, quiet())
	got, err := svc.StartTask(ctx, task.ID)
	if err != nil || got.State != db.StateRunning || got.Session == nil {
		t.Fatalf("start %+v %v", got, err)
	}
	if n := len(f.Launches()); n != 1 {
		t.Fatalf("launches %d", n)
	}
	o, err := r.store.Observation(ctx, *got.Session)
	if err != nil || !strings.HasPrefix(o.Links.Command, "claude attach ") {
		t.Fatalf("stored observation links %+v %v", o.Links, err)
	}
	if err := c.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if saved := savedSettings[claudecodesetup.SetupSettings](r, r.id, claudecode.Kind); !saved.Disconnected {
		t.Fatalf("disconnect not recorded: %+v", saved)
	}
	// The worker removed its adapter: the main server's proxy answers
	// unavailable.
	r.wait("adapter removed", func() bool { return !m.Adapters()[id].Health(ctx).Available })
}

// A foreground worker without --local-harnesses never sets up Claude
// Code: its machine refuses the kind with the fix, and the connectors
// index leaves it out.
func TestRemoteClaudeNeedsAWorkerThatAllowsLocalHarnesses(t *testing.T) {
	r := newRig(t, map[string]string{})
	a := r.machine().Allows(claudecode.Descriptor)
	if a.Available || a.Reason != "Claude Code runs on this machine only when its Stage worker allows local harnesses: use the worker service, or run stage worker run --local-harnesses." {
		t.Fatalf("allows %+v", a)
	}
	conns, err := r.set.Connectors(registry)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := conns.Get(r.id, claudecode.Kind); ok {
		t.Fatal("the connectors index kept Claude Code")
	}
	if _, ok := conns.Get(r.id, t3.Kind); !ok {
		t.Fatal("the connectors index left out T3")
	}
}

// A main server without Claude Code omits it from the advertised kinds.
// The worker still composes T3 and OpenCode, and leaves
// Claude Code out (the main server reports the machine needs an update).
func TestRemoteClaudeSkippedWhenServerOmitsKind(t *testing.T) {
	f := claudetest.New(t)
	r := newRigWith(t, map[string]string{}, rigOptions{profile: localHarnesses, claude: claudeOptions(f), omittedKinds: []string{claudecode.Kind, codex.Kind}})
	ctx := t.Context()
	if _, err := r.connector(t3.Kind).Status(ctx, false); err != nil {
		t.Fatalf("T3 setup: %v", err)
	}
	if _, err := r.connector(claudecode.Kind).Status(ctx, false); !isUnavailable(err, remote.CodeMachineNeedsUpdate) {
		t.Fatalf("Claude Code status: %v", err)
	}
	notSetUp := "Claude Code can't run on this machine: its Stage worker doesn't set it up. Update Stage on that machine."
	r.wait("Claude Code unavailable", func() bool { return r.machine().Allows(claudecode.Descriptor).Reason == notSetUp })
}
