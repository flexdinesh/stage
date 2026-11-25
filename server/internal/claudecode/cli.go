package claudecode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

// CLI runs the claude executable for Stage: in a cleaned environment, and a
// background launch in its own systemd scope where one is available.
type CLI struct {
	// Bin is the claude executable (an absolute path, resolved by setup).
	Bin string
	// Environ is the environment Stage passes on before cleaning (nil:
	// os.Environ()).
	Environ []string
	// Scope wraps a background launch in `systemd-run --user --scope`, so
	// Claude's daemon (started by the first launch) lives in its own cgroup
	// and outlives Stage's service (whose stop kills its control group).
	Scope bool
	// Home is the user's home ("" : os.UserHomeDir).
	Home string
}

// Result is one finished run.
type Result struct {
	Stdout, Stderr string
	Code           int
}

// StartError: the executable could not be started, so it did nothing.
type StartError struct{ Err error }

func (e *StartError) Error() string { return e.Err.Error() }
func (e *StartError) Unwrap() error { return e.Err }

// Run runs claude with args in dir ("" : Stage's cwd) and waits. A non-zero
// exit is a Result, not an error; err is a start failure (*StartError) or
// the context ending.
func (c *CLI) Run(ctx context.Context, dir string, args ...string) (Result, error) {
	return c.run(ctx, dir, false, args)
}

// Launch is Run for a background launch (`--bg`): it may start Claude's
// daemon, so it runs in its own scope when Scope is set.
func (c *CLI) Launch(ctx context.Context, dir string, args ...string) (Result, error) {
	return c.run(ctx, dir, c.Scope, args)
}

func (c *CLI) run(ctx context.Context, dir string, scope bool, args []string) (Result, error) {
	name, argv := c.Bin, args
	if scope {
		name = "systemd-run"
		argv = append([]string{"--user", "--scope", "--quiet", "--collect", "--", c.Bin}, args...)
	}
	cmd := exec.CommandContext(ctx, name, argv...)
	cmd.Dir = dir
	cmd.Env = c.env()
	cmd.WaitDelay = 2 * time.Second
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	res := Result{Stdout: out.String(), Stderr: errb.String()}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case ctx.Err() != nil:
		return res, ctx.Err()
	case errors.As(err, &exit):
		res.Code = exit.ExitCode()
	default:
		return res, &StartError{Err: err}
	}
	return res, nil
}

// clearedEnv are the variables a Claude (or T3) process sets for its own
// session; inherited, they would make the CLI act as that session's child.
var clearedEnv = []string{
	"CLAUDECODE", "CLAUDE_PID", "CLAUDE_AGENT_SDK_VERSION",
	"CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_SESSION_KIND", "CLAUDE_CODE_SESSION_ATTENDED",
	"CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_CHILD_NOTES_DIR", "CLAUDE_CODE_ENTRYPOINT",
	"CLAUDE_CODE_EXECPATH", "CLAUDE_JOB_DIR", "CLAUDE_BG_BACKEND",
}

// clearedPrefixes are cleared whole: Claude's messaging channel and T3's
// session and service variables.
var clearedPrefixes = []string{"CLAUDE_CODE_MESSAGING_", "T3CODE_", "T3_"}

// Cleared reports whether Stage drops the variable key before running
// claude. Claude's own settings (CLAUDE_CONFIG_DIR, CLAUDE_CODE_USE_*,
// ANTHROPIC_*) pass through.
func Cleared(key string) bool {
	if slices.Contains(clearedEnv, key) {
		return true
	}
	return slices.ContainsFunc(clearedPrefixes, func(p string) bool { return strings.HasPrefix(key, p) })
}

func (c *CLI) env() []string {
	src := c.Environ
	if src == nil {
		src = os.Environ()
	}
	out := make([]string, 0, len(src)+1)
	path := ""
	for _, kv := range src {
		k, v, _ := strings.Cut(kv, "=")
		if Cleared(k) {
			continue
		}
		if k == "PATH" {
			path = v
			continue
		}
		out = append(out, kv)
	}
	return append(out, "PATH="+widenPath(path, c.home()))
}

func (c *CLI) home() string {
	if c.Home != "" {
		return c.Home
	}
	h, _ := os.UserHomeDir()
	return h
}

// ToolDirs are where installers put claude and the tools its sessions run
// when a service manager's PATH lacks them; searched after PATH.
func ToolDirs(home string) []string {
	var dirs []string
	if home != "" {
		for _, d := range []string{".local/bin", ".claude/local", ".npm-global/bin", ".bun/bin", ".volta/bin", ".local/share/pnpm"} {
			dirs = append(dirs, filepath.Join(home, d))
		}
	}
	return append(dirs, "/usr/local/bin", "/opt/homebrew/bin", "/usr/bin", "/bin")
}

// widenPath appends the tool dirs missing from path: a background session
// inherits the environment of the launch that started Claude's daemon.
func widenPath(path, home string) string {
	parts := filepath.SplitList(path)
	for _, d := range ToolDirs(home) {
		if !slices.Contains(parts, d) {
			parts = append(parts, d)
		}
	}
	return strings.Join(parts, string(os.PathListSeparator))
}

// Find resolves the claude command: bin (a name or path) when set, else
// claude on PATH, else in the tool dirs. "" when none is executable.
func Find(bin, home string, lookPath func(string) (string, error)) string {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if bin = strings.TrimSpace(bin); bin != "" {
		if strings.HasPrefix(bin, "~/") && home != "" {
			bin = filepath.Join(home, bin[2:])
		}
		p, err := lookPath(bin)
		if err != nil {
			return ""
		}
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
		return p
	}
	if p, err := lookPath("claude"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
		return p
	}
	for _, d := range ToolDirs(home) {
		if p, err := lookPath(filepath.Join(d, "claude")); err == nil {
			return p
		}
	}
	return ""
}

var (
	scopeOnce sync.Once
	scopeOK   bool
)

// ScopeAvailable reports whether `systemd-run --user --scope` works here
// (Linux with a reachable user manager), probed once per process.
func ScopeAvailable() bool {
	scopeOnce.Do(func() {
		if runtime.GOOS != "linux" {
			return
		}
		if _, err := exec.LookPath("systemd-run"); err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		scopeOK = exec.CommandContext(ctx, "systemd-run", "--user", "--scope", "--quiet", "--collect", "true").Run() == nil
	})
	return scopeOK
}

// excerpt is a CLI output's first lines for an error message.
func excerpt(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 400 {
		s = s[:400] + "…"
	}
	return s
}

func exitError(what string, r Result) error {
	msg := excerpt(r.Stderr)
	if msg == "" {
		msg = excerpt(r.Stdout)
	}
	if msg == "" {
		return fmt.Errorf("claude %s exited %d", what, r.Code)
	}
	return fmt.Errorf("claude %s exited %d: %s", what, r.Code, msg)
}
