package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// systemd drives the user manager (systemctl --user).
type systemd struct {
	r      Runner
	uid    int
	cliEnv func(string) string
	// extraEnv is set when the user bus was found only via the default
	// XDG_RUNTIME_DIR (the CLI's own environment lacked it).
	extraEnv []string
}

func (s *systemd) Kind() string { return "systemd" }

func (s *systemd) run(ctx context.Context, name string, args ...string) (Result, error) {
	return s.r.Run(ctx, Cmd{Name: name, Args: args, Env: s.extraEnv})
}

func (s *systemd) ctl(ctx context.Context, args ...string) (Result, error) {
	return s.run(ctx, "systemctl", append([]string{"--user", "--no-pager"}, args...)...)
}

const noManagerFix = "log in through a session that starts a systemd user manager (pam_systemd), or run `loginctl enable-linger $USER` and `export XDG_RUNTIME_DIR=/run/user/$(id -u)`; otherwise run the server yourself in the foreground: `stage serve --listen unix`"

func (s *systemd) Probe(ctx context.Context) (ManagerInfo, error) {
	res, err := s.ctl(ctx, "show", "-p", "Version", "-p", "UnitPath")
	if err != nil {
		return ManagerInfo{}, &UnavailableError{Msg: "systemctl is not available: " + err.Error() + " (Stage supports only systemd user services on Linux)", Fix: "run the server yourself in the foreground: `stage serve --listen unix`"}
	}
	if res.Code != 0 && s.cliEnv("XDG_RUNTIME_DIR") == "" {
		dir := fmt.Sprintf("/run/user/%d", s.uid)
		if st, err := os.Stat(dir); err == nil && st.IsDir() && ownedBy(st, s.uid) {
			s.extraEnv = []string{"XDG_RUNTIME_DIR=" + dir}
			res, err = s.ctl(ctx, "show", "-p", "Version", "-p", "UnitPath")
			if err != nil || res.Code != 0 {
				s.extraEnv = nil
			}
		}
	}
	if res.Code != 0 {
		msg := firstLine(res.Stderr)
		if strings.Contains(res.Stderr, "not been booted with systemd") {
			return ManagerInfo{}, &UnavailableError{Msg: "this system is not running systemd: " + msg, Fix: "run the server yourself in the foreground: `stage serve --listen unix`"}
		}
		return ManagerInfo{}, &UnavailableError{Msg: "no systemd user manager reachable: " + msg, Fix: noManagerFix}
	}
	props := parseProps(res.Stdout)
	info := ManagerInfo{Kind: "systemd", Version: props["Version"], Env: map[string]string{}}
	if env, err := s.ctl(ctx, "show-environment"); err == nil && env.Code == 0 {
		for _, l := range lines(env.Stdout) {
			if k, v, ok := strings.Cut(l, "="); ok {
				info.Env[k] = v
			}
		}
	}
	home := info.Env["HOME"]
	if home == "" {
		home = s.cliEnv("HOME")
		info.Env["HOME"] = home
	}
	info.UnitDir = unitDir(props["UnitPath"], home, info.Env["XDG_CONFIG_HOME"], info.Env["XDG_RUNTIME_DIR"])
	info.Linger = s.linger(ctx)
	return info, nil
}

// unitDir is the persistent user unit dir the manager actually searches:
// the first UnitPath entry named systemd/user under home (not the runtime
// dir, not .control/.attached), which is the manager's own
// $XDG_CONFIG_HOME/systemd/user. Without UnitPath it is derived from the
// manager environment.
func unitDir(unitPath, home, xdgConfig, runtimeDir string) string {
	for _, d := range strings.Fields(unitPath) {
		if filepath.Base(d) != "user" || filepath.Base(filepath.Dir(d)) != "systemd" {
			continue
		}
		if home != "" && strings.HasPrefix(d, home+"/") && (runtimeDir == "" || !strings.HasPrefix(d, runtimeDir+"/")) {
			return d
		}
	}
	base := filepath.Join(home, ".config")
	if filepath.IsAbs(xdgConfig) {
		base = xdgConfig
	}
	return filepath.Join(base, "systemd", "user")
}

func ownedBy(st os.FileInfo, uid int) bool {
	sys, ok := st.Sys().(*syscall.Stat_t)
	return ok && int(sys.Uid) == uid
}

func (s *systemd) linger(ctx context.Context) *bool {
	if res, err := s.run(ctx, "loginctl", "show-user", strconv.Itoa(s.uid), "-p", "Linger", "--value"); err == nil && res.Code == 0 {
		v := strings.TrimSpace(res.Stdout) == "yes"
		return &v
	}
	for _, u := range []string{s.cliEnv("USER"), s.cliEnv("LOGNAME")} {
		if u == "" {
			continue
		}
		_, err := os.Stat(filepath.Join("/var/lib/systemd/linger", u))
		v := err == nil
		return &v
	}
	return nil
}

const unitProps = "LoadState,ActiveState,SubState,MainPID,NRestarts,ExecMainCode,ExecMainStatus,Result,InvocationID,NeedDaemonReload,FragmentPath,DropInPaths,UnitFileState"

