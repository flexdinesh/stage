package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/filelock"
	"github.com/flexdinesh/stage/server/internal/instance"
	"github.com/flexdinesh/stage/server/internal/listen"
	"github.com/flexdinesh/stage/server/internal/worker"
)

// fakeServer is the "server process" a fake manager runs: it really holds
// the data dir lock (in-process) with a fake pid, as the real one would.
type fakeServer struct {
	pid      int
	instance string
	owner    *instance.Owner
	sock     *filelock.Lock
	wlock    *filelock.Lock // worker role: the worker dir lock
}

// world is one test's isolated environment plus a fake manager.
type world struct {
	t       *testing.T
	goos    string
	home    string
	rt      string
	exe     string
	env     map[string]string
	dataDir string

	mu       sync.Mutex
	calls    [][]string
	nextPID  int
	srv      *fakeServer
	t3Online bool

	// systemd state
	unavailable bool
	loadedDef   []byte // what daemon-reload saw
	loaded      bool
	active      string // active | activating | deactivating | failed | inactive
	sub         string
	result      string
	exitCode    string
	exitStatus  int
	restarts    int
	invocation  int
	enabled     bool
	masked      bool
	// fragment, when set, is a foreign unit file systemd loaded instead.
	fragment string

	// launchd state
	lloaded       bool
	lstate        string
	lruns         int
	llastExit     string
	bootstrapCode int // nonzero: bootstrap "fails" with it (but still loads when bootstrapLoads)
	bootstrapLoad bool

	// role is the service under test (zero: the server).
	role Role
	// workerState, when set, is the channel state a started worker
	// reports.
	workerState string

	// onStart decides what a start does (default: a healthy server).
	onStart func(w *world)
	// stuck: a stop leaves the server draining (deactivating, lock held).
	stuck  bool
	linger string
}

