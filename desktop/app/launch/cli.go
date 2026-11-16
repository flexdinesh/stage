package launch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Exit codes of `stage service` (docs/service.md).
const (
	codeOK          = 0
	codeFailed      = 1
	codeUsage       = 2
	codeTimeout     = 3
	codeNotRunning  = 4
	codeConflict    = 5
	codeUnavailable = 6
	codeInvalid     = 7
)

// Result is a finished command: its output and exit code. Err is set when
// it could not run or did not finish (killed at its timeout).
type Result struct {
	Stdout, Stderr []byte
	Code           int
	Err            error
}

// Runner runs a command to completion; ctx bounds it.
type Runner func(ctx context.Context, name string, args ...string) Result

// ExecRunner runs commands with os/exec.
func ExecRunner(ctx context.Context, name string, args ...string) Result {
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	// A killed command may leave children holding its output open.
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	r := Result{Stdout: out.Bytes(), Stderr: errb.Bytes()}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case ctx.Err() != nil:
		r.Code, r.Err = -1, ctx.Err()
	case errors.As(err, &exit):
		r.Code = exit.ExitCode()
	default:
		r.Code, r.Err = -1, err
	}
	return r
}

// buildInfo is `stage version --json`.
type buildInfo struct {
	Identity   string `json:"identity"`
	Version    string `json:"version"`
	Commit     string `json:"commit"`
	APIVersion int    `json:"apiVersion"`
}

// report is the part of a `stage service <verb> --json` report the app
// reads.
type report struct {
	Code    int      `json:"code"`
	OK      bool     `json:"ok"`
	Message string   `json:"message"`
	Fix     string   `json:"fix"`
	Notes   []string `json:"notes"`
	LogTail []string `json:"logTail"`
	Status  *status  `json:"status"`
}

// status is the part of `stage service status --json` the app reads.
type status struct {
	Identity string `json:"identity"`
	State    string `json:"state"`
	Ready    bool   `json:"ready"`
	Socket   struct {
		Path   string `json:"path"`
		Exists bool   `json:"exists"`
	} `json:"socket"`
	Lock struct {
		Held   bool `json:"held"`
		Holder *struct {
			PID     int  `json:"pid"`
			Managed bool `json:"managed"`
		} `json:"holder"`
		OwnedByService bool `json:"ownedByService"`
	} `json:"lock"`
	Stage struct {
		Reachable  bool   `json:"reachable"`
		Version    string `json:"version"`
		APIVersion int    `json:"apiVersion"`
	} `json:"stage"`
	Definition struct {
		LoginStartup bool `json:"loginStartup"`
	} `json:"definition"`
	Problems []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Fix     string `json:"fix"`
	} `json:"problems"`
}

func (s *status) problems() []Problem {
	if s == nil {
		return nil
	}
	out := make([]Problem, 0, len(s.Problems))
	for _, p := range s.Problems {
		out = append(out, Problem{Code: p.Code, Message: p.Message, Fix: p.Fix})
	}
	return out
}

// DefaultTempDirs are refused as the app's location (as the server's
// service.DefaultTempDirs refuses them for a service executable).
func DefaultTempDirs() []string {
	return []string{os.TempDir(), "/tmp", "/var/tmp", "/private/tmp", "/private/var/folders", "/var/folders"}
}

// ErrUnstableLocation marks an app run from a place a service must not
// point at.
var ErrUnstableLocation = errors.New("unstable app location")

// ValidateLocation refuses a bundled CLI the service could not keep
// running: a relative path, temporary or build-cache locations, macOS App
// Translocation and the Trash. It mirrors the server's
// service.ValidateExe, so the app explains the problem itself instead of
// letting `stage service start` refuse --exe.
func ValidateLocation(exe string, tempDirs []string) error {
	if !filepath.IsAbs(exe) {
		return fmt.Errorf("%w: %q is not an absolute path", ErrUnstableLocation, exe)
	}
	clean := filepath.Clean(exe)
	for _, d := range tempDirs {
		d = filepath.Clean(d)
		if d != "." && d != "/" && (clean == d || strings.HasPrefix(clean, d+"/")) {
			return fmt.Errorf("%w: %s is in a temporary directory (%s)", ErrUnstableLocation, clean, d)
		}
	}
	for _, seg := range []string{"/go-build", "/AppTranslocation/", "/.Trash/", "/.local/share/Trash/"} {
		if strings.Contains(clean, seg) {
			return fmt.Errorf("%w: %s is in a transient location (%s)", ErrUnstableLocation, clean, strings.Trim(seg, "/"))
		}
	}
	return nil
}

// parseJSON decodes the single JSON document a --json command prints.
func parseJSON(out []byte, v any) error {
	out = bytes.TrimSpace(out)
	if len(out) == 0 || out[0] != '{' {
		return errors.New("no JSON output")
	}
	return json.Unmarshal(out, v)
}

// lines splits command output into non-empty lines, keeping the last n.
func lines(b []byte, n int) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if l = strings.TrimRight(l, "\r"); strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// shellQuote quotes s for a POSIX shell when it needs it.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`!*?[]{}()<>|&;#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
