//go:build smoke

// The installer smoke check (`mise run install:smoke`), with real
// binaries and the real systemd user manager under the isolated identity
// stage-svctest (as service:smoke): install, update through `stage update
// --from`, a real failed restart that rolls back, and uninstall. Its home,
// data and config live under .scratch/installtest; the only file outside
// is the marked unit ~/.config/systemd/user/stage-svctest.service (and the
// identity's runtime socket dir), removed after. With
// STAGE_INSTALL_SMOKE_DIST set to a `mise run dist` output, it also
// installs that real Stage Dev build into a temp home with no service
// manager (a failing systemctl stub first on PATH), so no real service is
// touched.
package install

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/service"
)

const smokeID = "stage-svctest"

type ismoke struct {
	t    *testing.T
	root string // repo root
	dir  string // .scratch/installtest
	env  []string
	home string
	unit string
}

func TestInstallSmoke(t *testing.T) {
	if os.Getenv("STAGE_INSTALL_SMOKE") != "1" {
		t.Skip("set STAGE_INSTALL_SMOKE=1 (mise run install:smoke)")
	}
	if runtime.GOOS != "linux" {
		t.Skip("drives systemd --user; macOS is not covered")
	}
	s := newISmoke(t)
	s.cleanup()
	defer s.cleanup()

	a1 := s.release("1.0.0")
	a2 := s.release("1.1.0")
	a3 := s.release("1.2.0")
	cfg := filepath.Join(s.dir, "config", smokeID)
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "service.env"), []byte("STAGE_LISTEN=unix\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(s.home, ".local", "bin", smokeID)
	cli := filepath.Join(s.home, ".local", "lib", smokeID, "app", "bin", "stage")

	t.Log("install 1.0.0 headless")
	r := s.result(filepath.Join(a1, "bin"), "install", "--from", a1, "--headless", "--json")
	if !r.OK || r.Service != "not-installed" {
		t.Fatalf("install: %+v", r)
	}
	// `stage service start` from the link records the link (a stable path).
	if rep := s.report(link, "service", "start", "--json"); !rep.OK || rep.Status.Stage.Version != "1.0.0" || rep.Status.Definition.Exe != link {
		t.Fatalf("service start: %+v exe %s", rep, rep.Status.Definition.Exe)
	}
	pid := s.status(link).Process.PID

	t.Log("stage update --from 1.1.0 restarts the service onto it")
	r = s.result(link, "update", "--from", a2, "--json")
	if !r.OK || r.Service != "restarted" || r.Version != "1.1.0" {
		t.Fatalf("update: %+v", r)
	}
	st := s.status(link)
	if !st.Ready || st.Stage.Version != "1.1.0" || st.Process.PID == pid || st.Definition.Exe != cli {
		t.Fatalf("after update: ready %v version %s pid %d (was %d) exe %s", st.Ready, st.Stage.Version, st.Process.PID, pid, st.Definition.Exe)
	}
	if exe, _ := os.Readlink(fmt.Sprintf("/proc/%d/exe", st.Process.PID)); exe != cli {
		t.Fatalf("server runs %s", exe)
	}
	var v struct {
		Version string `json:"version"`
		Install *struct {
			Mode, Version string
		} `json:"install"`
	}
	if code, out := s.run(link, "version", "--json"); code != 0 || json.Unmarshal([]byte(out), &v) != nil || v.Version != "1.1.0" || v.Install == nil || v.Install.Mode != "headless" {
		t.Fatalf("version: %d %s", code, out)
	}
	if r := s.result(link, "update", "--from", a2, "--json"); !r.OK || r.Changed {
		t.Fatalf("reinstall: %+v", r)
	}

	t.Log("a failed restart (socket dir made unsafe) rolls back to 1.1.0")
	sockDir := filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), smokeID)
	if err := os.Chmod(sockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	r = s.result(link, "update", "--from", a3, "--json")
	if err := os.Chmod(sockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if r.Code != CodeRolledBack || r.Service != "rolled-back" {
		t.Fatalf("failed update: %+v", r)
	}
	if code, out := s.run(link, "version"); code != 0 || !strings.Contains(out, "stage 1.1.0") {
		t.Fatalf("not rolled back: %s", out)
	}
	if rep := s.report(link, "service", "restart", "--json"); !rep.OK || rep.Status.Stage.Version != "1.1.0" {
		t.Fatalf("restart after rollback: %+v", rep)
	}

	t.Log("uninstall keeps data")
	r = s.result(link, "uninstall", "--json")
	if !r.OK || r.Service != "removed" {
		t.Fatalf("uninstall: %+v", r)
	}
	for _, p := range []string{link, filepath.Join(s.home, ".local", "lib", smokeID)} {
		if _, err := os.Lstat(p); err == nil {
			t.Fatalf("%s kept", p)
		}
	}
	if _, err := os.Stat(filepath.Join(s.dir, "data", smokeID, "stage.db")); err != nil {
		t.Fatalf("data removed: %v", err)
	}
	if p := s.showLoad(); p != "not-found" {
		t.Fatalf("unit still loaded: %s", p)
	}
}

func newISmoke(t *testing.T) *ismoke {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	s := &ismoke{t: t, root: root, dir: filepath.Join(root, ".scratch", "installtest")}
	s.home = filepath.Join(s.dir, "home")
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "STAGE_") || strings.HasPrefix(k, "T3_") || k == "XDG_DATA_HOME" || k == "XDG_CONFIG_HOME" || k == "NOTIFY_SOCKET" || k == "HOME" {
			continue
		}
		s.env = append(s.env, kv)
	}
	s.env = append(s.env, "HOME="+s.home, "XDG_DATA_HOME="+filepath.Join(s.dir, "data"), "XDG_CONFIG_HOME="+filepath.Join(s.dir, "config"))
	realHome, _ := os.UserHomeDir()
	s.unit = filepath.Join(realHome, ".config", "systemd", "user", smokeID+".service")
	if b, err := os.ReadFile(s.unit); err == nil && !bytes.Contains(b, []byte("Generator="+service.Generator)) {
		t.Fatalf("%s exists and is not ours; refusing to touch it", s.unit)
	}
	return s
}

