package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/instance"
)

// helperArgs runs main() with these args (tab-separated) in a subprocess.
const helperArgs = "STAGE_MAIN_HELPER_ARGS"

func TestMain(m *testing.M) {
	if a, ok := os.LookupEnv(helperArgs); ok {
		os.Args = append([]string{"stage"}, strings.Split(a, "\t")...)
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestExitCode(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{nil, 0},
		{errUsage{"x"}, 2},
		{&instance.LockedError{DataDir: "/d"}, 75},
		{config.Invalid(errors.New("bad")), 78},
		{errors.New("bind: address in use"), 1},
	}
	for _, c := range cases {
		if got := exitCode(c.err); got != c.want {
			t.Fatalf("exitCode(%v) = %d, want %d", c.err, got, c.want)
		}
	}
}

// env is an isolated environment: never the user's data, config or runtime dir.
func env(t *testing.T) (string, []string) {
	t.Helper()
	short, err := os.MkdirTemp("", "mn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	rt := filepath.Join(short, "rt")
	_ = os.Mkdir(rt, 0o700)
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "STAGE_") || strings.HasPrefix(k, "T3_") || strings.HasPrefix(k, "XDG_") || k == "HOME" || k == "NOTIFY_SOCKET" {
			continue
		}
		out = append(out, kv)
	}
	return short, append(out, "HOME="+filepath.Join(short, "h"), "XDG_RUNTIME_DIR="+rt, "T3_FAKE=1")
}

func runMain(t *testing.T, environ []string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(environ, helperArgs+"="+strings.Join(args, "\t"))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), stderr.String()
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0, stderr.String()
}

func TestServeExitCodes(t *testing.T) {
	short, environ := env(t)
	data := filepath.Join(short, "data")
	o, err := instance.Lock(data)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Release()
	m := instance.NewMeta()
	m.Managed, m.Manager, m.Service = true, "systemd", "stage.service"
	_ = o.Record(m)

	// Foreground serve on a held data dir: 75, naming the service.
	code, stderr := runMain(t, append(environ, "STAGE_DATA_DIR="+data), "serve", "--listen", "unix:"+filepath.Join(short, "a.sock"))
	if code != 75 || !strings.Contains(stderr, "stage service stop") {
		t.Fatalf("held: %d %s", code, stderr)
	}
	if code, _ := runMain(t, environ, "serve", "--managed", "--listen", "unix"); code != 2 {
		t.Fatalf("--managed --listen: %d", code)
	}
	// Managed with an invalid service.env: 78 (0 on macOS, where launchd cannot exclude it).
	cfgDir := filepath.Join(short, "h", ".config", "stage")
	if runtime.GOOS == "darwin" {
		cfgDir = filepath.Join(short, "h", "Library", "Application Support", "stage")
	}
	_ = os.MkdirAll(cfgDir, 0o700)
	_ = os.WriteFile(filepath.Join(cfgDir, "service.env"), []byte("STAGE_LISTEN=127.0.0.1:7380\n"), 0o600)
	want := 78
	if runtime.GOOS == "darwin" {
		want = 0
	}
	code, stderr = runMain(t, environ, "serve", "--managed")
	if code != want || !strings.Contains(stderr, "must include unix") {
		t.Fatalf("invalid service.env: %d %s", code, stderr)
	}
}

// A managed server owns the identity's default data dir (marked managed);
// a foreground server on it exits 75 and points at `stage service stop`;
// SIGTERM stops the managed one cleanly.
func TestManagedServe(t *testing.T) {
	short, environ := env(t)
	home := filepath.Join(short, "h")
	cfgDir := filepath.Join(home, ".config", "stage")
	data := filepath.Join(home, ".local", "share", "stage")
	if runtime.GOOS == "darwin" {
		cfgDir = filepath.Join(home, "Library", "Application Support", "stage")
		data = cfgDir
	}
	_ = os.MkdirAll(cfgDir, 0o700)
	_ = os.WriteFile(filepath.Join(cfgDir, "service.env"), []byte("STAGE_LISTEN=unix\nT3_FAKE=1\n"), 0o600)
	// An inherited STAGE_DATA_DIR is ignored in managed mode.
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(environ, helperArgs+"=serve\t--managed", "STAGE_DATA_DIR="+filepath.Join(short, "ignored"))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	var holder *instance.Meta
	for i := 0; i < 200 && (holder == nil || len(holder.Listeners) == 0); i++ {
		_, holder, _ = instance.ReadHolder(data)
		time.Sleep(25 * time.Millisecond)
	}
	if holder == nil || !holder.Managed || holder.PID != cmd.Process.Pid || len(holder.Listeners) != 1 || !strings.HasPrefix(holder.Listeners[0], "unix://") {
		t.Fatalf("holder %+v\n%s", holder, stderr.String())
	}
	code, fg := runMain(t, append(environ, "STAGE_DATA_DIR="+data), "serve", "--listen", "unix:"+filepath.Join(short, "b.sock"))
	if code != 75 || !strings.Contains(fg, "stage service stop") {
		t.Fatalf("foreground: %d %s", code, fg)
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("managed exit: %v\n%s", err, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(short, "ignored")); !os.IsNotExist(err) {
		t.Fatal("managed server used the inherited STAGE_DATA_DIR")
	}
}
