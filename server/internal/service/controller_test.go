package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/instance"
)

var ctx = context.Background()

func start(t *testing.T, w *world) Report {
	t.Helper()
	return w.ctl().Start(ctx, StartOptions{Wait: 2 * time.Second})
}

func mustOK(t *testing.T, w *world, r Report) {
	t.Helper()
	if !r.OK || r.Code != 0 {
		t.Fatalf("%s: code %d %q fix %q tail %v\ncalls:\n%s", r.Command, r.Code, r.Message, r.Fix, r.LogTail, w.callLog())
	}
}

func TestStartFresh(t *testing.T) {
	w := newWorld(t, "linux")
	r := start(t, w)
	mustOK(t, w, r)
	if !r.Changes.DefinitionWritten || !r.Changes.ServiceEnvCreated || !r.Changes.Started || !r.Changes.ManagerReloaded {
		t.Fatalf("changes %+v", r.Changes)
	}
	unit, err := os.ReadFile(w.unitPath())
	if err != nil || !strings.Contains(string(unit), "ExecStart="+w.exe+" serve --managed") {
		t.Fatalf("unit: %s %v", unit, err)
	}
	envPath := filepath.Join(w.home, ".config", "stage", "service.env")
	if st, err := os.Stat(envPath); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("service.env: %v %v", st, err)
	}
	if r.Status == nil || !r.Status.Ready || r.Status.State != "running" || !r.Status.Lock.OwnedByService {
		t.Fatalf("status %+v", r.Status)
	}
	if !strings.Contains(r.Message, "ready") || !strings.Contains(r.Message, "stage.sock") {
		t.Fatalf("message %q", r.Message)
	}
	log := w.callLog()
	if strings.Index(log, "daemon-reload") > strings.Index(log, "start --no-block") || strings.Contains(log, "reset-failed") {
		t.Fatalf("order (reload before start, no reset-failed when fresh):\n%s", log)
	}
}

func TestStartTwiceOneServer(t *testing.T) {
	w := newWorld(t, "linux")
	mustOK(t, w, start(t, w))
	pid := w.srv.pid
	r := start(t, w)
	mustOK(t, w, r)
	if r.Changes.Started || r.Changes.DefinitionWritten || w.called("start --no-block") != 1 || w.srv.pid != pid {
		t.Fatalf("second start changed things: %+v\n%s", r.Changes, w.callLog())
	}
}

func TestConcurrentStartsOneStart(t *testing.T) {
	w := newWorld(t, "linux")
	var wg sync.WaitGroup
	reps := make([]Report, 3)
	for i := range reps {
		wg.Add(1)
		go func() { defer wg.Done(); reps[i] = start(t, w) }()
	}
	wg.Wait()
	for _, r := range reps {
		mustOK(t, w, r)
	}
	if n := w.called("start --no-block"); n != 1 {
		t.Fatalf("%d start calls\n%s", n, w.callLog())
	}
}

