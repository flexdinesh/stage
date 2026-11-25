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
	"os/exec"
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
	var name, worktree, settings, prompt string
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
		case "--add-dir":
			if i+1 >= len(args) {
				return 2, fmt.Errorf("--add-dir needs a folder")
			}
			if fi, err := os.Stat(args[i+1]); err != nil || !fi.IsDir() {
				fmt.Fprintf(os.Stderr, "Path %s was not found.\n", args[i+1])
				return 1, nil
			}
			// Claude refuses an added folder outside every trusted one
			// (claude 2.1.332).
			if !s.Trusts(args[i+1]) {
				fmt.Fprintf(os.Stderr, "Added folder not trusted: %s. Leave out its --add-dir or, if you trust it and everything in its repository or below it, start `claude` there once and accept the trust prompt.\n", args[i+1])
				return 1, nil
			}
			i++
		case "--settings":
			if i+1 >= len(args) {
				return 2, fmt.Errorf("--settings needs a file")
			}
			// Claude keeps one --settings value: a second would drop
			// the first (the hooks or the worktree base).
			if settings != "" {
				return 2, fmt.Errorf("--settings given twice")
			}
			settings = args[i+1]
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
	branch := ""
	if worktree != "" {
		var err error
		if dir, branch, err = makeWorktree(dir, worktree, settings); err != nil {
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
	}, Settings: settings})
	if err := fakestate.AppendUserOn(s.ProjectsDir, dir, id, "cli", "bg", prompt, branch, now); err != nil {
		return 2, err
	}
	if s.Launch == fakestate.CrashAfterCreate {
		fmt.Fprintln(os.Stderr, "lost connection to the background service")
		return 1, nil
	}
	fmt.Printf("backgrounded · %s · %s\n\n  claude agents          list sessions\n", id[:8], name)
	return 0, nil
}

// makeWorktree is `-w name` as Claude does it (docs/adr/0005 E5, E10): in a
// Git checkout, a worktree of the main checkout at
// <main>/.claude/worktrees/<name> on a new branch worktree-<name>, from the
// launch checkout's HEAD when settings set worktree.baseRef "head", else
// from the main checkout's HEAD (standing in for origin's default branch).
// Outside Git it is a plain folder.
func makeWorktree(cwd, name, settings string) (string, string, error) {
	git := func(dir string, args ...string) (string, error) {
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		out, err := c.Output()
		return strings.TrimSpace(string(out)), err
	}
	common, err := git(cwd, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		dir := filepath.Join(cwd, ".claude", "worktrees", name)
		return dir, "", os.MkdirAll(dir, 0o755)
	}
	main := filepath.Dir(common)
	from := main
	var s struct {
		Worktree struct {
			BaseRef string `json:"baseRef"`
		} `json:"worktree"`
	}
	if settings != "" {
		raw := []byte(settings)
		if !strings.HasPrefix(strings.TrimSpace(settings), "{") {
			// A settings file, as Claude takes either.
			if raw, err = os.ReadFile(settings); err != nil {
				return "", "", fmt.Errorf("--settings: %w", err)
			}
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", "", fmt.Errorf("--settings: %w", err)
		}
	}
	if s.Worktree.BaseRef == "head" {
		from = cwd
	}
	base, err := git(from, "rev-parse", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("resolve HEAD in %s: %w", from, err)
	}
	dir, branch := filepath.Join(main, ".claude", "worktrees", name), "worktree-"+name
	if _, err := git(main, "worktree", "add", "-q", "-b", branch, dir, base); err != nil {
		return "", "", fmt.Errorf("git worktree add: %w", err)
	}
	return dir, branch, nil
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
