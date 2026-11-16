package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"
)

// launchd drives a LaunchAgent in the user's GUI domain (gui/<uid>). The
// canonical plist lives in the config dir and is bootstrapped on start; a
// copy in ~/Library/LaunchAgents (login startup) loads it at login.
type launchd struct {
	r      Runner
	uid    int
	cliEnv func(string) string
}

func (l *launchd) Kind() string { return "launchd" }

func (l *launchd) domain() string        { return fmt.Sprintf("gui/%d", l.uid) }
func (l *launchd) target(p Paths) string { return l.domain() + "/" + p.Label }
func (l *launchd) lc(ctx context.Context, args ...string) (Result, error) {
	return l.r.Run(ctx, Cmd{Name: "launchctl", Args: args})
}

func (l *launchd) Probe(ctx context.Context) (ManagerInfo, error) {
	res, err := l.lc(ctx, "print", l.domain())
	if err != nil {
		return ManagerInfo{}, &UnavailableError{Msg: "launchctl is not available: " + err.Error(), Fix: "run the server yourself in the foreground: `stage serve --listen unix`"}
	}
	if res.Code != 0 {
		return ManagerInfo{}, &UnavailableError{
			Msg: fmt.Sprintf("no GUI login session for uid %d (launchctl print %s: %s)", l.uid, l.domain(), firstLine(res.Stderr+res.Stdout)),
			Fix: "LaunchAgents run only while you are logged in to the desktop: log in, or run the server yourself in the foreground: `stage serve --listen unix`",
		}
	}
	return ManagerInfo{Kind: "launchd", Env: map[string]string{"HOME": l.cliEnv("HOME")}}, nil
}

// parsePrint reads `launchctl print <target>` (not a stable format: only
// the few top-level keys needed, parsed defensively).
func parsePrint(out string) RunState {
	rs := RunState{Loaded: true, Phase: PhaseStopped}
	state, runs := "", 0
	for _, raw := range strings.Split(out, "\n") {
		// Top-level keys are indented by one tab; nested blocks by more.
		if !strings.HasPrefix(raw, "\t") || strings.HasPrefix(raw, "\t\t") {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimSpace(raw), " = ")
		if !ok {
			continue
		}
		switch k {
		case "state":
			state = v
		case "pid":
			rs.PID = atoi(v)
		case "runs":
			runs = atoi(v)
		case "path":
			rs.LoadedFrom = v
		case "last exit code":
			if f := strings.FieldsFunc(v, func(r rune) bool { return r == ':' || r == ' ' }); len(f) > 0 {
				if n, err := parseInt(f[0]); err == nil {
					rs.LastExit = &n
				}
			}
		}
	}
	if runs > 0 {
		rs.Restarts = runs - 1
	}
	switch {
	case state == "running" && rs.PID > 0:
		rs.Phase = PhaseRunning
	case strings.Contains(state, "spawn") || strings.Contains(state, "xpcproxy"):
		rs.Phase = PhaseStarting
	case rs.LastExit != nil && *rs.LastExit != 0:
		rs.Phase = PhaseFailed
	}
	return rs
}

func parseInt(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}

func (l *launchd) State(ctx context.Context, p Paths) (RunState, error) {
	res, err := l.lc(ctx, "print", l.target(p))
	if err != nil {
		return RunState{}, err
	}
	if res.Code != 0 {
		return RunState{Phase: PhaseStopped}, nil
	}
	return parsePrint(res.Stdout), nil
}

func (l *launchd) Reload(context.Context, Paths, bool, RunState) (bool, error) { return false, nil }

func (l *launchd) Start(ctx context.Context, p Paths, rs RunState, defChanged bool) error {
	if rs.Loaded && defChanged {
		if err := l.Stop(ctx, p); err != nil {
			return err
		}
		rs.Loaded = false
	}
	if rs.Loaded {
		res, err := l.lc(ctx, "kickstart", l.target(p))
		if err != nil {
			return err
		}
		if res.Code != 0 {
			return fmt.Errorf("launchctl kickstart %s: %s", l.target(p), firstLine(res.Stderr))
		}
		return nil
	}
	// Clears a persisted `launchctl disable` override; idempotent.
	_, _ = l.lc(ctx, "enable", l.target(p))
	res, err := l.lc(ctx, "bootstrap", l.domain(), p.Definition)
	if err != nil {
		return err
	}
	if res.Code == 0 {
		return nil
	}
	// Error codes vary by macOS version ("already loaded" is 5, 17 or 37):
	// the job being loaded now is what counts.
	if st, err := l.State(ctx, p); err == nil && st.Loaded {
		return nil
	}
	msg := firstLine(res.Stderr + res.Stdout)
	switch {
	case res.Code == 125:
		return &UnavailableError{Msg: "launchd domain " + l.domain() + " is not available: " + msg, Fix: "log in to the desktop session"}
	case res.Code == 119 || strings.Contains(strings.ToLower(msg), "disabled"):
		return fmt.Errorf("launchd refuses %s as disabled (%s): allow it in System Settings > General > Login Items & Extensions, or run `launchctl enable %s`", p.Label, msg, l.target(p))
	case res.Code == 122:
		return fmt.Errorf("launchd refuses %s: its permissions must not allow group/other writes (%s)", p.Definition, msg)
	}
	return fmt.Errorf("launchctl bootstrap %s %s: exit %d: %s", l.domain(), p.Definition, res.Code, msg)
}

func (l *launchd) Stop(ctx context.Context, p Paths) error {
	res, err := l.lc(ctx, "bootout", l.target(p))
	if err != nil {
		return err
	}
	if res.Code == 0 {
		return nil
	}
	if st, err := l.State(ctx, p); err == nil && !st.Loaded {
		return nil // already unloaded (3, 113)
	}
	return fmt.Errorf("launchctl bootout %s: exit %d: %s", l.target(p), res.Code, firstLine(res.Stderr))
}

func (l *launchd) RestartInPlace(context.Context, Paths, RunState) (bool, error) { return false, nil }

func (l *launchd) LoginStartup(_ context.Context, p Paths, _ RunState) bool {
	b, err := os.ReadFile(p.LoginCopy)
	return err == nil && ParseDefinition("darwin", b).Generated
}

func (l *launchd) SetLoginStartup(_ context.Context, p Paths, on bool, def []byte) error {
	cur, err := os.ReadFile(p.LoginCopy)
	exists := err == nil
	if exists && !ParseDefinition("darwin", cur).Generated {
		return fmt.Errorf("%s was not written by `stage service`; leaving it", p.LoginCopy)
	}
	if on {
		return writeFileAtomic(p.LoginCopy, def, 0o644, 0o755)
	}
	if err := os.Remove(p.LoginCopy); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (l *launchd) Unregister(context.Context, Paths) error { return nil }

func (l *launchd) LogTail(_ context.Context, p Paths, _ RunState, n int) []string {
	out := tailLines(p.LogFile, n)
	if e := tailLines(p.StderrFile, n); len(e) > 0 {
		out = append(out, e...)
	}
	return out
}

func (l *launchd) Logs(ctx context.Context, p Paths, w io.Writer, n int, follow bool) error {
	return tailFile(ctx, p.LogFile, w, n, follow, 500*time.Millisecond)
}

func (l *launchd) LogsInfo(p Paths) LogsInfo {
	return LogsInfo{Kind: "file", Path: p.LogFile, StderrPath: p.StderrFile}
}
