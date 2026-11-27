package workersetup_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/claudecodesetup"
	"github.com/flexdinesh/stage/server/internal/codex"
	"github.com/flexdinesh/stage/server/internal/codexsetup"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/harnesses"
	"github.com/flexdinesh/stage/server/internal/listen"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/opencode"
	"github.com/flexdinesh/stage/server/internal/opencode/opencodetest"
	"github.com/flexdinesh/stage/server/internal/opencodesetup"
	"github.com/flexdinesh/stage/server/internal/remote"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/t3setup"
	"github.com/flexdinesh/stage/server/internal/worker"
	"github.com/flexdinesh/stage/server/internal/workerchan"
	"github.com/flexdinesh/stage/server/internal/workersetup"
)

// rig is a main server (store, hub, hosts, setup server) and one remote
// machine's real worker loop whose host composes its connection setup.
type rig struct {
	t       *testing.T
	store   *db.Store
	hub     *workerchan.Hub
	set     *machine.Set
	hosts   *remote.Hosts
	id      int64
	cfgPath string
	srv     *httptest.Server
	listen  []listen.Addr
	env     map[string]string
	fake    *t3.Fake
	opts    rigOptions
	stop    context.CancelFunc
	done    chan error
	changes int
}

// rigOptions shape the worker: its profile (default: a foreground worker
// without local harnesses), Claude Code's machine-local environment,
// Codex's options (its daemon), and which harness kinds the server omits.
type rigOptions struct {
	profile       harness.Profile
	claude        *claudecodesetup.Options
	codex         *codexsetup.Options
	omittedKinds  []string
	cacheIdentity string
}

// registry is the main server's harnesses.
var registry = harnesses.Registry()

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newRig(t *testing.T, env map[string]string) *rig {
	t.Helper()
	return newRigWith(t, env, rigOptions{profile: harness.Profile{Kind: harness.ProfileServer}})
}

func newRigWith(t *testing.T, env map[string]string, o rigOptions) *rig {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var temp dbtest.DB
	store := db.NewStore(temp.Reset(t))
	ctx, cancel := context.WithCancel(context.Background())
	p, err := store.CreatePairing(ctx, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	grant, err := store.RedeemPairing(ctx, p.Code, "", "test")
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, store: store, id: grant.MachineID, env: env, fake: t3.NewFake(), opts: o}
	r.hub = workerchan.NewHub(ctx, workerchan.Options{Version: "test"})
	r.set = machine.MustSet(machine.NewLocal(db.LocalMachine, harness.Profile{}, nil))
	reg := registry
	conns := func(id int64) []int64 {
		var ids []int64
		for _, c := range store.ActiveConnections() {
			if c.Machine == id {
				ids = append(ids, c.ID)
			}
		}
		return ids
	}
	r.hosts = remote.NewHosts(r.set, r.hub, conns, nil, quiet())
	kinds := []string{t3.Kind, opencode.Kind, claudecode.Kind, codex.Kind}
	r.hosts.SetSetup(remote.SetupHooks{
		Kinds: kinds, Active: store.ActiveID,
		Changed: func(int64, string, string) { r.changes++ },
	})
	remote.ServeSetup(r.hub, func(id int64) machine.StorePorts { return machine.NewStorePorts(store.OnMachine(id), reg) }, nil, harnesses.Modules())
	r.hosts.Join(r.id)
	handler := api.NewRouter(api.Deps{Store: store, Harnesses: reg, Channels: r.hub, Logger: quiet()})
	r.hub.SetHarnessKinds(slices.DeleteFunc(slices.Clone(kinds), func(kind string) bool { return slices.Contains(o.omittedKinds, kind) }))
	r.srv = httptest.NewServer(handler)
	r.cfgPath = filepath.Join(t.TempDir(), "worker", worker.ConfigFile)
	short, err := os.MkdirTemp("", "ws")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	if r.listen, err = listen.ParseList("unix:" + filepath.Join(short, "gw.sock")); err != nil {
		t.Fatal(err)
	}
	if err := worker.SaveConfig(r.cfgPath, worker.Config{Schema: worker.ConfigSchema, URL: r.srv.URL, MachineID: r.id, Label: "laptop", Token: grant.Token, JoinedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		r.stopWorker()
		cancel()
		r.srv.Close()
	})
	r.startWorker()
	return r
}