func newWorld(t *testing.T, goos string) *world {
	t.Helper()
	short, err := os.MkdirTemp("", "svc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	w := &world{t: t, goos: goos, home: filepath.Join(short, "h"), rt: filepath.Join(short, "rt"), nextPID: 4000, t3Online: true, linger: "yes"}
	for _, d := range []string{w.home, w.rt} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	w.exe = filepath.Join(short, "bin", "stage")
	_ = os.MkdirAll(filepath.Dir(w.exe), 0o755)
	if err := os.WriteFile(w.exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	w.env = map[string]string{"HOME": w.home, "XDG_RUNTIME_DIR": w.rt, "USER": "u"}
	w.dataDir = filepath.Join(w.home, ".local", "share", "stage")
	if goos == "darwin" {
		w.dataDir = filepath.Join(w.home, "Library", "Application Support", "stage")
	}
	w.active, w.lstate = "inactive", "not running"
	w.onStart = func(w *world) { w.run() }
	t.Cleanup(func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.kill()
	})
	return w
}

func (w *world) deps() Deps {
	return Deps{
		Role: w.role, GOOS: w.goos, UID: 1000, Getenv: func(k string) string { return w.env[k] },
		Runner: w, Health: w.health,
		Probe: func(_ context.Context, exe string) (BuildInfo, error) {
			return BuildInfo{Identity: buildinfo.Identity, Version: buildinfo.Version}, nil
		},
		Self: func() string { return w.exe }, Poll: time.Millisecond,
	}
}

func (w *world) ctl() *Controller { return New(w.deps()) }

// workerDir is the worker service's default worker dir.
func (w *world) workerDir() string { return filepath.Join(w.dataDir, worker.DirName) }

// join writes a worker config at the default path.
func (w *world) join() {
	w.t.Helper()
	c := worker.Config{Schema: worker.ConfigSchema, URL: "https://main.example", MachineID: 2, Label: "l", Token: "stage_mt_1_x"}
	if err := worker.SaveConfig(filepath.Join(w.workerDir(), worker.ConfigFile), c); err != nil {
		w.t.Fatal(err)
	}
}

// lockWorkerDir takes the worker dir lock as a worker process pid would.
func (w *world) lockWorkerDir(pid int, managed bool) *filelock.Lock {
	_ = os.MkdirAll(w.workerDir(), 0o700)
	l, err := filelock.Acquire(filepath.Join(w.workerDir(), worker.LockFile), 0o600)
	if err != nil {
		w.t.Errorf("worker lock: %v", err)
		return nil
	}
	b, _ := json.Marshal(worker.Holder{PID: pid, Version: buildinfo.Version, StartedAt: time.Now().UTC(), Managed: managed})
	_ = l.File().Truncate(0)
	_, _ = l.File().WriteAt(b, 0)
	return l
}

// run starts a healthy server or worker (call with mu held).
func (w *world) run() {
	w.kill()
	if w.role == RoleWorker {
		w.nextPID++
		w.srv = &fakeServer{pid: w.nextPID, wlock: w.lockWorkerDir(w.nextPID, true)}
		if w.workerState != "" {
			b, _ := json.Marshal(worker.State{Schema: 1, PID: w.nextPID, Managed: true, State: w.workerState, LastError: "dial tcp: connection refused"})
			_ = os.WriteFile(filepath.Join(w.workerDir(), worker.StateFile), b, 0o600)
		}
		w.active, w.sub, w.result = "active", "running", "success"
		w.lstate, w.lruns = "running", w.lruns+1
		w.invocation++
		return
	}
	o, err := instance.Lock(w.dataDir)
	if err != nil {
		w.t.Errorf("fake server lock: %v", err)
		return
	}
	w.nextPID++
	m := instance.NewMeta()
	m.PID, m.Managed, m.Manager = w.nextPID, true, ManagerKind(w.goos)
	if err := o.Record(m); err != nil {
		w.t.Errorf("record: %v", err)
	}
	sock, err := ResolvePaths(w.goos, func(k string) string { return w.env[k] }, nil, filepath.Dir(w.unitPath()))
	if err != nil {
		w.t.Errorf("paths: %v", err)
		return
	}
	_ = os.MkdirAll(filepath.Dir(sock.Socket), 0o700)
	sl, err := filelock.Acquire(listen.LockPath(sock.Socket), 0o600)
	if err != nil {
		w.t.Errorf("socket lock: %v", err)
	}
	w.srv = &fakeServer{pid: m.PID, instance: m.InstanceID, owner: o, sock: sl}
	w.active, w.sub, w.result = "active", "running", "success"
	w.lstate, w.lruns = "running", w.lruns+1
	w.invocation++
}

// kill ends the fake server (call with mu held).
func (w *world) kill() {
	if w.srv != nil {
		if w.srv.owner != nil {
			w.srv.owner.Release()
		}
		if w.srv.wlock != nil {
			w.srv.wlock.Release()
		}
		if w.srv.sock != nil {
			w.srv.sock.Release()
		}
		w.srv = nil
	}
}

func (w *world) health(_ context.Context, url string) (client.Health, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.srv == nil || !strings.HasPrefix(url, "unix://") {
		return client.Health{}, errors.New("stage unreachable")
	}
	h := client.Health{Status: "ok", DB: "ok", Service: "stage", Identity: "stage", Version: buildinfo.Version, APIVersion: buildinfo.APIVersion, InstanceID: w.srv.instance}
	var lastErr *string
	if !w.t3Online {
		e := "dial tcp 127.0.0.1:3773: connection refused"
		lastErr = &e
	}
	h.Harnesses = append(h.Harnesses, struct {
		Kind         string  `json:"kind"`
		ConnectionID int64   `json:"connectionId"`
		Configured   bool    `json:"configured"`
		Connected    bool    `json:"connected"`
		LastError    *string `json:"lastError"`
	}{Kind: "t3", ConnectionID: 1, Configured: true, Connected: w.t3Online, LastError: lastErr})
	return h, nil
}

func (w *world) called(verb string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for _, c := range w.calls {
		if strings.Contains(strings.Join(c, " "), verb) {
			n++
		}
	}
	return n
}

func (w *world) callLog() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var b strings.Builder
	for _, c := range w.calls {
		b.WriteString(strings.Join(c, " ") + "\n")
	}
	return b.String()
}

func (w *world) unitPath() string {
	return filepath.Join(w.home, ".config", "systemd", "user", UnitNameFor(w.role))
}

func (w *world) Run(_ context.Context, c Cmd) (Result, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, append([]string{c.Name}, c.Args...))
	for _, a := range c.Args {
		if a == "kill" {
			w.t.Errorf("manager asked to kill: %v", c.Args)
		}
	}
	switch c.Name {
	case "systemctl":
		return w.systemctl(c.Args[2:]), nil
	case "journalctl":
		return Result{Stdout: "line one\nline two\n"}, nil
	case "loginctl":
		return Result{Stdout: w.linger + "\n"}, nil
	case "launchctl":
		return w.launchctl(c.Args), nil
	}
	return Result{}, fmt.Errorf("%s not found", c.Name)
}

