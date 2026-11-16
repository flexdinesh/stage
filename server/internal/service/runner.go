package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// Cmd is one service-manager tool invocation (systemctl, journalctl,
// loginctl, launchctl).
type Cmd struct {
	Name string
	Args []string
	// Env adds or overrides variables of the child's environment.
	Env []string
	// Stdout, when set, streams stdout there instead of capturing it (logs).
	Stdout io.Writer
}

// Result is a finished command. A non-zero exit is a Result, not an error.
type Result struct {
	Stdout, Stderr string
	Code           int
}

// Runner runs manager tools. Tests substitute a fake.
type Runner interface {
	Run(ctx context.Context, c Cmd) (Result, error)
}

// ExecRunner runs the real tools, found independently of the caller's
// PATH (a GUI launch may have a minimal one), with stable, uncolored,
// unpaged output.
type ExecRunner struct{}

// toolDirs are searched after PATH.
var toolDirs = []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin", "/run/current-system/sw/bin"}

// ToolPath finds a manager tool: launchctl is always /bin/launchctl; others
// come from PATH, then the usual system dirs.
func ToolPath(name string) (string, error) {
	if name == "launchctl" {
		return "/bin/launchctl", nil
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	for _, d := range toolDirs {
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s not found", name)
}

// Run implements Runner.
func (ExecRunner) Run(ctx context.Context, c Cmd) (Result, error) {
	path, err := ToolPath(c.Name)
	if err != nil {
		return Result{}, err
	}
	cmd := exec.CommandContext(ctx, path, c.Args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "SYSTEMD_PAGER=", "SYSTEMD_COLORS=0", "SYSTEMD_URLIFY=0", "SYSTEMD_LESS=")
	cmd.Env = append(cmd.Env, c.Env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if c.Stdout != nil {
		cmd.Stdout = c.Stdout
	}
	err = cmd.Run()
	res := Result{Stdout: out.String(), Stderr: errb.String()}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.Code = ee.ExitCode()
		return res, nil
	}
	return res, err
}
