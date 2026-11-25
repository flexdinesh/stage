package claudecodesetup_test

import (
	"errors"
	"path/filepath"
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