func TestStartRefusesForegroundHolder(t *testing.T) {
	w := newWorld(t, "linux")
	o, err := instance.Lock(w.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Release()
	m := instance.NewMeta()
	m.Listeners = []string{"http://127.0.0.1:7380"}
	_ = o.Record(m)
	r := start(t, w)
	if r.Code != CodeConflict || !strings.Contains(r.Message, "in use by another stage server") || w.called("start --no-block") != 0 {
		t.Fatalf("got %d %q\n%s", r.Code, r.Message, w.callLog())
	}
	if r.Status == nil || r.Status.Lock.OwnedByService || !hasProblem(r.Status, "foreground_holder") {
		t.Fatalf("status %+v", r.Status)
	}
}

func hasProblem(s *Status, code string) bool {
	for _, p := range s.Problems {
		if p.Code == code {
			return true
		}
	}
	return false
}

func TestStartExit75IsConflict(t *testing.T) {
	w := newWorld(t, "linux")
	w.onStart = func(w *world) { w.active, w.result, w.exitCode, w.exitStatus = "failed", "exit-code", "1", 75 }
	r := start(t, w)
	if r.Code != CodeConflict || len(r.LogTail) == 0 {
		t.Fatalf("got %d %q tail %v", r.Code, r.Message, r.LogTail)
	}
}

func TestStartFailsFastOnCrashLoop(t *testing.T) {
	w := newWorld(t, "linux")
	w.onStart = func(w *world) {
		w.active, w.sub, w.restarts, w.exitCode, w.exitStatus = "activating", "auto-restart", 1, "1", 1
	}
	r := start(t, w)
	if r.Code != CodeFailed || !strings.Contains(r.Message, "keeps restarting") || len(r.LogTail) == 0 {
		t.Fatalf("got %d %q tail %v", r.Code, r.Message, r.LogTail)
	}
}

func TestStartTimeout(t *testing.T) {
	w := newWorld(t, "linux")
	w.onStart = func(w *world) { w.active, w.sub = "activating", "start" }
	r := w.ctl().Start(ctx, StartOptions{Wait: 100 * time.Millisecond})
	if r.Code != CodeTimeout {
		t.Fatalf("got %d %q", r.Code, r.Message)
	}
}

func TestStartNoWait(t *testing.T) {
	w := newWorld(t, "linux")
	r := w.ctl().Start(ctx, StartOptions{})
	mustOK(t, w, r)
	if r.Message != "start requested" || !r.Changes.Started {
		t.Fatalf("%+v", r)
	}
}

func TestManagerUnavailable(t *testing.T) {
	w := newWorld(t, "linux")
	w.unavailable = true
	r := start(t, w)
	if r.Code != CodeUnavailable || !strings.Contains(r.Fix, "enable-linger") || !strings.Contains(r.Fix, "stage serve --listen unix") {
		t.Fatalf("got %d %q %q", r.Code, r.Message, r.Fix)
	}
	if _, err := os.Stat(w.unitPath()); !os.IsNotExist(err) {
		t.Fatal("wrote a unit without a manager")
	}
	st, code := w.ctl().Status(ctx)
	if code != CodeUnavailable || st.State != "unknown" || !hasProblem(&st, "manager_unavailable") {
		t.Fatalf("status %d %+v", code, st)
	}
}

func TestRootRefused(t *testing.T) {
	w := newWorld(t, "linux")
	w.env["SUDO_USER"] = "u"
	if r := start(t, w); r.Code != CodeInvalid || len(w.calls) != 0 {
		t.Fatalf("got %d %q", r.Code, r.Message)
	}
}

func TestDefinitionRules(t *testing.T) {
	w := newWorld(t, "linux")
	// Foreign unit: untouched, refused.
	_ = os.MkdirAll(filepath.Dir(w.unitPath()), 0o755)
	foreign := []byte("[Service]\nExecStart=/usr/bin/other\n")
	_ = os.WriteFile(w.unitPath(), foreign, 0o644)
	if r := start(t, w); r.Code != CodeInvalid {
		t.Fatalf("foreign: %d %q", r.Code, r.Message)
	}
	if b, _ := os.ReadFile(w.unitPath()); string(b) != string(foreign) {
		t.Fatal("foreign unit rewritten")
	}
	_ = os.Remove(w.unitPath())
	mustOK(t, w, start(t, w))

	// Another CLI binary: definition kept with a warning.
	other := filepath.Join(filepath.Dir(w.exe), "stage2")
	_ = os.WriteFile(other, []byte("#!/bin/sh\n"), 0o755)
	d := w.deps()
	d.Self = func() string { return other }
	r := New(d).Start(ctx, StartOptions{Wait: time.Second})
	mustOK(t, w, r)
	if r.Changes.DefinitionWritten || len(r.Warnings) == 0 || !strings.Contains(r.Warnings[0], "--exe") {
		t.Fatalf("exe differs: %+v %v", r.Changes, r.Warnings)
	}
	// --exe switches it (pending restart: start never restarts).
	r = New(d).Start(ctx, StartOptions{Exe: other, Wait: time.Second})
	mustOK(t, w, r)
	if !r.Changes.DefinitionWritten || !r.Changes.PendingRestart || r.Changes.Started {
		t.Fatalf("--exe: %+v", r.Changes)
	}
	if b, _ := os.ReadFile(w.unitPath()); !strings.Contains(string(b), other+" serve") {
		t.Fatalf("unit not switched:\n%s", b)
	}
	// A stale unit of ours with the same exe is rewritten.
	b, _ := os.ReadFile(w.unitPath())
	_ = os.WriteFile(w.unitPath(), []byte(strings.Replace(string(b), "RestartSec=2s", "RestartSec=9s", 1)), 0o644)
	r = New(d).Start(ctx, StartOptions{Wait: time.Second})
	mustOK(t, w, r)
	if !r.Changes.DefinitionWritten {
		t.Fatalf("stale not rewritten: %+v", r.Changes)
	}
}

func TestMaskedRefused(t *testing.T) {
	w := newWorld(t, "linux")
	w.masked = true
	if r := start(t, w); r.Code != CodeInvalid || !strings.Contains(r.Fix, "unmask") {
		t.Fatalf("got %d %q", r.Code, r.Message)
	}
}

func TestInvalidServiceEnv(t *testing.T) {
	w := newWorld(t, "linux")
	p := filepath.Join(w.home, ".config", "stage", "service.env")
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	_ = os.WriteFile(p, []byte("STAGE_DATA_DIR=/x\n"), 0o600)
	if r := start(t, w); r.Code != CodeInvalid || w.called("start --no-block") != 0 {
		t.Fatalf("got %d %q", r.Code, r.Message)
	}
	_ = os.WriteFile(p, []byte("STAGE_LISTEN=127.0.0.1:7380\n"), 0o600)
	if r := start(t, w); r.Code != CodeInvalid || !strings.Contains(r.Message, "must include unix") {
		t.Fatalf("tcp-only: %d %q", r.Code, r.Message)
	}
}

func TestStopAndRestart(t *testing.T) {
	w := newWorld(t, "linux")
	mustOK(t, w, start(t, w))
	old := w.srv.instance
	r := w.ctl().Restart(ctx, StartOptions{Wait: 2 * time.Second})
	mustOK(t, w, r)
	if w.srv.instance == old || w.called("restart --no-block") != 1 {
		t.Fatalf("restart: %+v\n%s", r, w.callLog())
	}
	r = w.ctl().Stop(ctx, time.Second)
	mustOK(t, w, r)
	if !r.Changes.Stopped || r.Message != "stopped" {
		t.Fatalf("stop %+v", r)
	}
	if held, _, _ := instance.ReadHolder(w.dataDir); held {
		t.Fatal("data dir still held after stop")
	}
	r = w.ctl().Stop(ctx, time.Second)
	mustOK(t, w, r)
	if r.Changes.Stopped || r.Message != "not running" {
		t.Fatalf("second stop %+v", r)
	}
	st, code := w.ctl().Status(ctx)
	if code != CodeNotRunning || st.State != "stopped" {
		t.Fatalf("status %d %s", code, st.State)
	}
}

func TestStopLeavesForegroundServer(t *testing.T) {
	w := newWorld(t, "linux")
	mustOK(t, w, start(t, w))
	mustOK(t, w, w.ctl().Stop(ctx, time.Second))
	o, err := instance.Lock(w.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Release()
	_ = o.Record(instance.NewMeta())
	before := w.called("stop --no-block")
	r := w.ctl().Stop(ctx, time.Second)
	mustOK(t, w, r)
	if w.called("stop --no-block") != before || len(r.Notes) == 0 || !strings.Contains(r.Notes[0], "not touched") {
		t.Fatalf("stop with foreground holder: %+v", r)
	}
	if held, _, _ := instance.ReadHolder(w.dataDir); !held {
		t.Fatal("foreground server lost its lock")
	}
}

func TestStopWaitsForExit(t *testing.T) {
	w := newWorld(t, "linux")
	mustOK(t, w, start(t, w))
	// The server keeps draining (still holds the data dir lock).
	w.mu.Lock()
	w.stuck = true
	w.mu.Unlock()
	r := w.ctl().Stop(ctx, 100*time.Millisecond)
	if r.Code != CodeTimeout {
		t.Fatalf("got %d %q", r.Code, r.Message)
	}
}

func TestEnableDisableUninstall(t *testing.T) {
	w := newWorld(t, "linux")
	r := w.ctl().Enable(ctx, "")
	mustOK(t, w, r)
	if !w.enabled || w.srv != nil || r.Changes.LoginStartup == nil || !*r.Changes.LoginStartup || w.called("start --no-block") != 0 {
		t.Fatalf("enable: %+v\n%s", r, w.callLog())
	}
	if r.Status == nil || !r.Status.Definition.LoginStartup {
		t.Fatalf("status after enable %+v", r.Status)
	}
	mustOK(t, w, start(t, w))
	mustOK(t, w, w.ctl().Disable(ctx))
	if w.enabled || w.srv == nil {
		t.Fatal("disable changed running state or kept login startup")
	}
	r = w.ctl().Uninstall(ctx, time.Second)
	mustOK(t, w, r)
	if _, err := os.Stat(w.unitPath()); !os.IsNotExist(err) || w.srv != nil || !r.Changes.DefinitionRemoved {
		t.Fatalf("uninstall: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(w.home, ".config", "stage", "service.env")); err != nil {
		t.Fatal("service.env removed")
	}
	log := w.callLog()
	if strings.LastIndex(log, "daemon-reload") < strings.LastIndex(log, "stop --no-block") {
		t.Fatalf("no reload after removal:\n%s", log)
	}
	st, code := w.ctl().Status(ctx)
	if code != CodeNotRunning || st.State != "not-installed" {
		t.Fatalf("status %d %s", code, st.State)
	}
	mustOK(t, w, w.ctl().Uninstall(ctx, time.Second))
}

func TestStatusSeparatesT3(t *testing.T) {
	w := newWorld(t, "linux")
	w.t3Online = false
	w.linger = "no"
	r := start(t, w)
	mustOK(t, w, r)
	joined := strings.Join(r.Notes, "\n")
	if !strings.Contains(joined, "T3 is not connected") || !strings.Contains(joined, "lingering is off") {
		t.Fatalf("notes %v", r.Notes)
	}
	st, code := w.ctl().Status(ctx)
	if code != CodeOK || !st.Ready || st.T3 == nil || st.T3.Connected || !hasProblem(&st, "t3_offline") || !hasProblem(&st, "linger_off") {
		t.Fatalf("status %d %+v", code, st)
	}
	if !st.CLI.AgentResolvesService || hasProblem(&st, "agent_socket_mismatch") {
		t.Fatalf("agent resolution %+v", st.CLI)
	}
	w.env["XDG_RUNTIME_DIR"] = ""
	st, _ = w.ctl().Status(ctx)
	if st.CLI.AgentResolvesService || !hasProblem(&st, "agent_socket_mismatch") {
		t.Fatalf("expected socket mismatch %+v", st.CLI)
	}
}

func TestPinsFollowDefinition(t *testing.T) {
	w := newWorld(t, "linux")
	data := filepath.Join(w.home, "xdgdata")
	w.env["XDG_DATA_HOME"] = data
	w.dataDir = filepath.Join(data, "stage")
	mustOK(t, w, start(t, w))
	b, _ := os.ReadFile(w.unitPath())
	if !strings.Contains(string(b), "Environment=XDG_DATA_HOME="+data) {
		t.Fatalf("not pinned:\n%s", b)
	}
	// A later shell without it still targets the pinned data dir.
	delete(w.env, "XDG_DATA_HOME")
	st, code := w.ctl().Status(ctx)
	if code != CodeOK || st.DataDir != w.dataDir || !hasProblem(&st, "env_mismatch") {
		t.Fatalf("status %d %s %v", code, st.DataDir, st.Problems)
	}
}

func TestLaunchdStart(t *testing.T) {
	w := newWorld(t, "darwin")
	r := start(t, w)
	mustOK(t, w, r)
	plist := filepath.Join(w.dataDir, "io.github.flexdinesh.stage.plist")
	if b, err := os.ReadFile(plist); err != nil || !strings.Contains(string(b), "<string>--managed</string>") {
		t.Fatalf("plist: %v", err)
	}
	if w.called("bootstrap gui/1000 "+plist) != 1 || w.called("enable gui/1000/") != 1 {
		t.Fatalf("calls:\n%s", w.callLog())
	}
	if _, err := os.Stat(filepath.Join(w.home, "Library", "LaunchAgents")); !os.IsNotExist(err) {
		t.Fatal("login copy written without enable")
	}
	if st, err := os.Stat(filepath.Join(w.home, "Library", "Logs", "stage")); err != nil || !st.IsDir() {
		t.Fatalf("log dir: %v", err)
	}
	mustOK(t, w, start(t, w))
	if w.called("bootstrap") != 1 || w.called("kickstart") != 0 {
		t.Fatalf("second start:\n%s", w.callLog())
	}
	r = w.ctl().Restart(ctx, StartOptions{Wait: 2 * time.Second})
	mustOK(t, w, r)
	if w.called("bootout") != 1 || w.called("bootstrap") != 2 {
		t.Fatalf("restart:\n%s", w.callLog())
	}
	mustOK(t, w, w.ctl().Stop(ctx, time.Second))
	if w.lloaded || w.srv != nil {
		t.Fatal("still loaded after stop")
	}
}

func TestLaunchdBootstrapErrorButLoaded(t *testing.T) {
	w := newWorld(t, "darwin")
	w.bootstrapCode, w.bootstrapLoad = 5, true
	mustOK(t, w, start(t, w))
	w2 := newWorld(t, "darwin")
	w2.bootstrapCode = 5
	if r := start(t, w2); r.Code != CodeFailed || !strings.Contains(r.Message, "exit 5") {
		t.Fatalf("real failure: %d %q", r.Code, r.Message)
	}
}

func TestLaunchdLoginCopy(t *testing.T) {
	w := newWorld(t, "darwin")
	mustOK(t, w, w.ctl().Enable(ctx, ""))
	cp := filepath.Join(w.home, "Library", "LaunchAgents", "io.github.flexdinesh.stage.plist")
	if b, err := os.ReadFile(cp); err != nil || !strings.Contains(string(b), w.exe) {
		t.Fatalf("login copy: %v", err)
	}
	if w.lloaded {
		t.Fatal("enable bootstrapped the copy")
	}
	// A definition rewrite (new exe) refreshes the copy.
	other := filepath.Join(filepath.Dir(w.exe), "stage2")
	_ = os.WriteFile(other, []byte("#!/bin/sh\n"), 0o755)
	mustOK(t, w, w.ctl().Start(ctx, StartOptions{Exe: other, Wait: 2 * time.Second}))
	if b, _ := os.ReadFile(cp); !strings.Contains(string(b), other) {
		t.Fatal("login copy not refreshed")
	}
	mustOK(t, w, w.ctl().Disable(ctx))
	if _, err := os.Stat(cp); !os.IsNotExist(err) || w.srv == nil {
		t.Fatal("disable: copy kept or service stopped")
	}
}

func TestLaunchdChangedPlistReloads(t *testing.T) {
	w := newWorld(t, "darwin")
	mustOK(t, w, start(t, w))
	// Crashed (loaded, not running), then the definition changes.
	w.mu.Lock()
	w.kill()
	w.lstate, w.llastExit = "not running", "1"
	w.mu.Unlock()
	other := filepath.Join(filepath.Dir(w.exe), "stage2")
	_ = os.WriteFile(other, []byte("#!/bin/sh\n"), 0o755)
	mustOK(t, w, w.ctl().Start(ctx, StartOptions{Exe: other, Wait: 2 * time.Second}))
	if w.called("bootout") != 1 || w.called("bootstrap") != 2 {
		t.Fatalf("calls:\n%s", w.callLog())
	}
}

// A unit with our name loaded from a file `stage service` did not write
// (packaged, hand-written) is never started, stopped, disabled or removed.
func TestForeignLoadedUnitLeftAlone(t *testing.T) {
	w := newWorld(t, "linux")
	w.fragment = "/etc/systemd/user/stage.service"
	w.mu.Lock()
	w.run()
	w.enabled = true
	w.mu.Unlock()
	for name, r := range map[string]Report{
		"stop":      w.ctl().Stop(ctx, time.Second),
		"uninstall": w.ctl().Uninstall(ctx, time.Second),
		"disable":   w.ctl().Disable(ctx),
		"start":     start(t, w),
	} {
		if r.Code != CodeInvalid || !strings.Contains(r.Message, "did not write") {
			t.Fatalf("%s: %d %q", name, r.Code, r.Message)
		}
	}
	if w.called("stop --no-block") != 0 || w.called("disable") != 0 || w.called("start --no-block") != 0 || !w.enabled || w.srv == nil {
		t.Fatalf("touched the foreign unit:\n%s", w.callLog())
	}
	if _, err := os.Stat(w.unitPath()); !os.IsNotExist(err) {
		t.Fatal("wrote a unit shadowing the foreign one")
	}
	st, _ := w.ctl().Status(ctx)
	if !hasProblem(&st, "definition_shadowed") {
		t.Fatalf("status problems %v", st.Problems)
	}
}

// A crash loop (waiting to auto-restart) is failed, not starting.
func TestStatusCrashLoopIsFailed(t *testing.T) {
	w := newWorld(t, "linux")
	mustOK(t, w, start(t, w))
	w.mu.Lock()
	w.kill()
	w.active, w.sub, w.restarts, w.exitCode, w.exitStatus = "activating", "auto-restart", 3, "1", 1
	w.mu.Unlock()
	if st, code := w.ctl().Status(ctx); st.State != "failed" || code != CodeFailed {
		t.Fatalf("crash loop: %s %d", st.State, code)
	}
}

// A command that waited for the lock sees the definition as it is now: a
// start queued behind an uninstall registers the service again.
func TestDefinitionReadUnderLock(t *testing.T) {
	w := newWorld(t, "linux")
	mustOK(t, w, start(t, w))
	c := w.ctl()
	if err := c.prepare(ctx); err != nil || !c.defExists {
		t.Fatalf("prepare: %v %v", err, c.defExists)
	}
	mustOK(t, w, w.ctl().Uninstall(ctx, time.Second))
	r := c.Start(ctx, StartOptions{Wait: 2 * time.Second})
	mustOK(t, w, r)
	if !r.Changes.DefinitionWritten {
		t.Fatalf("stale definition used: %+v", r.Changes)
	}
}

// macOS: a managed server that lost the data dir to a foreground server
// exits 0 (launchd would respawn 75); start still reports a conflict.
func TestLaunchdExitZeroConflict(t *testing.T) {
	w := newWorld(t, "darwin")
	var fg *instance.Owner
	w.onStart = func(w *world) {
		o, err := instance.Lock(w.dataDir)
		if err != nil {
			w.t.Errorf("lock: %v", err)
			return
		}
		_ = o.Record(instance.NewMeta())
		fg = o
		w.lstate, w.llastExit, w.lruns = "not running", "0", w.lruns+1
	}
	defer func() {
		if fg != nil {
			fg.Release()
		}
	}()
	if r := start(t, w); r.Code != CodeConflict {
		t.Fatalf("got %d %q", r.Code, r.Message)
	}
}
