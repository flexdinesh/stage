//go:build smoke

// The real user-service smoke check (`mise run service:smoke`): builds a
// binary with the isolated test identity stage-svctest and drives the real
// systemd user manager with it. It never touches the stable identity's
// unit, socket or data: its data and config live under .scratch/svctest
// (pinned into the test unit); the only file it writes in $HOME is the
// marked unit ~/.config/systemd/user/stage-svctest.service, removed after.
package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/instance"
	"github.com/flexdinesh/stage/server/internal/service"
)

const smokeID = "stage-svctest"

type smoke struct {
	t       *testing.T
	bin     string
	dir     string // .scratch/svctest
	env     []string
	unit    string // the unit file in the user's unit dir
	wunit   string // the worker unit file
	short   string // short temp dir for foreground sockets
	dataDir string
}

func TestSmoke(t *testing.T) {
	if os.Getenv("STAGE_SERVICE_SMOKE") != "1" {
		t.Skip("set STAGE_SERVICE_SMOKE=1 (mise run service:smoke)")
	}
	if runtime.GOOS != "linux" {
		t.Skip("the smoke check drives systemd --user; macOS is not covered yet")
	}
	s := newSmoke(t)
	s.cleanup()
	stopSig := make(chan os.Signal, 1)
	signal.Notify(stopSig, os.Interrupt, syscall.SIGTERM)
	go func() {
		if _, ok := <-stopSig; ok {
			s.cleanup()
			os.Exit(1)
		}
	}()
	defer func() { signal.Stop(stopSig); close(stopSig); s.cleanup() }()

	// service.env: no real T3 (nothing registers with it).
	cfg := filepath.Join(s.dir, "config", smokeID)
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "service.env"), []byte("STAGE_LISTEN=unix\nT3_URL=http://127.0.0.1:9\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Log("concurrent starts give one server")
	var wg sync.WaitGroup
	reps := make([]service.Report, 2)
	for i := range reps {
		wg.Add(1)
		go func() { defer wg.Done(); reps[i] = s.report("start", "--json") }()
	}
	wg.Wait()
	for _, r := range reps {
		if !r.OK || r.Status == nil || !r.Status.Ready {
			t.Fatalf("start: %+v", r)
		}
	}
	if reps[0].Status.Process.PID != reps[1].Status.Process.PID || reps[0].Status.Stage.InstanceID != reps[1].Status.Stage.InstanceID {
		t.Fatalf("two servers: %d %d", reps[0].Status.Process.PID, reps[1].Status.Process.PID)
	}
	if r := s.report("start", "--json"); !r.OK || r.Changes.Started || r.Changes.DefinitionWritten {
		t.Fatalf("third start changed things: %+v", r.Changes)
	}
	s.verifyUnit()

	t.Log("crash restart")
	st := s.status()
	pid := st.Process.PID
	if exe, _ := os.Readlink("/proc/" + itoa(pid) + "/exe"); exe != s.bin {
		t.Fatalf("pid %d is %q, not our test binary", pid, exe)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	st = s.waitStatus(20*time.Second, func(st service.Status) bool { return st.Ready && st.Process.PID != pid })
	if st.Process.Restarts < 1 {
		t.Fatalf("restarts %d after crash", st.Process.Restarts)
	}

	t.Log("managed and foreground cannot double-own")
	if code, out := s.run("serve", "--listen", "unix:"+filepath.Join(s.short, "a.sock")); code != 75 || !strings.Contains(out, "stage service stop") {
		t.Fatalf("foreground vs service: %d %s", code, out)
	}
	if code, _ := s.run("serve", "--managed"); code != 75 {
		t.Fatalf("second managed: %d", code)
	}

	t.Log("restart gives a new instance")
	old := st.Stage.InstanceID
	if r := s.report("restart", "--json"); !r.OK || r.Status.Stage.InstanceID == old {
		t.Fatalf("restart: %+v", r)
	}

	t.Log("stop twice")
	if r := s.report("stop", "--json"); !r.OK || r.Message != "stopped" {
		t.Fatalf("stop: %+v", r)
	}
	if r := s.report("stop", "--json"); !r.OK || r.Message != "not running" {
		t.Fatalf("second stop: %+v", r)
	}
	st = s.status()
	if st.Lock.Held || st.Socket.Exists || st.State != "stopped" {
		t.Fatalf("after stop: %+v", st)
	}

	t.Log("foreground holder: service start refuses; a direct start does not loop")
	fg := exec.Command(s.bin, "serve", "--listen", "unix:"+filepath.Join(s.short, "fg.sock"))
	fg.Env = s.env
	if err := fg.Start(); err != nil {
		t.Fatal(err)
	}
	fgDone := make(chan struct{})
	go func() { _ = fg.Wait(); close(fgDone) }()
	defer func() { _ = fg.Process.Kill(); <-fgDone }()
	s.waitFor(10*time.Second, "foreground holder", func() bool {
		_, h, _ := instance.ReadHolder(s.dataDir)
		return h != nil && h.PID == fg.Process.Pid
	})
	if r := s.report("start", "--json"); r.Code != service.CodeConflict || !strings.Contains(r.Message, "pid "+itoa(fg.Process.Pid)) {
		t.Fatalf("start with foreground holder: %+v", r)
	}
	s.systemctl("start", smokeID+".service")
	s.waitFor(10*time.Second, "unit failed with 75", func() bool {
		p := s.show("ActiveState,ExecMainStatus")
		return p["ActiveState"] == "failed" && p["ExecMainStatus"] == "75"
	})
	time.Sleep(3 * time.Second)
	if p := s.show("ActiveState,NRestarts"); p["ActiveState"] != "failed" || p["NRestarts"] != "0" {
		t.Fatalf("restarted on 75: %v", p)
	}
	_ = fg.Process.Signal(syscall.SIGTERM)
	<-fgDone
	if r := s.report("start", "--json"); !r.OK {
		t.Fatalf("start after foreground exit: %+v", r)
	}

	t.Log("login startup is separate from running state")
	if r := s.report("enable", "--json"); !r.OK || s.show("UnitFileState")["UnitFileState"] != "enabled" {
		t.Fatalf("enable: %+v", r)
	}
	wants := filepath.Join(filepath.Dir(s.unit), "default.target.wants", smokeID+".service")
	if _, err := os.Lstat(wants); err != nil {
		t.Fatalf("wants link: %v", err)
	}
	if r := s.report("disable", "--json"); !r.OK || s.show("UnitFileState")["UnitFileState"] != "disabled" {
		t.Fatalf("disable: %+v", r)
	}
	if _, err := os.Lstat(wants); !os.IsNotExist(err) {
		t.Fatal("wants link kept")
	}
	if st := s.status(); !st.Ready {
		t.Fatalf("enable/disable changed running state: %+v", st)
	}

	t.Log("logs")
	if code, out := s.run("service", "logs", "-n", "5"); code != 0 || strings.TrimSpace(out) == "" {
		t.Fatalf("logs: %d %q", code, out)
	}

	t.Log("worker: join a foreground main server")
	m := s.startMain("")
	defer func() { m.stop() }()
	code, out := s.runEnv([]string{"STAGE_URL=" + m.url}, "machine", "pair", "--label", "smoke", "--json")
	var pair struct {
		Link string `json:"link"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &pair) != nil || pair.Link == "" {
		t.Fatalf("pair: %d %s", code, out)
	}
	if code, out := s.run("worker", "join", pair.Link); code != 0 {
		t.Fatalf("worker join: %d %s", code, out)
	}

	t.Log("worker: refused while the server service is installed")
	if r := s.workerReport("start", "--json"); r.OK || r.Code != service.CodeInvalid || !strings.Contains(r.Message, "not both") {
		t.Fatalf("worker start beside the server service: %+v", r)
	}

	t.Log("uninstall keeps data and config")
	if r := s.report("uninstall", "--json"); !r.OK || !r.Changes.DefinitionRemoved {
		t.Fatalf("uninstall: %+v", r)
	}
	if code, _ := s.run("service", "status"); code != service.CodeNotRunning {
		t.Fatalf("status after uninstall: %d", code)
	}
	if p := s.show("LoadState"); p["LoadState"] != "not-found" {
		t.Fatalf("unit still loaded: %v", p)
	}
	for _, f := range []string{filepath.Join(s.dataDir, "stage.db"), filepath.Join(cfg, "service.env")} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("%s removed: %v", f, err)
		}
	}

	t.Log("worker service: start, online")
	r := s.workerReport("start", "--json")
	if !r.OK || r.Status == nil || !r.Status.Ready || r.Status.Worker == nil || !r.Status.Worker.OwnedByService {
		t.Fatalf("worker start: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(cfg, "worker.env")); err != nil {
		t.Fatalf("worker.env: %v", err)
	}
	if r := s.report("start", "--json"); r.OK || r.Code != service.CodeInvalid {
		t.Fatalf("server start beside the worker service: %+v", r)
	}
	s.verifyUnitFile(s.wunit)
	s.waitWorkerOnline()

	t.Log("worker service: crash restart")
	wpid := r.Status.Process.PID
	if exe, _ := os.Readlink("/proc/" + itoa(wpid) + "/exe"); exe != s.bin {
		t.Fatalf("pid %d is %q, not our test binary", wpid, exe)
	}
	if err := syscall.Kill(wpid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	ws := s.waitWorkerStatus(20*time.Second, func(st service.Status) bool { return st.Ready && st.Process.PID != wpid })
	if ws.Process.Restarts < 1 {
		t.Fatalf("worker restarts %d after crash", ws.Process.Restarts)
	}
	s.waitWorkerOnline()

	t.Log("worker service: a foreground worker cannot double-own")
	if code, out := s.run("worker", "run"); code != 75 || !strings.Contains(out, "worker service stop") {
		t.Fatalf("foreground worker vs service: %d %s", code, out)
	}

	t.Log("worker service: survives a main restart")
	m.stop()
	s.waitFor(30*time.Second, "worker offline", func() bool { c, _ := s.run("worker", "status"); return c == 1 })
	m = s.startMain(strings.TrimPrefix(m.url, "http://"))
	s.waitWorkerOnline()

	t.Log("worker service: logs, stop, uninstall")
	if code, out := s.run("worker", "service", "logs", "-n", "5"); code != 0 || strings.TrimSpace(out) == "" {
		t.Fatalf("worker logs: %d %q", code, out)
	}
	if r := s.workerReport("stop", "--json"); !r.OK || r.Message != "stopped" {
		t.Fatalf("worker stop: %+v", r)
	}
	if r := s.workerReport("uninstall", "--json"); !r.OK || !r.Changes.DefinitionRemoved {
		t.Fatalf("worker uninstall: %+v", r)
	}
	if _, err := os.Stat(s.wunit); !os.IsNotExist(err) {
		t.Fatalf("worker unit kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.dataDir, "worker", "worker.json")); err != nil {
		t.Fatalf("worker config removed: %v", err)
	}
}

// mainServer is a foreground main server of the test binary on its own
// data dir (never the service's) and a loopback port.
type mainServer struct {
	s    *smoke
	url  string
	cmd  *exec.Cmd
	done chan struct{}
	env  []string
	args []string
}

// startMain on addr ("": a free loopback port).
func (s *smoke) startMain(addr string) *mainServer {
	s.t.Helper()
	if addr == "" {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			s.t.Fatal(err)
		}
		addr = l.Addr().String()
		_ = l.Close()
	}
	m := &mainServer{s: s, url: "http://" + addr,
		env:  append(slices.Clone(s.env), "STAGE_DATA_DIR="+filepath.Join(s.dir, "data", "main"), "STAGE_PUBLIC_URL=http://"+addr, "T3_URL=http://127.0.0.1:9"),
		args: []string{"serve", "--listen", "unix:" + filepath.Join(s.short, "main.sock") + "," + addr}}
	m.cmd = exec.Command(s.bin, m.args...)
	m.cmd.Env = m.env
	if err := m.cmd.Start(); err != nil {
		s.t.Fatal(err)
	}
	m.done = make(chan struct{})
	go func() { _ = m.cmd.Wait(); close(m.done) }()
	s.waitFor(30*time.Second, "main server health", func() bool {
		res, err := http.Get(m.url + "/api/health")
		if err != nil {
			return false
		}
		_ = res.Body.Close()
		return res.StatusCode == 200
	})
	return m
}

func (m *mainServer) stop() {
	select {
	case <-m.done:
		return
	default:
	}
	_ = m.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-m.done:
	case <-time.After(30 * time.Second):
		_ = m.cmd.Process.Kill()
		<-m.done
	}
}

func (s *smoke) waitWorkerOnline() {
	s.t.Helper()
	s.waitFor(30*time.Second, "worker online", func() bool { c, _ := s.run("worker", "status"); return c == 0 })
}

func (s *smoke) workerReport(args ...string) service.Report {
	return s.report(append([]string{"--worker"}, args...)...)
}

func (s *smoke) workerStatus() service.Status {
	s.t.Helper()
	cmd := exec.Command(s.bin, "worker", "service", "status", "--json")
	cmd.Env = s.env
	out, _ := cmd.Output()
	var st service.Status
	if err := json.Unmarshal(out, &st); err != nil {
		s.t.Fatalf("worker status: %v\n%s", err, out)
	}
	return st
}

func (s *smoke) waitWorkerStatus(d time.Duration, ok func(service.Status) bool) service.Status {
	s.t.Helper()
	deadline := time.Now().Add(d)
	for {
		st := s.workerStatus()
		if ok(st) {
			return st
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("worker status never matched: %+v", st)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func newSmoke(t *testing.T) *smoke {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".scratch", "svctest")
	s := &smoke{t: t, dir: dir, bin: filepath.Join(dir, "bin", smokeID)}
	build := exec.Command("go", "build", "-ldflags", "-X github.com/flexdinesh/stage/server/internal/buildinfo.Identity="+smokeID, "-o", s.bin, "./cmd/stage")
	build.Dir = filepath.Join(root, "server")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "STAGE_") || strings.HasPrefix(k, "T3_") || k == "XDG_DATA_HOME" || k == "XDG_CONFIG_HOME" || k == "NOTIFY_SOCKET" {
			continue
		}
		s.env = append(s.env, kv)
	}
	s.env = append(s.env, "XDG_DATA_HOME="+filepath.Join(dir, "data"), "XDG_CONFIG_HOME="+filepath.Join(dir, "config"))
	s.dataDir = filepath.Join(dir, "data", smokeID)
	if s.short, err = os.MkdirTemp("", "smk"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(s.short) })

	var bi service.BuildInfo
	code, out := s.run("version", "--json")
	if code != 0 || json.Unmarshal([]byte(out), &bi) != nil || bi.Identity != smokeID || bi.ServiceUnit != smokeID+".service" {
		t.Fatalf("test binary identity: %d %s", code, out)
	}
	home, _ := os.UserHomeDir()
	cfgHome := filepath.Join(home, ".config")
	if p := s.showEnv("XDG_CONFIG_HOME"); filepath.IsAbs(p) {
		cfgHome = p
	}
	s.unit = filepath.Join(cfgHome, "systemd", "user", smokeID+".service")
	s.wunit = filepath.Join(cfgHome, "systemd", "user", smokeID+"-worker.service")
	for _, u := range []string{s.unit, s.wunit} {
		if b, err := os.ReadFile(u); err == nil && !bytes.Contains(b, []byte("Generator="+service.Generator)) {
			t.Fatalf("%s exists and is not ours; refusing to touch it", u)
		}
	}
	return s
}

// cleanup removes everything the smoke check may have created, by exact path.
func (s *smoke) cleanup() {
	_, _ = s.run("worker", "service", "uninstall", "--wait", "2m")
	_, _ = s.run("service", "uninstall", "--wait", "2m")
	for _, u := range []string{s.unit, s.wunit} {
		name := filepath.Base(u)
		if b, err := os.ReadFile(u); err == nil && bytes.Contains(b, []byte("Generator="+service.Generator)) {
			s.systemctl("stop", name)
			s.systemctl("disable", name)
			_ = os.Remove(u)
			s.systemctl("daemon-reload")
		}
		s.systemctl("reset-failed", name)
	}
	_ = os.RemoveAll(filepath.Join(s.dir, "data"))
	_ = os.RemoveAll(filepath.Join(s.dir, "config"))
	if rt := os.Getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(rt) {
		_ = os.RemoveAll(filepath.Join(rt, smokeID))
	}
}

func (s *smoke) run(args ...string) (int, string) { return s.runEnv(nil, args...) }

func (s *smoke) runEnv(extra []string, args ...string) (int, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.bin, args...)
	cmd.Env = append(slices.Clone(s.env), extra...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), out.String()
	}
	if err != nil {
		s.t.Fatalf("%v: %v", args, err)
	}
	return 0, out.String()
}

func (s *smoke) report(args ...string) service.Report {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	prefix := []string{"service"}
	if len(args) > 0 && args[0] == "--worker" {
		prefix, args = []string{"worker", "service"}, args[1:]
	}
	cmd := exec.CommandContext(ctx, s.bin, append(prefix, args...)...)
	cmd.Env = s.env
	out, _ := cmd.Output()
	var r service.Report
	if err := json.Unmarshal(out, &r); err != nil {
		s.t.Fatalf("service %v: %v\n%s", args, err, out)
	}
	return r
}

func (s *smoke) status() service.Status {
	s.t.Helper()
	cmd := exec.Command(s.bin, "service", "status", "--json")
	cmd.Env = s.env
	out, _ := cmd.Output()
	var st service.Status
	if err := json.Unmarshal(out, &st); err != nil {
		s.t.Fatalf("status: %v\n%s", err, out)
	}
	return st
}

func (s *smoke) waitStatus(d time.Duration, ok func(service.Status) bool) service.Status {
	s.t.Helper()
	deadline := time.Now().Add(d)
	for {
		st := s.status()
		if ok(st) {
			return st
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("status never matched: %+v", st)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func (s *smoke) waitFor(d time.Duration, what string, ok func() bool) {
	s.t.Helper()
	deadline := time.Now().Add(d)
	for !ok() {
		if time.Now().After(deadline) {
			s.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *smoke) systemctl(args ...string) string {
	out, _ := exec.Command("systemctl", append([]string{"--user", "--no-pager"}, args...)...).CombinedOutput()
	return string(out)
}

func (s *smoke) show(props string) map[string]string {
	out := map[string]string{}
	for _, l := range strings.Split(s.systemctl("show", smokeID+".service", "-p", props), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			out[k] = v
		}
	}
	return out
}

func (s *smoke) showEnv(key string) string {
	for _, l := range strings.Split(s.systemctl("show-environment"), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok && k == key {
			return v
		}
	}
	return ""
}

func (s *smoke) verifyUnit() { s.verifyUnitFile(s.unit) }

func (s *smoke) verifyUnitFile(unit string) {
	if _, err := exec.LookPath("systemd-analyze"); err != nil {
		s.t.Log("systemd-analyze not found; unit not verified")
		return
	}
	out, err := exec.Command("systemd-analyze", "--user", "verify", unit).CombinedOutput()
	if err != nil {
		s.t.Fatalf("systemd-analyze verify: %v\n%s", err, out)
	}
	if len(bytes.TrimSpace(out)) > 0 {
		s.t.Logf("systemd-analyze verify output:\n%s", out)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