// release builds a real stage-svctest CLI of version and an asset dir
// holding its headless archive, manifest and checksums. The binary is
// also left at <dir>/bin to bootstrap the first install.
func (s *ismoke) release(version string) string {
	s.t.Helper()
	dir := filepath.Join(s.dir, "releases", version)
	_ = os.RemoveAll(dir)
	bin := filepath.Join(dir, "bin")
	bi := "github.com/flexdinesh/stage/server/internal/buildinfo"
	build := exec.Command("go", "build", "-trimpath", "-ldflags",
		fmt.Sprintf("-X %s.Identity=%s -X %s.Version=%s -X %s.Commit=%s", bi, smokeID, bi, version, bi, testCommit), "-o", bin, "./cmd/stage")
	build.Dir = filepath.Join(s.root, "server")
	if out, err := build.CombinedOutput(); err != nil {
		s.t.Fatalf("build: %v\n%s", err, out)
	}
	data, err := os.ReadFile(bin)
	if err != nil {
		s.t.Fatal(err)
	}
	base := fmt.Sprintf("%s-%s-%s-%s", smokeID, version, runtime.GOOS, runtime.GOARCH)
	writeTarGz(s.t, filepath.Join(dir, base+".tar.gz"), []member{{name: base + "/", typ: '5', mode: 0o755},
		{name: base + "/stage", body: data, mode: 0o755}, {name: base + "/LICENSE", body: []byte("MIT")}})
	sum, size, err := fileSHA256(filepath.Join(dir, base+".tar.gz"))
	if err != nil {
		s.t.Fatal(err)
	}
	m := Manifest{Schema: 1, Name: "stage", Version: version, Commit: testCommit, Identity: smokeID, Channel: "dev",
		Artifacts: []Artifact{{Name: base + ".tar.gz", Kind: "cli", Format: "tar.gz", OS: runtime.GOOS, Arch: runtime.GOARCH, Size: size, SHA256: sum,
			CLI: &Binary{Path: base + "/stage", SHA256: bytesSHA256(data)}}}}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, ManifestName), b, 0o644); err != nil {
		s.t.Fatal(err)
	}
	writeSums(s.t, dir, m)
	return dir
}

func (s *ismoke) run(exe string, args ...string) (int, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Env = s.env
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), out.String()
	}
	if err != nil {
		s.t.Fatalf("%s %v: %v", exe, args, err)
	}
	return 0, out.String()
}

func (s *ismoke) result(exe string, args ...string) Result {
	s.t.Helper()
	_, out := s.run(exe, args...)
	var r Result
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		s.t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return r
}

func (s *ismoke) report(exe string, args ...string) service.Report {
	s.t.Helper()
	_, out := s.run(exe, args...)
	var r service.Report
	if err := json.Unmarshal([]byte(out), &r); err != nil || r.Status == nil {
		s.t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return r
}

func (s *ismoke) status(exe string) service.Status {
	s.t.Helper()
	_, out := s.run(exe, "service", "status", "--json")
	var st service.Status
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		s.t.Fatalf("status: %v\n%s", err, out)
	}
	return st
}

