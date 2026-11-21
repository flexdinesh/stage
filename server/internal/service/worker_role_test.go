package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/worker"
)

func newWorkerWorld(t *testing.T, goos string) *world {
	t.Helper()
	w := newWorld(t, goos)
	w.role = RoleWorker
	return w
}

func TestWorkerStartFresh(t *testing.T) {
	w := newWorkerWorld(t, "linux")
	w.join()
	r := start(t, w)
	mustOK(t, w, r)
	unit, err := os.ReadFile(w.unitPath())
	if err != nil || !strings.Contains(string(unit), "ExecStart="+w.exe+" worker run --managed") || !strings.HasSuffix(w.unitPath(), "stage-worker.service") {
		t.Fatalf("unit %s: %s %v", w.unitPath(), unit, err)
	}
	envPath := filepath.Join(w.home, ".config", "stage", "worker.env")
	if st, err := os.Stat(envPath); err != nil || st.Mode().Perm() != 0o600 || !r.Changes.ServiceEnvCreated {
		t.Fatalf("worker.env: %v %v %+v", st, err, r.Changes)
	}
	if _, err := os.Stat(filepath.Join(w.home, ".config", "stage", "service.env")); !os.IsNotExist(err) {
		t.Fatalf("worker start created service.env: %v", err)
	}
	if !strings.Contains(r.Message, "stage worker") || !strings.Contains(r.Message, "ready") {
		t.Fatalf("message %q", r.Message)
	}
	st := r.Status
	if st == nil || !st.Ready || st.Role != "worker" || st.Worker == nil || !st.Worker.Joined || !st.Worker.OwnedByService || st.Worker.MachineID != 2 || st.Harnesses != nil {
		t.Fatalf("status %+v worker %+v", st, st.Worker)
	}
	// Status and stop go through the same unit.
	got, code := w.ctl().Status(ctx)
	if code != CodeOK || !got.Ready {
		t.Fatalf("status %d %+v", code, got)
	}
	r = w.ctl().Stop(ctx, 2*time.Second)
	mustOK(t, w, r)
	if r.Message != "stopped" {
		t.Fatalf("stop %q", r.Message)
	}
	r = w.ctl().Uninstall(ctx, 2*time.Second)
	mustOK(t, w, r)
	if !strings.Contains(r.Message, "worker config") || !r.Changes.DefinitionRemoved {
		t.Fatalf("uninstall %+v", r)
	}
	if _, err := os.Stat(filepath.Join(w.workerDir(), worker.ConfigFile)); err != nil {
		t.Fatalf("uninstall removed the worker config: %v", err)
	}
}

func TestWorkerStartNotJoined(t *testing.T) {
	w := newWorkerWorld(t, "linux")
	r := start(t, w)
	if r.Code != CodeInvalid || !strings.Contains(r.Fix, "stage worker join") || w.called("start --no-block") != 0 {
		t.Fatalf("got %d %q %q\n%s", r.Code, r.Message, r.Fix, w.callLog())
	}
	if r.Status == nil || !hasProblem(r.Status, "worker_not_joined") {
		t.Fatalf("status %+v", r.Status)
	}
}

