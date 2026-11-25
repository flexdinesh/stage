package claudecodesetup_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest/fakestate"
	"github.com/flexdinesh/stage/server/internal/claudecodesetup"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
)

var testDB dbtest.DB

type env struct {
	f     *claudetest.Fake
	store *db.Store
	ports machine.StorePorts
	local *machine.Local
	opts  claudecodesetup.Options
}

func newEnv(t *testing.T) *env {
	t.Helper()
	f := claudetest.New(t)
	store := db.NewStore(testDB.Reset(t))
	reg := harness.MustRegistry(claudecode.Descriptor)
	e := &env{
		f: f, store: store, ports: machine.NewStorePorts(store.OnMachine(db.LocalMachine), reg),
		local: machine.NewLocal(db.LocalMachine, harness.Profile{Kind: harness.ProfileDesktop, LocalProcesses: true}, nil),
		// The fake is found by its path; nothing on PATH is named claude.
		opts: claudecodesetup.Options{Home: f.Home, Environ: f.Environ(), LookPath: func(p string) (string, error) {
			if p == f.Bin {
				return p, nil
			}
			return "", errors.New("not found")
		}, CheckTimeout: 2 * time.Second},
	}
	if err := e.ports.SaveClaudeSettings(t.Context(), claudeSettings(f.Bin)); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) service(t *testing.T) *claudecodesetup.Service {
	t.Helper()
	s, err := claudecodesetup.New(t.Context(), claudecodesetup.Ports{Settings: e.ports, Identity: e.ports, Install: e.local.Install}, e.opts)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Connect checks the command and its login, creates the machine's
// connection named by its config directory (never the account), installs
// the adapter, and survives a restart; disconnect survives one too, and a
// reconnect resumes the same connection.
func TestConnectDisconnectAndRestart(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	s := e.service(t)
	if st := s.Status(ctx, true); st.State != claudecodesetup.StateDisconnected || st.Configured || st.Reason != "" {
		t.Fatalf("before connect %+v", st)
	}
	if _, err := s.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	id, _ := e.ports.Active(ctx, claudecode.Kind)
	c, err := e.ports.GetHarnessConnection(ctx, id)
	if err != nil || id == 0 || c.Label != "Claude Code (~/.claude)" {
		t.Fatalf("connection %d %+v %v", id, c, err)
	}
	a, ok := e.local.Adapter(id)
	if !ok || !a.Health(ctx).Available {
		t.Fatal("adapter not installed or unhealthy")
	}
	if st := s.Status(ctx, true); st.State != claudecodesetup.StateConnected || !st.Connected || st.ConnectionID != id {
		t.Fatalf("connected %+v", st)
	}
	if saved, _ := e.ports.ClaudeSettings(ctx); saved.ConfigDir != e.f.ConfigDir() || saved.Disconnected {
		t.Fatalf("settings %+v", saved)
	}

	// A restart installs the connected adapter without probing.
	e.local = machine.NewLocal(db.LocalMachine, harness.Profile{Kind: harness.ProfileDesktop, LocalProcesses: true}, nil)
	calls := len(e.f.Read().Calls)
	s = e.service(t)
	if _, ok := e.local.Adapter(id); !ok || len(e.f.Read().Calls) != calls || !s.Summary().Connected {
		t.Fatal("restart did not reinstall the adapter, or probed")
	}

	if err := s.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.local.Adapter(id); ok || s.Summary().Configured {
		t.Fatal("disconnect left the adapter")
	}
	e.local = machine.NewLocal(db.LocalMachine, harness.Profile{Kind: harness.ProfileDesktop, LocalProcesses: true}, nil)
	s = e.service(t)
	if _, ok := e.local.Adapter(id); ok {
		t.Fatal("a restart reconnected a disconnected Claude Code")
	}
	if _, err := s.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if again, _ := e.ports.Active(ctx, claudecode.Kind); again != id {
		t.Fatalf("reconnect moved to %d", again)
	}
}

// Each failed check says what to do; none creates a connection.
func TestConnectFailures(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	var conflict *db.ConflictError

	if err := e.ports.SaveClaudeSettings(ctx, claudeSettings(filepath.Join(e.f.Home, "missing"))); err != nil {
		t.Fatal(err)
	}
	s := e.service(t)
	if _, err := s.Connect(ctx); !errors.As(err, &conflict) || conflict.Code != claudecodesetup.CodeNotFound || !strings.Contains(conflict.Msg, "Check the Claude command") {
		t.Fatalf("missing command: %v", err)
	}
	if st := s.Status(ctx, false); st.State != claudecodesetup.StateNotFound || st.Fields[0].Value == "" {
		t.Fatalf("status %+v", st)
	}
	// Blank: PATH, then the tool dirs; nothing there.
	if err := s.Configure(ctx, []byte(`{"binary":null}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Connect(ctx); !errors.As(err, &conflict) || !strings.Contains(conflict.Msg, "Install Claude Code") {
		t.Fatalf("default lookup: %v", err)
	}
	if err := s.Configure(ctx, []byte(`{"binary":"`+e.f.Bin+`"}`)); err != nil {
		t.Fatal(err)
	}

	e.f.Update(func(st *fakestate.State) { st.LoggedIn = false })
	if _, err := s.Connect(ctx); !errors.As(err, &conflict) || conflict.Code != claudecodesetup.CodeLoggedOut || !strings.Contains(conflict.Msg, "claude auth login") {
		t.Fatalf("logged out: %v", err)
	}
	e.f.Update(func(st *fakestate.State) { st.LoggedIn, st.HangVersion = true, true })
	if _, err := s.Connect(ctx); !errors.As(err, &conflict) || conflict.Code != claudecodesetup.CodeUnresponsive || !strings.Contains(conflict.Msg, "didn't answer") {
		t.Fatalf("hung: %v", err)
	}
	if id, _ := e.ports.Active(ctx, claudecode.Kind); id != 0 {
		t.Fatalf("a failed connect created connection %d", id)
	}
	if err := s.Configure(ctx, []byte(`{"other":1}`)); err == nil {
		t.Fatal("unknown setting accepted")
	}
}

// A connected Claude whose command went missing recovers when the Claude
// command is fixed: the adapter comes back and the status is connected
// again, without another Connect.
func TestConfigureRecoversAMissingCommand(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	s := e.service(t)
	if _, err := s.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	id := s.Summary().ConnectionID
	if err := s.Configure(ctx, []byte(`{"binary":"`+filepath.Join(e.f.Home, "missing")+`"}`)); err != nil {
		t.Fatal(err)
	}
	if st := s.Status(ctx, true); st.State != claudecodesetup.StateNotFound || st.Connected || !st.Configured {
		t.Fatalf("missing command: %+v", st)
	}
	if _, ok := e.local.Adapter(id); ok {
		t.Fatal("adapter kept for a missing command")
	}
	if err := s.Configure(ctx, []byte(`{"binary":"`+e.f.Bin+`"}`)); err != nil {
		t.Fatal(err)
	}
	if st := s.Status(ctx, true); st.State != claudecodesetup.StateConnected || !st.Connected {
		t.Fatalf("fixed command: %+v", st)
	}
	if _, ok := e.local.Adapter(id); !ok {
		t.Fatal("no adapter after the command was fixed")
	}
}

// The connector speaks the generic setup contract.
func TestConnector(t *testing.T) {
	e := newEnv(t)
	c := claudecodesetup.NewConnector(e.service(t))
	if _, err := c.Run(t.Context(), "pair", nil); err == nil {
		t.Fatal("unknown action ran")
	}
	if _, err := c.Run(t.Context(), claudecode.ActionConnect, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if !c.Summary(t.Context()).Connected {
		t.Fatal("not connected")
	}
}

func claudeSettings(bin string) config.ClaudeSettings { return config.ClaudeSettings{Binary: bin} }

func eventsOf(t *testing.T, st harness.Status) *claudecode.EventsStatus {
	t.Helper()
	d, ok := st.Details.(claudecodesetup.Details)
	if !ok {
		t.Fatalf("details %T", st.Details)
	}
	return d.Events
}

// Push starts with the first adapter: the connected status reports push
// (no hook yet, then the last one), the adapter launches with the hook
// settings, receives hooks and answers approvals (respond); disconnect
// ends its subscription and the status drops events.
func TestHookEventsStatus(t *testing.T) {
	e := newEnv(t)
	e.opts.StateDir = filepath.Join(t.TempDir(), "claude")
	ctx := t.Context()
	s := e.service(t)
	if ev := eventsOf(t, s.Status(ctx, true)); ev != nil {
		t.Fatalf("events before connect %+v", ev)
	}
	if _, err := s.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	ev := eventsOf(t, s.Status(ctx, false))
	if ev == nil || ev.Mode != claudecode.ModePush || ev.Reason != "" || ev.LastEventAt != nil {
		t.Fatalf("connected events %+v", ev)
	}
	id, _ := e.ports.Active(ctx, claudecode.Kind)
	installed, _ := e.local.Adapter(id)
	a, ok := installed.(*claudecode.Adapter)
	if !ok {
		t.Fatalf("adapter %T", installed)
	}
	if caps, _ := a.Features(ctx); !caps.Operations[execution.OpRespond].Available {
		t.Fatalf("respond %+v", caps.Operations[execution.OpRespond])
	}
	done := make(chan error, 1)
	go func() {
		done <- a.Events(ctx, func(context.Context, execution.Observation) error { return nil })
	}()
	o, err := a.Launch(ctx, execution.Intent{RequestID: "r", ConnectionID: id, Project: e.f.Workspace, Directory: e.f.Workspace, Prompt: "x", Workspace: execution.Workspace{Kind: "root"}}, execution.Checkpoint{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if args := e.f.Launches()[0].Args; !slices.Contains(args, filepath.Join(e.opts.StateDir, claudecode.HooksFile)) {
		t.Fatalf("launch args %q", args)
	}
	if code := e.f.Hook(o.Session.ExternalSessionID, "Stop", nil); code != 204 {
		t.Fatal(code)
	}
	if ev := eventsOf(t, s.Status(ctx, false)); ev == nil || ev.LastEventAt == nil {
		t.Fatalf("after a hook %+v", ev)
	}
	if err := s.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the removed adapter still receives hooks")
	}
	if ev := eventsOf(t, s.Status(ctx, false)); ev != nil {
		t.Fatalf("events after disconnect %+v", ev)
	}
}

// Without a folder for the hook settings Stage follows sessions by polling
// and says why; approvals are answered in Claude Code.
func TestHookEventsPollingWithoutStateDir(t *testing.T) {
	e := newEnv(t)
	s := e.service(t)
	if _, err := s.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	ev := eventsOf(t, s.Status(t.Context(), false))
	if ev == nil || ev.Mode != claudecode.ModePolling || ev.Reason != "Stage has no folder for its Claude Code hook settings. It checks Claude Code every few seconds instead." {
		t.Fatalf("events %+v", ev)
	}
	id, _ := e.ports.Active(t.Context(), claudecode.Kind)
	a, _ := e.local.Adapter(id)
	if caps, _ := a.Features(t.Context()); caps.Operations[execution.OpRespond].Available {
		t.Fatal("respond without hooks")
	}
}
