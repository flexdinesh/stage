package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/service"
)

// OSDeps is the real environment; log receives progress lines.
func OSDeps(log io.Writer, svc func() service.Deps) Deps {
	home, _ := os.UserHomeDir()
	return Deps{
		Platform: Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Home: home, Getenv: os.Getenv},
		Identity: buildinfo.Identity, Version: buildinfo.Version,
		Service: osService{deps: svc},
		Desktop: osDesktop{goos: runtime.GOOS, getenv: os.Getenv, home: home},
		Run:     runCmd, LookPath: exec.LookPath, FreeSpace: freeSpace,
		Log: log, Now: time.Now, PID: os.Getpid(),
	}
}

func runCmd(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	return cmd.CombinedOutput()
}

func freeSpace(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}

func isNoSpace(err error) bool {
	return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT)
}

// osService drives the per-user service through its controller (a new
// controller per operation).
type osService struct{ deps func() service.Deps }

func (s osService) d() service.Deps {
	if s.deps != nil {
		return s.deps()
	}
	return service.OSDeps()
}

func (s osService) Status(ctx context.Context) service.Status {
	st, _ := service.New(s.d()).Status(ctx)
	return st
}

func (s osService) Restart(ctx context.Context, exe string) service.Report {
	return service.New(s.d()).Restart(ctx, service.StartOptions{Exe: exe, Wait: service.DefaultRestartWait})
}

func (s osService) Uninstall(ctx context.Context) service.Report {
	return service.New(s.d()).Uninstall(ctx, service.DefaultStopWait)
}

func (s osService) ServerSchema(ctx context.Context, st service.Status) (int64, bool) {
	d := s.d()
	if !st.Stage.Reachable || st.Stage.URL == "" || d.Health == nil {
		return 0, false
	}
	h, err := d.Health(ctx, st.Stage.URL)
	if err != nil || h.Schema <= 0 {
		return 0, false
	}
	return h.Schema, true
}

// osDesktop finds the desktop app by its executable path, quits it with
// SIGTERM (MyGo turns it into a graceful Quit) and relaunches it detached.
type osDesktop struct {
	goos   string
	getenv func(string) string
	home   string
}

func (o osDesktop) Running(l Layout) []int {
	if l.Desktop == "" {
		return nil
	}
	if o.goos == "darwin" {
		return o.runningDarwin(l)
	}
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var pids []int
	self := os.Getpid()
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		exe, err := os.Readlink(filepath.Join("/proc", e.Name(), "exe"))
		if err != nil {
			continue
		}
		exe = strings.TrimSuffix(exe, " (deleted)")
		if exe == l.Desktop || (filepath.Base(exe) == l.Slug && (within(exe, l.App) || within(exe, l.Prev))) {
			pids = append(pids, pid)
		}
	}
	return pids
}

func (o osDesktop) runningDarwin(l Layout) []int {
	out, err := exec.Command("/bin/ps", "-axo", "pid=,comm=").Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		comm := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), f[0]))
		if comm == l.Desktop {
			pids = append(pids, pid)
		}
	}
	return pids
}

func (o osDesktop) Quit(ctx context.Context, pids []int) error {
	for _, pid := range pids {
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		alive := 0
		for _, pid := range pids {
			if syscall.Kill(pid, 0) == nil {
				alive++
			}
		}
		if alive == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%d desktop process(es) still running after 15s", alive)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (o osDesktop) Launch(l Layout) error {
	if o.goos == "darwin" {
		return exec.Command("/usr/bin/open", l.App).Run()
	}
	if o.getenv("DISPLAY") == "" && o.getenv("WAYLAND_DISPLAY") == "" {
		return errors.New("no display in this session")
	}
	cmd := exec.Command(l.Desktop)
	cmd.Dir = o.home
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