func (s *systemd) State(ctx context.Context, p Paths) (RunState, error) {
	res, err := s.ctl(ctx, "show", p.Unit, "-p", unitProps)
	if err != nil {
		return RunState{}, err
	}
	if res.Code != 0 {
		return RunState{}, fmt.Errorf("systemctl show %s: %s", p.Unit, firstLine(res.Stderr))
	}
	pr := parseProps(res.Stdout)
	rs := RunState{
		Loaded: pr["LoadState"] != "" && pr["LoadState"] != "not-found",
		Masked: pr["LoadState"] == "masked",
		PID:    atoi(pr["MainPID"]), Restarts: atoi(pr["NRestarts"]), Result: pr["Result"],
		RunID: pr["InvocationID"], NeedsReload: pr["NeedDaemonReload"] == "yes",
		LoadedFrom: pr["FragmentPath"], Enabled: pr["UnitFileState"] == "enabled",
		AutoRestart: pr["SubState"] == "auto-restart",
	}
	if d := strings.TrimSpace(pr["DropInPaths"]); d != "" {
		rs.DropIns = strings.Fields(d)
	}
	// ExecMainCode 1 = CLD_EXITED: ExecMainStatus is an exit status (else a signal).
	if pr["ExecMainCode"] == "1" {
		v := atoi(pr["ExecMainStatus"])
		rs.LastExit = &v
	}
	switch pr["ActiveState"] {
	case "active", "reloading", "refreshing":
		rs.Phase = PhaseRunning
	case "activating":
		rs.Phase = PhaseStarting
	case "deactivating":
		rs.Phase = PhaseStopping
	case "failed":
		rs.Phase = PhaseFailed
	default:
		rs.Phase = PhaseStopped
	}
	return rs, nil
}

func (s *systemd) must(ctx context.Context, args ...string) error {
	res, err := s.ctl(ctx, args...)
	if err != nil {
		return err
	}
	if res.Code != 0 {
		return fmt.Errorf("systemctl --user %s: %s", strings.Join(args, " "), firstLine(res.Stderr))
	}
	return nil
}

func (s *systemd) Reload(ctx context.Context, p Paths, wrote bool, rs RunState) (bool, error) {
	if !wrote && !rs.NeedsReload {
		return false, nil
	}
	return true, s.must(ctx, "daemon-reload")
}

func (s *systemd) resetFailed(ctx context.Context, p Paths, rs RunState) {
	if rs.Phase == PhaseFailed || (rs.Result != "" && rs.Result != "success") {
		_, _ = s.ctl(ctx, "reset-failed", p.Unit)
	}
}

func (s *systemd) Start(ctx context.Context, p Paths, rs RunState, _ bool) error {
	s.resetFailed(ctx, p, rs)
	return s.must(ctx, "start", "--no-block", p.Unit)
}

func (s *systemd) Stop(ctx context.Context, p Paths) error {
	return s.must(ctx, "stop", "--no-block", p.Unit)
}

func (s *systemd) RestartInPlace(ctx context.Context, p Paths, rs RunState) (bool, error) {
	s.resetFailed(ctx, p, rs)
	return true, s.must(ctx, "restart", "--no-block", p.Unit)
}

func (s *systemd) LoginStartup(_ context.Context, _ Paths, rs RunState) bool { return rs.Enabled }

func (s *systemd) SetLoginStartup(ctx context.Context, p Paths, on bool, _ []byte) error {
	if on {
		return s.must(ctx, "enable", p.Unit)
	}
	err := s.must(ctx, "disable", p.Unit)
	// disable needs the unit file; a leftover link of ours is removed directly.
	link := filepath.Join(filepath.Dir(p.Definition), "default.target.wants", p.Unit)
	if t, lerr := os.Readlink(link); lerr == nil && t == p.Definition {
		if rerr := os.Remove(link); rerr == nil {
			return nil
		}
	}
	return err
}

func (s *systemd) Unregister(ctx context.Context, p Paths) error {
	if err := s.must(ctx, "daemon-reload"); err != nil {
		return err
	}
	_, _ = s.ctl(ctx, "reset-failed", p.Unit)
	return nil
}

func (s *systemd) LogTail(ctx context.Context, p Paths, rs RunState, n int) []string {
	args := []string{"--user", "--no-pager", "-o", "cat", "-n", strconv.Itoa(n)}
	if rs.RunID != "" {
		args = append(args, "_SYSTEMD_INVOCATION_ID="+rs.RunID)
	} else {
		args = append(args, "-u", p.Unit)
	}
	res, err := s.run(ctx, "journalctl", args...)
	if err != nil || res.Code != 0 {
		return nil
	}
	return lines(res.Stdout)
}

func (s *systemd) Logs(ctx context.Context, p Paths, w io.Writer, n int, follow bool) error {
	args := []string{"--user", "--no-pager", "-o", "short-iso", "-u", p.Unit, "-n", strconv.Itoa(n)}
	if follow {
		args = append(args, "-f")
	}
	res, err := s.r.Run(ctx, Cmd{Name: "journalctl", Args: args, Env: s.extraEnv, Stdout: w})
	if err != nil {
		return err
	}
	if res.Code != 0 && ctx.Err() == nil {
		return errors.New("journalctl: " + firstLine(res.Stderr))
	}
	return nil
}

func (s *systemd) LogsInfo(p Paths) LogsInfo { return LogsInfo{Kind: "journald", Unit: p.Unit} }
