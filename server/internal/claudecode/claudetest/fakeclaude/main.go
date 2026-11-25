// Command fakeclaude is a stand-in for the claude CLI in Stage's tests: it
// answers the commands the Claude Code adapter runs (--version, auth status,
// --bg, agents, stop) from a JSON state file (FAKE_CLAUDE_STATE) and writes
// transcripts like Claude's. claudetest drives it.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest/fakestate"
)

func main() {
	path := os.Getenv("FAKE_CLAUDE_STATE")
	if path == "" {
		fmt.Fprintln(os.Stderr, "fakeclaude: FAKE_CLAUDE_STATE is not set")
		os.Exit(2)
	}
	code := 0
	err := fakestate.Update(path, func(s *fakestate.State) error {
		var err error
		code, err = run(s, os.Args[1:])
		return err
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakeclaude:", err)
		os.Exit(2)
	}
	os.Exit(code)
}

func run(s *fakestate.State, args []string) (int, error) {
	s.Calls = append(s.Calls, fakestate.Call{Args: args, Env: claudeEnv(), Dir: cwd()})
	switch {
	case len(args) == 1 && args[0] == "--version":
		if s.HangVersion {
			time.Sleep(time.Minute)
		}
		fmt.Printf("%s (Claude Code)\n", s.Version)
		return 0, nil
	case len(args) >= 2 && args[0] == "auth" && args[1] == "status":
		b, _ := json.Marshal(map[string]any{"loggedIn": s.LoggedIn, "authMethod": "claude.ai", "configDirectory": s.ConfigDir, "projectsDirectory": s.ProjectsDir})
		fmt.Println(string(b))
		if !s.LoggedIn {
			return 1, nil
		}
		return 0, nil
	case len(args) >= 1 && args[0] == "agents":
		if s.FailAgents {
			fmt.Fprintln(os.Stderr, "daemon unavailable")
			return 1, nil
		}
		out := []fakestate.Entry{}
		for _, e := range s.Sessions {
			if !e.Removed {
				out = append(out, e.Entry)
			}
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		return 0, nil
	case len(args) == 2 && args[0] == "stop":
		for i := range s.Sessions {
			e := &s.Sessions[i]
			if !e.Removed && e.Kind == "background" && e.ID == args[1] {
				e.State, e.Status, e.WaitingFor, e.PID = "stopped", "", "", 0
				fmt.Printf("stopped %s\n", e.ID)
				return 0, nil
			}
		}
		fmt.Fprintf(os.Stderr, "No session matching '%s'. Run 'claude agents' to list running sessions.\n", args[1])
		return 1, nil
	case len(args) == 2 && args[0] == "rm":
		// Drops the entry from the list; the transcript stays.
		for i := range s.Sessions {
			e := &s.Sessions[i]
			if !e.Removed && e.Kind == "background" && e.ID == args[1] {
				e.Removed, e.PID, e.Status = true, 0, ""
				fmt.Printf("removed %s\n", e.ID)
				return 0, nil
			}
		}
		fmt.Fprintf(os.Stderr, "No session matching '%s'.\n", args[1])
		return 1, nil
	case len(args) >= 1 && args[0] == "--bg":
		return launch(s, args[1:])
	}
	fmt.Fprintf(os.Stderr, "fakeclaude: unsupported %q\n", args)
	return 2, nil
}

func launch(s *fakestate.State, args []string) (int, error) {
	var name, worktree, prompt string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-n", "--model", "--effort", "--permission-mode":
			if args[i] == "-n" && i+1 < len(args) {
				name = args[i+1]
			}
			i++
		case "-w":
			worktree = args[i+1]
			i++
		case "--":
			prompt = strings.Join(args[i+1:], " ")
			i = len(args)
		default:
			return 2, fmt.Errorf("unexpected argument %q", args[i])
		}
	}
	dir := cwd()
	if !s.Trusts(dir) {
		fmt.Fprintf(os.Stderr, "Workspace not trusted. Run claude in %s once and accept the trust prompt, then retry.\n", dir)
		return 1, nil
	}
	if s.Launch == fakestate.FailBeforeCreate {
		fmt.Fprintln(os.Stderr, "connection reset")
		return 1, nil
	}
	if worktree != "" {
		dir = filepath.Join(dir, ".claude", "worktrees", worktree)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return 2, err
		}
	}
	id := newUUID()
	now := time.Now().UTC()
	if name == "" {
		name = "fake session"
	}
	s.Sessions = append(s.Sessions, fakestate.Session{Entry: fakestate.Entry{
		PID: 4242, ID: id[:8], Cwd: dir, Kind: "background", StartedAt: now.UnixMilli(),
		SessionID: id, Name: name, Status: "busy", State: "working",
	}})
	if err := fakestate.AppendUser(s.ProjectsDir, dir, id, "cli", "bg", prompt, now); err != nil {
		return 2, err
	}
	if s.Launch == fakestate.CrashAfterCreate {
		fmt.Fprintln(os.Stderr, "lost connection to the background service")
		return 1, nil
	}
	fmt.Printf("backgrounded · %s · %s\n\n  claude agents          list sessions\n", id[:8], name)
	return 0, nil
}

func claudeEnv() map[string]string {
	out := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "CLAUDE") || strings.HasPrefix(k, "T3") || k == "PATH" || strings.HasPrefix(k, "STAGE_PROBE") {
			out[k] = v
		}
	}
	return out
}

func cwd() string {
	d, _ := os.Getwd()
	return d
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