func TestWorkerEnvNamesConfig(t *testing.T) {
	w := newWorkerWorld(t, "linux")
	other := filepath.Join(w.home, "elsewhere", worker.ConfigFile)
	if err := worker.SaveConfig(other, worker.Config{Schema: worker.ConfigSchema, URL: "https://m.example", MachineID: 5, Token: "t"}); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(w.home, ".config", "stage", "worker.env")
	_ = os.MkdirAll(filepath.Dir(envPath), 0o700)
	if err := os.WriteFile(envPath, []byte("STAGE_WORKER_CONFIG="+other+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, _ := w.ctl().Status(ctx)
	if st.Worker == nil || st.Worker.Config != other || st.Worker.MachineID != 5 {
		t.Fatalf("worker %+v", st.Worker)
	}
	if err := os.WriteFile(envPath, []byte("STAGE_LISTEN=unix\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := start(t, w); r.Code != CodeInvalid || !strings.Contains(r.Message, "unknown key") {
		t.Fatalf("invalid worker.env: %d %q", r.Code, r.Message)
	}
}

func TestRoleConflict(t *testing.T) {
	// A server service blocks the worker service...
	w := newWorld(t, "linux")
	mustOK(t, w, start(t, w))
	w.role = RoleWorker
	w.join()
	r := start(t, w)
	if r.Code != CodeInvalid || !strings.Contains(r.Message, "server service is installed") || !strings.Contains(r.Fix, "stage service uninstall") {
		t.Fatalf("worker over server: %d %q %q", r.Code, r.Message, r.Fix)
	}
	if r := w.ctl().Enable(ctx, ""); r.Code != CodeInvalid {
		t.Fatalf("enable worker over server: %d %q", r.Code, r.Message)
	}
	// ...and the other way round.
	v := newWorkerWorld(t, "linux")
	v.join()
	mustOK(t, v, start(t, v))
	v.role = RoleServer
	r = start(t, v)
	if r.Code != CodeInvalid || !strings.Contains(r.Message, "worker service is installed") || !strings.Contains(r.Fix, "stage worker service uninstall") {
		t.Fatalf("server over worker: %d %q %q", r.Code, r.Message, r.Fix)
	}
}

func TestWorkerForegroundHolder(t *testing.T) {
	w := newWorkerWorld(t, "linux")
	w.join()
	l := w.lockWorkerDir(31337, false)
	defer l.Release()
	r := start(t, w)
	if r.Code != CodeConflict || !strings.Contains(r.Message, "in use by another stage worker") || w.called("start --no-block") != 0 {
		t.Fatalf("got %d %q\n%s", r.Code, r.Message, w.callLog())
	}
	if r.Status == nil || !hasProblem(r.Status, "foreground_holder") {
		t.Fatalf("status %+v", r.Status)
	}
}

func TestWorkerExit78IsInvalid(t *testing.T) {
	w := newWorkerWorld(t, "linux")
	w.join()
	w.onStart = func(w *world) { w.active, w.result, w.exitCode, w.exitStatus = "failed", "exit-code", "1", 78 }
	r := start(t, w)
	if r.Code != CodeInvalid || !strings.Contains(r.Message, "worker refused its configuration") {
		t.Fatalf("got %d %q", r.Code, r.Message)
	}
}

func TestWorkerOfflineIsANote(t *testing.T) {
	w := newWorkerWorld(t, "linux")
	w.join()
	w.workerState = worker.StateConnecting
	r := start(t, w)
	mustOK(t, w, r)
	if !strings.Contains(strings.Join(r.Notes, "\n"), "channel is connecting") {
		t.Fatalf("notes %v", r.Notes)
	}
	if r.Status == nil || !r.Status.Ready || !hasProblem(r.Status, "worker_offline") {
		t.Fatalf("status %+v", r.Status)
	}
}

func TestWorkerRestartNewProcess(t *testing.T) {
	w := newWorkerWorld(t, "linux")
	w.join()
	mustOK(t, w, start(t, w))
	pid := w.srv.pid
	time.Sleep(2 * time.Millisecond) // a distinct start time
	r := w.ctl().Restart(ctx, StartOptions{Wait: 2 * time.Second})
	mustOK(t, w, r)
	if w.srv.pid == pid || !r.Changes.Started {
		t.Fatalf("restart kept pid %d: %+v", pid, r.Changes)
	}
}

func TestWorkerLaunchd(t *testing.T) {
	w := newWorkerWorld(t, "darwin")
	w.join()
	r := start(t, w)
	mustOK(t, w, r)
	plist, err := os.ReadFile(filepath.Join(w.dataDir, LabelFor(RoleWorker)+".plist"))
	if err != nil || !strings.Contains(string(plist), "<string>worker</string>") || !strings.Contains(string(plist), "worker.launchd.stderr.log") {
		t.Fatalf("plist %s %v", plist, err)
	}
}