func (r *rig) startWorker() {
	r.startWorkerWith(nil)
}

func (r *rig) startWorkerWith(onHost func(context.Context, *remote.Host, *workersetup.Setup)) {
	r.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r.stop, r.done = cancel, make(chan error, 1)
	host := func(id int64, call workerchan.Upcall) worker.Host {
		h := remote.NewHost(machine.NewLocal(id, r.opts.profile, machine.LocalOps{}))
		stateDir := ""
		if r.opts.cacheIdentity != "" {
			stateDir = worker.Dir(r.cfgPath)
		}
		setup := workersetup.Attach(workersetup.Options{
			Host: h, Ports: workersetup.Ports{Call: call, Creds: worker.NewCredentialStore(r.cfgPath, harnesses.LegacySecrets()...)},
			Getenv: func(k string) string { return r.env[k] }, Logger: quiet(),
			Overrides: map[string]any{t3.Kind: r.fake, claudecode.Kind: r.opts.claude, codex.Kind: r.opts.codex},
			StateDir:  stateDir, CacheIdentity: r.opts.cacheIdentity,
		})
		if onHost != nil {
			onHost(ctx, h, setup)
		}
		return h
	}
	done := r.done
	go func() {
		done <- worker.Run(ctx, worker.RunOptions{ConfigPath: r.cfgPath, Logger: quiet(), HTTP: r.srv.Client(), Listen: r.listen,
			MinBackoff: 20 * time.Millisecond, MaxBackoff: 100 * time.Millisecond, Host: host})
	}()
	r.wait("setup composed and described", func() bool {
		m := r.machine()
		if !m.Online() {
			return false
		}
		_, err := m.Connectors()[t3.Kind].Status(context.Background(), false)
		return err == nil
	})
}

func (r *rig) stopWorker() {
	if r.stop == nil {
		return
	}
	r.stop()
	r.stop = nil
	select {
	case err := <-r.done:
		if err != nil {
			r.t.Errorf("worker: %v", err)
		}
	case <-time.After(10 * time.Second):
		r.t.Error("worker did not stop")
	}
	r.wait("machine offline", func() bool { return !r.machine().Online() })
}

func (r *rig) machine() *remote.Machine {
	m, ok := r.hosts.Machine(r.id)
	if !ok {
		r.t.Fatal("remote machine not hosted")
	}
	return m
}

func (r *rig) connector(kind string) harness.Connector {
	c, ok := r.machine().Connector(kind)
	if !ok {
		r.t.Fatalf("no %s connector", kind)
	}
	return c
}

