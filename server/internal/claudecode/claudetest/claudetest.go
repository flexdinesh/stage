// Package claudetest runs the Claude Code adapter against a fake claude CLI
// (fakeclaude): a real executable with a state file the test drives.
package claudetest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// Git runs git in dir with a fixed identity and no user or system config,
// failing the test on error; it returns trimmed stdout.
func (f *Fake) Git(dir string, args ...string) string {
	f.t.Helper()
	c := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=stage", "-c", "user.email=stage@example.invalid"}, args...)...)
	c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	out, err := c.Output()
	if err != nil {
		f.t.Fatalf("git %v in %s: %v", args, dir, err)
	}
	return strings.TrimSpace(string(out))
}

// GitInit makes Workspace a real Git repository on main with one commit,
// so `-w` launches make real worktrees (fakeclaude makeWorktree).
func (f *Fake) GitInit() {
	f.t.Helper()
	if err := os.RemoveAll(filepath.Join(f.Workspace, ".git")); err != nil {
		f.t.Fatal(err)
	}
	f.Git(f.Workspace, "init", "-q", "-b", "main")
	f.Git(f.Workspace, "commit", "-q", "--allow-empty", "-m", "init")
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

// fakePID: the fake's pids are made up, never this machine's processes; a
// stopped fake session's process is gone at once.
func fakePID(int) bool { return false }

// Adapter is an adapter on connection over the fake.
func (f *Fake) Adapter(connection int64) *claudecode.Adapter {
	return claudecode.New(claudecode.Options{Connection: connection, CLI: f.CLI(), Identity: f.ConfigDir(), RosterTTL: time.Nanosecond, LaunchWait: 2 * time.Second, ProcessAlive: fakePID})
}

// Hooks opens a hook receiver in a temporary folder, closed when the test
// ends.
func (f *Fake) Hooks() *claudecode.Hooks {
	f.t.Helper()
	h, err := claudecode.OpenHooks(claudecode.HooksOptions{Dir: filepath.Join(f.Home, "stage-claude")})
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = h.Close() })
	return h
}

// HookedAdapter is Adapter with hooks: launches pass their settings file
// and Events pushes what hooks announce.
func (f *Fake) HookedAdapter(connection int64, h *claudecode.Hooks) *claudecode.Adapter {
	return claudecode.New(claudecode.Options{Connection: connection, CLI: f.CLI(), Identity: f.ConfigDir(), RosterTTL: time.Nanosecond, LaunchWait: 2 * time.Second, Hooks: h, ProcessAlive: fakePID,
		SettleDelays: []time.Duration{0, 20 * time.Millisecond, 40 * time.Millisecond, 80 * time.Millisecond}})
}

// Hook posts event for session id as Claude does: to the HTTP hook in the
// settings file the session was launched with, with that hook's headers
// and Claude's input shape; extra adds input fields (notification_type).
// It returns the HTTP status.
func (f *Fake) Hook(id, event string, extra map[string]any) int {
	f.t.Helper()
	return f.HookAs(id, id, event, extra)
}

// HookAs posts event naming session id through the hook settings session
// via was launched with (a hook for a session Stage does not know).
func (f *Fake) HookAs(via, id, event string, extra map[string]any) int {
	f.t.Helper()
	c := f.post(via, id, event, extra)
	if c.Wait(f.t, 5*time.Second).Err != nil {
		f.t.Fatal(c.Err)
	}
	return c.Status
}

// HookCall is one hook a fake session sent, as Claude sends it: an HTTP
// request that a PermissionRequest keeps open until Stage answers.
type HookCall struct {
	cancel context.CancelFunc
	done   chan struct{}
	Status int
	Body   string
	Err    error
}

// Wait is the hook's response (fails the test after d).
func (c *HookCall) Wait(t testing.TB, d time.Duration) *HookCall {
	t.Helper()
	select {
	case <-c.done:
	case <-time.After(d):
		t.Fatal("hook not answered")
	}
	return c
}

// Answered reports whether the hook got its response.
func (c *HookCall) Answered() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// Abandon closes the hook request (Claude's timeout, or the process ended).
func (c *HookCall) Abandon() { c.cancel(); <-c.done }

// Ask blocks session id on a permission prompt for tool (the session list
// shows it waiting) and sends its PermissionRequest hook, which stays open.
func (f *Fake) Ask(id, tool string, input any, suggestions []any) *HookCall {
	f.t.Helper()
	waitingFor := "permission prompt"
	if tool == "AskUserQuestion" {
		waitingFor = "input needed"
	}
	f.SetState(id, "blocked", "waiting", waitingFor)
	if suggestions == nil {
		suggestions = []any{}
	}
	return f.post(id, id, "PermissionRequest", map[string]any{"tool_name": tool, "tool_input": input, "permission_suggestions": suggestions})
}

// post sends event naming session id, asynchronously, to the HTTP hook in
// the settings file session via was launched with, with that hook's
// headers and Claude's input shape; extra adds input fields.
func (f *Fake) post(via, id, event string, extra map[string]any) *HookCall {
	f.t.Helper()
	s := f.Session(via)
	if s.Settings == "" {
		f.t.Fatalf("session %s was launched without --settings", via)
	}
	b, err := os.ReadFile(s.Settings)
	if err != nil {
		f.t.Fatal(err)
	}
	var settings struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type    string            `json:"type"`
				URL     string            `json:"url"`
				Headers map[string]string `json:"headers"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &settings); err != nil {
		f.t.Fatal(err)
	}
	if len(settings.Hooks[event]) == 0 || len(settings.Hooks[event][0].Hooks) == 0 || settings.Hooks[event][0].Hooks[0].Type != "http" {
		f.t.Fatalf("settings %s have no HTTP hook for %s", s.Settings, event)
	}
	hook := settings.Hooks[event][0].Hooks[0]
	input := map[string]any{
		"session_id": id, "transcript_path": fakestate.TranscriptPath(f.ProjectsDir(), s.Cwd, id), "cwd": s.Cwd,
		"hook_event_name": event, "permission_mode": "default",
	}
	for k, v := range extra {
		input[k] = v
	}
	body, err := json.Marshal(input)
	if err != nil {
		f.t.Fatal(err)
	}
	return Post(hook.URL, hook.Headers, body)
}

// Post sends a hook request with headers, asynchronously.
func Post(url string, headers map[string]string, body []byte) *HookCall {
	ctx, cancel := context.WithCancel(context.Background())
	c := &HookCall{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(c.done)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			c.Err = err
			return
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			c.Err = err
			return
		}
		defer res.Body.Close()
		b, err := io.ReadAll(res.Body)
		c.Status, c.Body, c.Err = res.StatusCode, string(b), err
	}()
	return c
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

// Rm runs `claude rm <short>` on a background session, as a user would.
func (f *Fake) Rm(id string) {
	f.t.Helper()
	res, err := f.CLI().Run(context.Background(), "", "rm", f.Session(id).ID)
	if err != nil || res.Code != 0 {
		f.t.Fatalf("claude rm: %v %+v", err, res)
	}
}

// Prompt appends a typed prompt to a background session's transcript (a
// new turn, e.g. from `claude attach`).
func (f *Fake) Prompt(id, prompt string) {
	f.t.Helper()
	if err := fakestate.AppendUser(f.ProjectsDir(), f.Session(id).Cwd, id, "cli", "bg", prompt, time.Now().UTC()); err != nil {
		f.t.Fatal(err)
	}
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
	if err := fakestate.AppendUser(f.ProjectsDir(), cwd, id, entrypoint, "", prompt, time.Now().UTC()); err != nil {
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
