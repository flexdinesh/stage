// Package claudetest runs the Claude Code adapter against a fake claude CLI
// (fakeclaude): a real executable with a state file the test drives.
package claudetest

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest/fakestate"
)

var (
	buildOnce sync.Once
	buildDir  string
	buildErr  error
)

// binary builds fakeclaude once per test process.
func binary(t testing.TB) string {
	t.Helper()
	buildOnce.Do(func() {
		buildDir, buildErr = os.MkdirTemp("", "fakeclaude-")
		if buildErr != nil {
			return
		}
		out, err := exec.Command("go", "build", "-o", filepath.Join(buildDir, "claude"), "github.com/flexdinesh/stage/server/internal/claudecode/claudetest/fakeclaude").CombinedOutput()
		if err != nil {
			buildErr = &buildError{err: err, out: string(out)}
		}
	})
	if buildErr != nil {
		t.Fatalf("build fake claude: %v", buildErr)
	}
	return filepath.Join(buildDir, "claude")
}

type buildError struct {
	err error
	out string
}

func (e *buildError) Error() string { return e.err.Error() + ": " + e.out }

// Fake is one fake claude installation: a home with a config directory, a
// transcript root, a trusted workspace and the state file.
type Fake struct {
	t     testing.TB
	Bin   string
	Home  string
	State string
	// Workspace is a trusted folder (a Git checkout top).
	Workspace string
}

// New sets up a fake: logged in, version 2.1.332, Workspace trusted.
func New(t testing.TB) *Fake {
	t.Helper()
	// Short paths: transcript folder names encode the full cwd.
	home, err := os.MkdirTemp("", "cc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	f := &Fake{t: t, Bin: binary(t), Home: home, State: filepath.Join(home, "fake-state.json"), Workspace: filepath.Join(home, "src", "repo")}
	if err := os.MkdirAll(filepath.Join(f.Workspace, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.Update(func(s *fakestate.State) {
		s.Version, s.LoggedIn = "2.1.332", true
		s.ConfigDir, s.ProjectsDir = filepath.Join(home, ".claude"), filepath.Join(home, ".claude", "projects")
		s.Trusted = []string{f.Workspace}
	})
	f.WriteClaudeJSON(f.Workspace)
	return f
}

// ConfigDir is the fake's config directory (the connection identity).
func (f *Fake) ConfigDir() string { return filepath.Join(f.Home, ".claude") }

// ProjectsDir is the fake's transcript root.
func (f *Fake) ProjectsDir() string { return filepath.Join(f.Home, ".claude", "projects") }

// CLI runs the fake with a test environment.
func (f *Fake) CLI() *claudecode.CLI {
	return &claudecode.CLI{Bin: f.Bin, Home: f.Home, Environ: f.Environ()}
}

// Environ is the environment Stage would pass on: the state file, plus
// session variables the CLI must clear.
func (f *Fake) Environ() []string {
	return []string{
		"HOME=" + f.Home, "PATH=" + os.Getenv("PATH"), "FAKE_CLAUDE_STATE=" + f.State,
		"CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=00000000-0000-4000-8000-000000000000", "CLAUDE_CODE_ENTRYPOINT=t3-code",
		"CLAUDE_CODE_MESSAGING_SOCKET=/tmp/x.sock", "T3CODE_THREAD_ID=thread", "CLAUDE_CONFIG_DIR_KEEP=1",
	}
}

// Adapter is an adapter on connection over the fake.
func (f *Fake) Adapter(connection int64) *claudecode.Adapter {
	return claudecode.New(claudecode.Options{Connection: connection, CLI: f.CLI(), Identity: f.ConfigDir(), RosterTTL: time.Nanosecond, LaunchWait: 2 * time.Second})
}

// Update changes the fake's state.
func (f *Fake) Update(fn func(*fakestate.State)) {
	f.t.Helper()
	if err := fakestate.Update(f.State, func(s *fakestate.State) error { fn(s); return nil }); err != nil {
		f.t.Fatal(err)
	}
}

// Read is the fake's state.
func (f *Fake) Read() fakestate.State {
	f.t.Helper()
	s, err := fakestate.Read(f.State)
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

// Session is the listed session with id (UUID).
func (f *Fake) Session(id string) fakestate.Session {
	f.t.Helper()
	for _, s := range f.Read().Sessions {
		if s.SessionID == id {
			return s
		}
	}
	f.t.Fatalf("no fake session %s", id)
	return fakestate.Session{}
}

// SetState sets a session's list state and status.
func (f *Fake) SetState(id, state, status, waitingFor string) {
	f.Update(func(s *fakestate.State) {
		for i := range s.Sessions {
			if s.Sessions[i].SessionID == id {
				s.Sessions[i].State, s.Sessions[i].Status, s.Sessions[i].WaitingFor = state, status, waitingFor
			}
		}
	})
}

// Remove drops a session from the list (claude rm keeps the transcript).
func (f *Fake) Remove(id string) {
	f.Update(func(s *fakestate.State) {
		for i := range s.Sessions {
			if s.Sessions[i].SessionID == id {
				s.Sessions[i].Removed = true
			}
		}
	})
}

// Reply appends an assistant reply to a session's transcript.
func (f *Fake) Reply(id, text string, turnOver bool) {
	f.t.Helper()
	if err := fakestate.AppendReply(f.ProjectsDir(), f.Session(id).Cwd, id, text, turnOver); err != nil {
		f.t.Fatal(err)
	}
}

// AddInteractive lists an interactive session (a terminal or SDK one) with
// a transcript whose first prompt has entrypoint.
func (f *Fake) AddInteractive(id, cwd, entrypoint, prompt string) {
	f.t.Helper()
	f.Update(func(s *fakestate.State) {
		s.Sessions = append(s.Sessions, fakestate.Session{Entry: fakestate.Entry{PID: 7, Cwd: cwd, Kind: "interactive", StartedAt: time.Now().UnixMilli(), SessionID: id, Name: "interactive", Status: "busy"}})
	})
	if err := fakestate.AppendUser(f.ProjectsDir(), cwd, id, entrypoint, prompt, time.Now().UTC()); err != nil {
		f.t.Fatal(err)
	}
}

// WriteClaudeJSON writes the state file's projects (Claude's inventory).
func (f *Fake) WriteClaudeJSON(dirs ...string) {
	f.t.Helper()
	b := []byte(`{"numStartups":1,"projects":{`)
	for i, d := range dirs {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, []byte(`"`+d+`":{"hasTrustDialogAccepted":true}`)...)
	}
	b = append(b, []byte(`}}`)...)
	if err := os.WriteFile(filepath.Join(f.Home, ".claude.json"), b, 0o600); err != nil {
		f.t.Fatal(err)
	}
}

// Launches are the fake's --bg calls.
func (f *Fake) Launches() []fakestate.Call {
	var out []fakestate.Call
	for _, c := range f.Read().Calls {
		if len(c.Args) > 0 && c.Args[0] == "--bg" {
			out = append(out, c)
		}
	}
	return out
}
