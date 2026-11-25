package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// DaemonInfo is what `codex app-server daemon version` (and `start`)
// print: the daemon's status, its control socket and the versions.
type DaemonInfo struct {
	// Status: running, alreadyRunning or started when up; anything else
	// means the daemon is not serving.
	Status           string `json:"status"`
	SocketPath       string `json:"socketPath"`
	CLIVersion       string `json:"cliVersion"`
	AppServerVersion string `json:"appServerVersion"`
}

// Up reports whether the daemon serves its socket.
func (d DaemonInfo) Up() bool {
	switch d.Status {
	case "running", "alreadyRunning", "started":
		return d.SocketPath != ""
	}
	return false
}

// Daemon runs the Codex CLI's daemon commands. Stage starts the daemon
// when it is not running and never stops, restarts or updates it: the
// daemon is shared with the user's own Codex clients.
type Daemon interface {
	// Version reports the daemon without starting it.
	Version(ctx context.Context) (DaemonInfo, error)
	// Start starts the daemon if it is not running (idempotent).
	Start(ctx context.Context) (DaemonInfo, error)
}

// ErrNotInstalled: the Codex CLI was not found.
var ErrNotInstalled = errors.New("codex CLI not found")

// commandTimeout bounds one daemon command.
const commandTimeout = 20 * time.Second

// CLI runs Binary ("" = codex on PATH) with Stage's environment minus the
// variables that would tie the daemon to another session (Environ).
type CLI struct {
	Binary string
	// Env is the base environment (nil: os.Environ()).
	Env []string
}

// Path is the binary CLI runs.
func (c CLI) Path() string {
	if strings.TrimSpace(c.Binary) == "" {
		return "codex"
	}
	return c.Binary
}

func (c CLI) Version(ctx context.Context) (DaemonInfo, error) {
	return c.run(ctx, "version")
}

func (c CLI) Start(ctx context.Context) (DaemonInfo, error) {
	return c.run(ctx, "start")
}

func (c CLI) run(ctx context.Context, verb string) (DaemonInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	path, err := exec.LookPath(c.Path())
	if err != nil {
		return DaemonInfo{}, fmt.Errorf("%w: %s", ErrNotInstalled, c.Path())
	}
	cmd := exec.CommandContext(ctx, path, "app-server", "daemon", verb)
	env := c.Env
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = Environ(env)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	var info DaemonInfo
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &info); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if runErr != nil && msg == "" {
			msg = runErr.Error()
		}
		if msg == "" {
			msg = "unreadable output"
		}
		return DaemonInfo{}, fmt.Errorf("codex app-server daemon %s: %s", verb, firstLine(msg))
	}
	return info, nil
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return strings.TrimSpace(s)
}

// sessionEnv are variables a harness sets for its own sessions: a daemon
// started from inside one must not inherit them (its turns would carry
// another session's identity).
var sessionEnv = []string{
	"CODEX_THREAD_ID", "CODEX_SESSION_ID", "CODEX_TOOL_CALL_ID", "CODEX_INTERNAL_ORIGINATOR_OVERRIDE",
	"STAGE_CALLER_SESSION", "OPENCODE_SESSION_ID", "CLAUDECODE", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_ENTRYPOINT",
}

// Environ is env without session variables (sessionEnv and T3CODE_*).
// CODEX_HOME and everything else stay.
func Environ(env []string) []string {
	out := make([]string, 0, len(env))
next:
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "T3CODE_") {
			continue
		}
		for _, s := range sessionEnv {
			if k == s {
				continue next
			}
		}
		out = append(out, kv)
	}
	return out
}