func (w *world) systemctl(a []string) Result {
	if w.unavailable {
		return Result{Code: 1, Stderr: "Failed to connect to bus: No medium found\n"}
	}
	switch a[0] {
	case "show":
		if a[1] == "-p" {
			return Result{Stdout: "UnitPath=" + filepath.Dir(w.unitPath()) + " /usr/lib/systemd/user\nVersion=262.2-1-arch\n"}
		}
		cur, _ := os.ReadFile(w.unitPath())
		load := "not-found"
		frag := ""
		if w.loaded {
			load, frag = "loaded", w.unitPath()
		}
		if w.fragment != "" {
			load, frag = "loaded", w.fragment
		}
		if w.masked {
			load = "masked"
		}
		need := "no"
		if w.loaded && string(cur) != string(w.loadedDef) {
			need = "yes"
		}
		pid := 0
		if w.srv != nil && w.active != "inactive" && w.active != "failed" {
			pid = w.srv.pid
		}
		ufs := "disabled"
		if w.enabled {
			ufs = "enabled"
		}
		return Result{Stdout: fmt.Sprintf("Result=%s\nNRestarts=%d\nExecMainCode=%s\nExecMainStatus=%d\nMainPID=%d\nInvocationID=inv%d\nLoadState=%s\nActiveState=%s\nSubState=%s\nFragmentPath=%s\nDropInPaths=\nNeedDaemonReload=%s\nUnitFileState=%s\n",
			w.result, w.restarts, w.exitCode, w.exitStatus, pid, w.invocation, load, w.active, w.sub, frag, need, ufs)}
	case "show-environment":
		return Result{Stdout: fmt.Sprintf("HOME=%s\nXDG_RUNTIME_DIR=%s\nPATH=/usr/bin\n", w.home, w.rt)}
	case "daemon-reload":
		cur, err := os.ReadFile(w.unitPath())
		w.loaded, w.loadedDef = err == nil, cur
	case "reset-failed":
		if w.active == "failed" {
			w.active = "inactive"
		}
		w.result = "success"
	case "start":
		if !w.loaded {
			return Result{Code: 5, Stderr: "Unit stage.service not found.\n"}
		}
		if w.active != "active" && w.active != "activating" {
			w.onStart(w)
		}
	case "stop":
		if w.stuck {
			w.active, w.sub = "deactivating", "stop-sigterm"
			break
		}
		w.kill()
		w.active, w.sub = "inactive", "dead"
	case "restart":
		w.onStart(w)
	case "enable":
		w.enabled = true
	case "disable":
		w.enabled = false
	}
	return Result{}
}

func (w *world) launchctl(a []string) Result {
	target := "gui/1000/" + LabelFor(w.role)
	switch a[0] {
	case "print":
		if a[1] == "gui/1000" {
			if w.unavailable {
				return Result{Code: 113, Stderr: "Could not find domain\n"}
			}
			return Result{Stdout: "gui/1000 = {\n}\n"}
		}
		if !w.lloaded {
			return Result{Code: 113, Stderr: "Could not find service \"x\" in domain for user gui: 1000\n"}
		}
		pid := ""
		if w.srv != nil && w.lstate == "running" {
			pid = fmt.Sprintf("\tpid = %d\n", w.srv.pid)
		}
		last := w.llastExit
		if last == "" {
			last = "(never exited)"
		}
		return Result{Stdout: fmt.Sprintf("%s = {\n\tpath = %s\n\tstate = %s\n\truns = %d\n%s\tlast exit code = %s\n}\n", target, filepath.Join(w.dataDir, LabelFor(w.role)+".plist"), w.lstate, w.lruns, pid, last)}
	case "enable":
	case "bootstrap":
		if w.lloaded {
			return Result{Code: 37, Stderr: "Bootstrap failed: 37: Operation already in progress\n"}
		}
		if w.bootstrapCode != 0 {
			if w.bootstrapLoad {
				w.lloaded = true
				w.onStart(w)
			}
			return Result{Code: w.bootstrapCode, Stderr: fmt.Sprintf("Bootstrap failed: %d: Input/output error\n", w.bootstrapCode)}
		}
		w.lloaded = true
		w.onStart(w)
	case "bootout":
		if !w.lloaded {
			return Result{Code: 113, Stderr: "Boot-out failed: 113\n"}
		}
		w.kill()
		w.lloaded, w.lstate = false, "not running"
	case "kickstart":
		w.onStart(w)
	}
	return Result{}
}
