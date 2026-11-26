package codexsetup_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/flexdinesh/stage/server/internal/codex"
	"github.com/flexdinesh/stage/server/internal/codex/codextest"
	"github.com/flexdinesh/stage/server/internal/codexsetup"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
)

type rig struct {
	store  *db.Store
	ports  machine.StorePorts
	srv    *codextest.Server
	daemon *codextest.Daemon
	local  *machine.Local
	svc    *codexsetup.Service
	conn   *codexsetup.Connector
}

func newRig(t *testing.T) *rig {
	t.Helper()
	var temp dbtest.DB
	store := db.NewStore(temp.Reset(t))
	srv := codextest.New(t)
	r := &rig{store: store, ports: machine.NewStorePorts(store.OnMachine(db.LocalMachine), harness.MustRegistry(codex.Descriptor)), srv: srv, daemon: &codextest.Daemon{Srv: srv, T: t}}
	r.local = machine.NewLocal(db.LocalMachine, harness.Profile{Kind: harness.ProfileDesktop, LocalProcesses: true}, nil)
	r.start(t)
	return r
}

// start composes the setup as serve does at boot.
func (r *rig) start(t *testing.T) {
	t.Helper()
	svc, err := codexsetup.New(t.Context(), codexsetup.Options{
		Ports:   codexsetup.Ports{Settings: r.ports, Identity: r.ports, Install: r.local.Install},
		Version: "test", Daemon: func(string) codex.Daemon { return r.daemon },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	r.svc, r.conn = svc, codexsetup.NewConnector(svc)
}

func (r *rig) status(t *testing.T) harness.Status {
	t.Helper()
	st, err := r.conn.Status(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func details(t *testing.T, st harness.Status) codexsetup.Details {
	t.Helper()
	d, ok := st.Details.(codexsetup.Details)
	if !ok {
		t.Fatalf("details %T", st.Details)
	}
	return d
}

// Connect creates the machine's Codex connection (identity: the Codex
// home, label: the account), installs the adapter and survives a restart;
// Disconnect also survives one.
func TestConnectCreatesConnectionAndSurvivesRestart(t *testing.T) {
	r := newRig(t)
	ctx := t.Context()
	st := r.status(t)
	if st.State != codexsetup.StateNotConnected || st.Configured || st.ConnectionID != 0 || details(t, st).Daemon != "running" || r.daemon.Starts() != 0 {
		t.Fatalf("before connect %+v starts %d", st, r.daemon.Starts())
	}
	res, err := r.conn.Run(ctx, codex.ActionConnect, json.RawMessage(`{}`))
	if err != nil || res.Replaced != 0 {
		t.Fatal(res, err)
	}
	id := r.store.ActiveID(db.LocalMachine, codex.Kind)
	c, err := r.store.GetHarnessConnection(ctx, id)
	if err != nil || id == 0 || c.Label != "Codex · u@example.com" {
		t.Fatalf("connection %d %+v %v", id, c, err)
	}
	if _, ok := r.local.Adapter(id); !ok {
		t.Fatal("adapter not installed")
	}
	st = r.status(t)
	d := details(t, st)
	if st.State != codexsetup.StateConnected || !st.Connected || !st.Configured || st.ConnectionID != id || d.CodexHome != r.srv.CodexHome || d.Account == nil || d.Account.Type != "chatgpt" || d.CLIVersion != "0.171.0" || d.Fix != "" {
		t.Fatalf("connected %+v %+v", st, d)
	}

	// Restart: the saved connection is installed again without a probe.
	r.local = machine.NewLocal(db.LocalMachine, harness.Profile{Kind: harness.ProfileDesktop, LocalProcesses: true}, nil)
	r.start(t)
	if _, ok := r.local.Adapter(id); !ok || !r.svc.Summary().Configured {
		t.Fatal("restart lost the connection")
	}

	if err := r.conn.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.local.Adapter(id); ok {
		t.Fatal("disconnect left the adapter")
	}
	r.local = machine.NewLocal(db.LocalMachine, harness.Profile{Kind: harness.ProfileDesktop, LocalProcesses: true}, nil)
	r.start(t)
	if _, ok := r.local.Adapter(id); ok || r.svc.Summary().Configured || r.svc.Summary().ConnectionID != id {
		t.Fatal("disconnect did not survive a restart")
	}
	// Reconnecting keeps the same connection (same Codex home).
	if res, err = r.conn.Run(ctx, codex.ActionConnect, nil); err != nil || res.Replaced != 0 || r.store.ActiveID(db.LocalMachine, codex.Kind) != id {
		t.Fatal(res, err)
	}
}

func TestConnectRefusesSignedOutAndMissingCLI(t *testing.T) {
	r := newRig(t)
	ctx := t.Context()
	r.srv.SetSignedOut(true)
	_, err := r.conn.Run(ctx, codex.ActionConnect, nil)
	var conflict *db.ConflictError
	if !errors.As(err, &conflict) || conflict.Code != codexsetup.CodeSignedOut {
		t.Fatal(err)
	}
	if id := r.store.ActiveID(db.LocalMachine, codex.Kind); id != 0 {
		t.Fatal("signed-out connect created a connection", id)
	}
	st, _ := r.conn.Status(ctx, false)
	if st.State != codexsetup.StateSignedOut || details(t, st).Fix != "codex login" {
		t.Fatalf("%+v", st)
	}

	r.daemon.Missing = true
	_, err = r.conn.Run(ctx, codex.ActionConnect, nil)
	if !errors.As(err, &conflict) || conflict.Code != codexsetup.CodeNotInstalled {
		t.Fatal(err)
	}
	if st := r.status(t); st.State != codexsetup.StateNotInstalled || st.Reason == "" {
		t.Fatalf("%+v", st)
	}
	if _, err := r.conn.Run(ctx, "pair", nil); err == nil {
		t.Fatal("unknown action ran")
	}
}

// A stopped daemon on a connected Codex: the status names the fix; the
// next probe starts it again.
func TestStatusReportsAStoppedDaemonWithItsFix(t *testing.T) {
	r := newRig(t)
	ctx := t.Context()
	if _, err := r.conn.Run(ctx, codex.ActionConnect, nil); err != nil {
		t.Fatal(err)
	}
	r.svc.Close()
	r.srv.Stop()
	broken := failing{r.daemon}
	r.svc.Close()
	// Swap in a daemon that can't start, through the binary setting.
	svc, err := codexsetup.New(ctx, codexsetup.Options{
		Ports:   codexsetup.Ports{Settings: r.ports, Identity: r.ports, Install: r.local.Install},
		Version: "test", Daemon: func(string) codex.Daemon { return broken },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	st, _ := codexsetup.NewConnector(svc).Status(ctx, true)
	if st.State != codexsetup.StateUnreachable || st.Connected || details(t, st).Fix != "codex app-server daemon start" || details(t, st).Daemon != "stopped" {
		t.Fatalf("%+v %+v", st, st.Details)
	}
}

type failing struct{ d *codextest.Daemon }

func (f failing) Version(ctx context.Context) (codex.DaemonInfo, error) { return f.d.Version(ctx) }
func (f failing) Start(context.Context) (codex.DaemonInfo, error) {
	return codex.DaemonInfo{}, errors.New("exit status 1")
}

func TestBinarySettingPersistsAndSwitchesTheAdapter(t *testing.T) {
	r := newRig(t)
	ctx := t.Context()
	if _, err := r.conn.Run(ctx, codex.ActionConnect, nil); err != nil {
		t.Fatal(err)
	}
	id := r.store.ActiveID(db.LocalMachine, codex.Kind)
	before, _ := r.local.Adapter(id)
	if _, err := r.conn.Configure(ctx, json.RawMessage(`{"binary":" /opt/codex/bin/codex "}`)); err != nil {
		t.Fatal(err)
	}
	after, _ := r.local.Adapter(id)
	if after == before {
		t.Fatal("adapter kept the old CLI")
	}
	ref := execution.SessionRef{ConnectionID: id, ExternalSessionID: "019a0000-0000-7000-8000-000000000001"}
	r.srv.AddThread(codextest.Thread{ID: ref.ExternalSessionID, Cwd: "/w/app"})
	if o, err := after.Observe(ctx, ref); err != nil || o.Links.Command != "cd /w/app && /opt/codex/bin/codex resume "+ref.ExternalSessionID {
		t.Fatal(o.Links, err)
	}
	saved, err := r.store.CodexSettings(ctx, db.LocalMachine)
	if err != nil || saved.Binary != "/opt/codex/bin/codex" || !saved.Connected {
		t.Fatal(saved, err)
	}
	if st := r.status(t); st.Fields[0].Value != "/opt/codex/bin/codex" || st.Fields[0].Source != "saved" {
		t.Fatalf("%+v", st.Fields)
	}
	if _, err := r.conn.Configure(ctx, json.RawMessage(`{"binary":null}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.conn.Configure(ctx, json.RawMessage(`{"other":1}`)); err == nil {
		t.Fatal("unknown setting accepted")
	}
}