func (r *rig) wait(what string, ok func() bool) {
	r.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			r.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func savedSettings[T any](r *rig, machineID int64, kind string) T {
	r.t.Helper()
	var settings T
	raw, err := r.store.HarnessSettings(r.t.Context(), machineID, kind)
	if err != nil {
		r.t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		r.t.Fatal(err)
	}
	return settings
}

func (r *rig) openCodeCred() opencodesetup.Credential {
	r.t.Helper()
	var c opencodesetup.Credential
	raw, err := worker.NewCredentialStore(r.cfgPath).Get(r.t.Context(), opencode.Kind, "credential")
	if err != nil {
		r.t.Fatal(err)
	}
	if raw != nil {
		if err := json.Unmarshal(raw, &c); err != nil {
			r.t.Fatal(err)
		}
	}
	return c
}

func TestRemoteT3PairCreatesTheMachinesConnectionAndInstallsItsAdapter(t *testing.T) {
	r := newRig(t, map[string]string{})
	ctx := t.Context()
	if err := r.fake.Unpair(ctx); err != nil {
		t.Fatal(err)
	}
	c := r.connector(t3.Kind)
	st, err := c.Status(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if sum := c.Summary(ctx); sum.ConnectionID != 0 || sum.Configured {
		t.Fatalf("fresh machine summary %+v", sum)
	}
	if id := r.store.ActiveID(r.id, t3.Kind); id != 0 {
		t.Fatalf("T3 connection %d before pairing", id)
	}
	if f := st.Fields[0]; f.ID != "url" || f.Source != "default" {
		t.Fatalf("url field %+v", f)
	}
	res, err := c.Run(ctx, t3.ActionPair, json.RawMessage(`{"code":"ABCD-1234"}`))
	if err != nil || res.Replaced != 0 {
		t.Fatalf("pair: %+v %v", res, err)
	}
	id := r.store.ActiveID(r.id, t3.Kind)
	if id == 0 || r.store.ConnectionMachine(id) != r.id {
		t.Fatalf("pairing created connection %d on machine %d", id, r.store.ConnectionMachine(id))
	}
	r.wait("adapter on the new connection", func() bool {
		a, ok := r.machine().Adapter(id)
		return ok && a.Health(ctx).Available
	})
	if sum := c.Summary(ctx); sum.ConnectionID != id || !sum.Configured || !sum.Connected {
		t.Fatalf("paired summary %+v", sum)
	}
	if h := r.machine().Adapters()[id].Health(ctx); !h.Available {
		t.Fatalf("adapter health %+v", h)
	}
	if r.changes == 0 {
		t.Fatal("setup change hook not run")
	}
	// The local machine's T3 setup is untouched.
	if local := r.store.ActiveID(db.LocalMachine, t3.Kind); local == id {
		t.Fatal("paired the local machine")
	}
}

// A remote machine's T3 URLs are recorded on the main server; its admin
// token stays in the worker's credentials file. Disconnect clears both.
func TestRemoteT3SettingsStayWhereTheyBelong(t *testing.T) {
	r := newRig(t, map[string]string{})
	ctx := t.Context()
	c := r.connector(t3.Kind)
	if _, err := c.Configure(ctx, json.RawMessage(`{"url":"http://t3.laptop:3773","publicUrl":"https://t3.example"}`)); err != nil {
		t.Fatal(err)
	}
	settings := savedSettings[t3setup.Settings](r, r.id, t3.Kind)
	if settings.URL == nil || *settings.URL != "http://t3.laptop:3773" || settings.PublicURL == nil || *settings.PublicURL != "https://t3.example" {
		t.Fatalf("main server record %+v", settings)
	}
	if _, err := c.Configure(ctx, json.RawMessage(`{"adminToken":"admin-secret"}`)); err != nil {
		t.Fatal(err)
	}
	if tok, err := r.store.HarnessSecret(ctx, r.id, t3.Kind, "adminToken"); err != nil || tok != nil {
		t.Fatalf("main server admin token %q %v", tok, err)
	}
	creds, err := os.ReadFile(worker.NewCredentialStore(r.cfgPath).Path())
	if err != nil || !strings.Contains(string(creds), "admin-secret") {
		t.Fatalf("worker credentials %q %v", creds, err)
	}
	if sum := c.Summary(ctx); !sum.Configured {
		t.Fatalf("admin token did not configure T3: %+v", sum)
	}
	if err := c.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if sum := c.Summary(ctx); sum.Configured {
		t.Fatalf("disconnected T3 configured: %+v", sum)
	}
	settings = savedSettings[t3setup.Settings](r, r.id, t3.Kind)
	creds, _ = os.ReadFile(worker.NewCredentialStore(r.cfgPath).Path())
	if settings.URL != nil || settings.PublicURL != nil || strings.Contains(string(creds), "admin-secret") {
		t.Fatalf("disconnect kept settings: %+v %q", settings, creds)
	}
}

func TestRemoteOpenCodeCredentialsStayOnTheWorker(t *testing.T) {
	r := newRig(t, map[string]string{})
	ctx := t.Context()
	fake := opencodetest.New(t, "")
	c := r.connector(opencode.Kind)
	if id := r.store.ActiveID(r.id, opencode.Kind); id != 0 {
		t.Fatalf("OpenCode connection %d before connecting", id)
	}
	if _, err := c.Run(ctx, opencode.ActionPair, json.RawMessage(`{"link":"`+fake.Link()+`"}`)); err != nil {
		t.Fatal(err)
	}
	id := r.store.ActiveID(r.id, opencode.Kind)
	if id == 0 {
		t.Fatal("connecting created no OpenCode connection on the machine")
	}
	setup := savedSettings[opencodesetup.SetupSettings](r, r.id, opencode.Kind)
	if setup.Endpoint != fake.URL || setup.Auth != opencodesetup.AuthPaired {
		t.Fatalf("main settings %+v", setup)
	}
	if got, verified, err := r.store.ActiveConnectionIdentity(ctx, r.id, opencode.Kind); err != nil || got != id || verified != fake.URL {
		t.Fatalf("main identity %d %q %v", got, verified, err)
	}
	if cred, err := r.store.HarnessSecret(ctx, r.id, opencode.Kind, "credential"); err != nil || cred != nil {
		t.Fatalf("main server credential %q %v", cred, err)
	}
	if got := r.openCodeCred(); got.Password == "" || !fake.Accepts(got.Password) {
		t.Fatalf("worker credential %+v", got)
	}
	info, err := os.Stat(worker.NewCredentialStore(r.cfgPath).Path())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials file %v %v", info, err)
	}
	r.wait("adapter installed", func() bool { _, ok := r.machine().Adapter(id); return ok })
	if sum := c.Summary(ctx); !sum.Configured || sum.ConnectionID != id {
		t.Fatalf("summary %+v", sum)
	}

	// A restarted worker reconnects with the credential from its file.
	r.stopWorker()
	if _, err := c.Status(ctx, false); !isUnavailable(err, remote.CodeMachineOffline) {
		t.Fatalf("offline status: %v", err)
	}
	if sum := c.Summary(ctx); sum.Connected || sum.ConnectionID != id {
		t.Fatalf("offline summary %+v", sum)
	}
	r.startWorker()
	st, err := c.Status(ctx, true)
	if err != nil || st.State != "connected" {
		t.Fatalf("after restart %+v %v", st, err)
	}

	if err := c.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.openCodeCred(); got.Password != "" {
		t.Fatalf("disconnect kept the credential %+v", got)
	}
}

func TestSetupCallsStayOnTheCallersMachine(t *testing.T) {
	r := newRig(t, map[string]string{})
	ctx := t.Context()
	local := r.store.ActiveID(db.LocalMachine, t3.Kind)
	if local == 0 {
		t.Fatal("no local T3 connection")
	}
	// A worker-side port calling as the remote machine.
	var p workersetup.Ports
	p.Call = func(ctx context.Context, kind string, body any) (json.RawMessage, error) {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		env, err := r.hub.Deliver(ctx, r.id, sessionOf(t, r), workerchan.Envelope{ID: "t1", Kind: kind, Body: raw})
		if err != nil {
			return nil, err
		}
		if env.Error != nil {
			return nil, &workerchan.RemoteError{Code: env.Error.Code, Message: env.Error.Message}
		}
		return env.Body, nil
	}
	if exists, err := p.ConnectionExists(ctx, local); err != nil || exists {
		t.Fatalf("another machine's connection exists: %v %v", exists, err)
	}
	if err := p.SetConnectionIdentity(ctx, local, "x", "x"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("stamped another machine's connection: %v", err)
	}
	if _, _, _, err := p.ConnectionEvidence(ctx, local); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("read another machine's evidence: %v", err)
	}
	if _, err := p.Call(ctx, remote.KindSettingsSave, json.RawMessage(`{"kind":"t3","machineId":1,"patch":{"url":"http://laptop:3773"}}`)); err != nil {
		t.Fatal(err)
	}
	if settings := savedSettings[t3setup.Settings](r, db.LocalMachine, t3.Kind); settings.URL != nil {
		t.Fatal("set the local machine's T3 URL")
	}
	settings := savedSettings[t3setup.Settings](r, r.id, t3.Kind)
	if settings.URL == nil || *settings.URL != "http://laptop:3773" {
		t.Fatalf("remote T3 URL %+v", settings.URL)
	}
	raw, err := p.Call(ctx, remote.KindSetupSnapshot, json.RawMessage(`{"kinds":["t3"],"machineId":1}`))
	var inputs remote.SetupSnapshot
	if err != nil || json.Unmarshal(raw, &inputs) != nil {
		t.Fatalf("setup snapshot: %s %v", raw, err)
	}
	t3Input := inputs[t3.Kind]
	if len(inputs) != 1 || t3Input.Connection.ID != 0 || string(t3Input.Settings) != `{"url":"http://laptop:3773"}` {
		t.Fatalf("snapshot did not use the caller's settings and connection: %+v", inputs)
	}
	for _, test := range []struct {
		kind, patch string
	}{
		{t3.Kind, `{"adminToken":"secret"}`},
		{opencode.Kind, `{"password":"secret"}`},
		{t3.Kind, `{"url":false}`},
		{"unknown", `{"url":"http://laptop:3773"}`},
	} {
		var invalid *db.InvalidError
		if err := p.Save(ctx, test.kind, json.RawMessage(test.patch)); !errors.As(err, &invalid) {
			t.Errorf("saved invalid %s settings: %v", test.kind, err)
		}
	}
	if raw, err := p.Get(ctx, t3.Kind); err != nil || string(raw) != `{"url":"http://laptop:3773"}` {
		t.Fatalf("remote settings %s %v", raw, err)
	}

	if err := p.SetLastError(ctx, opencode.Kind, "test error"); err != nil {
		t.Fatal(err)
	}
	if status, err := r.store.HarnessSyncStatus(ctx, r.id, opencode.Kind); err != nil || status.LastError == nil || *status.LastError != "test error" {
		t.Fatalf("remote status %+v %v", status, err)
	}
	if status, err := r.store.HarnessSyncStatus(ctx, db.LocalMachine, opencode.Kind); err != nil || status.LastError != nil {
		t.Fatalf("local status %+v %v", status, err)
	}
	if err := p.SetLastError(ctx, opencode.Kind, ""); err != nil {
		t.Fatal(err)
	}
	if status, err := r.store.HarnessSyncStatus(ctx, r.id, opencode.Kind); err != nil || status.LastError != nil {
		t.Fatalf("remote status after clearing %+v %v", status, err)
	}
	if err := r.store.SaveHarnessSettings(ctx, r.id, t3.Kind, json.RawMessage(`{"adminToken":"invalid-stored-setting"}`)); err != nil {
		t.Fatal(err)
	}
	var rejected *workerchan.RemoteError
	if raw, err := p.Call(ctx, remote.KindSetupSnapshot, remote.SetupSnapshotRequest{Kinds: []string{t3.Kind}}); len(raw) != 0 || !errors.As(err, &rejected) || rejected.Code != remote.CodeInvalid {
		t.Fatalf("snapshot accepted invalid stored settings: %v", err)
	}
}

// sessionOf is the remote machine's live channel session id (from a
// worker-side call's point of view the hub needs it).
func sessionOf(t *testing.T, r *rig) string {
	t.Helper()
	id := r.hub.SessionID(r.id)
	if id == "" {
		t.Fatal("no live session")
	}
	return id
}

func isUnavailable(err error, code string) bool {
	var u *remote.UnavailableError
	return errors.As(err, &u) && u.Code == code
}