func (s *ismoke) systemctl(args ...string) string {
	out, _ := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	return string(out)
}

func (s *ismoke) showLoad() string {
	return strings.TrimSpace(strings.TrimPrefix(s.systemctl("show", "-p", "LoadState", smokeID+".service"), "LoadState="))
}

// cleanup removes what the smoke check may have created, by exact path.
func (s *ismoke) cleanup() {
	if b, err := os.ReadFile(s.unit); err == nil && bytes.Contains(b, []byte("Generator="+service.Generator)) {
		s.systemctl("stop", smokeID+".service")
		s.systemctl("disable", smokeID+".service")
		_ = os.Remove(s.unit)
		s.systemctl("daemon-reload")
	}
	s.systemctl("reset-failed", smokeID+".service")
	for _, d := range []string{"home", "data", "config"} {
		_ = os.RemoveAll(filepath.Join(s.dir, d))
	}
	if rt := os.Getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(rt) {
		_ = os.RemoveAll(filepath.Join(rt, smokeID))
	}
}

// TestInstallSmokeDist installs a real `mise run dist` Stage Dev build
// (desktop, else headless without WebKit) into a temp home with no
// service manager, reinstalls it and uninstalls it.
func TestInstallSmokeDist(t *testing.T) {
	dist := os.Getenv("STAGE_INSTALL_SMOKE_DIST")
	if dist == "" {
		t.Skip("set STAGE_INSTALL_SMOKE_DIST to a mise run dist output (mise run install:smoke)")
	}
	m, err := readManifest(dist)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	stubs := t.TempDir()
	if err := os.WriteFile(filepath.Join(stubs, "systemctl"), []byte("#!/bin/sh\necho 'Failed to connect to bus: No medium found' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "STAGE_") || strings.HasPrefix(k, "T3_") || strings.HasPrefix(k, "XDG_") || k == "HOME" || k == "PATH" || k == "DISPLAY" || k == "WAYLAND_DISPLAY" {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "HOME="+home, "PATH="+stubs+":"+os.Getenv("PATH"), "XDG_DATA_HOME="+filepath.Join(home, "share"), "XDG_CONFIG_HOME="+filepath.Join(home, "config"))
	base := fmt.Sprintf("%s-%s-%s-%s", m.Identity, m.Version, runtime.GOOS, runtime.GOARCH)
	boot := filepath.Join(t.TempDir(), "stage")
	if err := extractFile(filepath.Join(dist, base+".tar.gz"), base+"/stage", boot); err != nil {
		t.Fatal(err)
	}
	run := func(exe string, args ...string) (int, Result) {
		cmd := exec.Command(exe, append(args, "--json")...)
		cmd.Env = env
		out, err := cmd.Output()
		code := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		var r Result
		if err := json.Unmarshal(out, &r); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return code, r
	}
	mode := "--desktop"
	code, r := run(boot, "install", "--from", dist, mode)
	if code == CodeUnsupported && strings.Contains(r.Message, "WebKitGTK") {
		t.Logf("desktop not installable here (%s); installing headless", r.Message)
		mode = "--headless"
		code, r = run(boot, "install", "--from", dist, mode)
	}
	if code != 0 || !r.OK || r.Service != "unavailable" {
		t.Fatalf("install %s: %d %+v", mode, code, r)
	}
	link := filepath.Join(home, ".local", "bin", m.Identity)
	if target, _ := os.Readlink(link); target != r.CLI {
		t.Fatalf("link -> %s, want %s", target, r.CLI)
	}
	if mode == "--desktop" {
		entry := filepath.Join(home, "share", "applications", m.Identity+"-desktop.desktop")
		b, err := os.ReadFile(entry)
		if err != nil || !bytes.Contains(b, []byte("Exec="+filepath.Join(home, ".local", "lib", m.Identity, "app", m.Identity+"-desktop"))) ||
			!bytes.Contains(b, []byte("x-scheme-handler/"+m.Identity)) {
			t.Fatalf("entry %s: %v\n%s", entry, err, b)
		}
		t.Logf("desktop entry:\n%s", b)
	}
	if code, r := run(link, "install", "--from", dist); code != 0 || r.Changed {
		t.Fatalf("reinstall: %d %+v", code, r)
	}
	if code, r := run(link, "uninstall"); code != 0 || !r.OK {
		t.Fatalf("uninstall: %d %+v", code, r)
	}
	if _, err := os.Lstat(filepath.Join(home, ".local", "lib", m.Identity)); err == nil {
		t.Fatal("install root kept")
	}
}
