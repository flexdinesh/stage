package claudecode_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// TestLiveClaude runs the adapter against the real claude CLI with the
// user's own login (`mise run claude:live`, STAGE_TEST_CLAUDE = the claude
// executable). It uses haiku, two scratch folders it marks trusted in
// Claude's state file, and removes its sessions, transcripts and trust
// entries afterwards.
func TestLiveClaude(t *testing.T) {
	bin := os.Getenv("STAGE_TEST_CLAUDE")
	if bin == "" {
		t.Skip("set STAGE_TEST_CLAUDE to the claude executable")
	}
	if !filepath.IsAbs(bin) {
		t.Fatal("STAGE_TEST_CLAUDE must be an absolute path")
	}
	home, _ := os.UserHomeDir()
	cli := &claudecode.CLI{Bin: bin, Home: home, Scope: claudecode.ScopeAvailable()}
	ctx := t.Context()
	au, err := claudecode.ReadAuth(ctx, cli)
	if err != nil || !au.LoggedIn {
		t.Fatalf("claude must be logged in: %+v %v", au, err)
	}
	state := filepath.Join(home, ".claude.json")
	if filepath.Clean(au.ConfigDirectory) != filepath.Join(home, ".claude") {
		state = filepath.Join(au.ConfigDirectory, ".claude.json")
	}

	scratch, err := os.MkdirTemp("", "stage-cc-live-")
	if err != nil {
		t.Fatal(err)
	}
	scratch, _ = filepath.EvalSymlinks(scratch)
	plain, repo := filepath.Join(scratch, "plain"), filepath.Join(scratch, "repo")
	for _, d := range []string{plain, repo} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=stage", "-c", "user.email=stage@example.invalid"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "init")

	a := claudecode.New(claudecode.Options{Connection: 1, CLI: cli, Identity: au.ConfigDirectory})
	var shorts []string
	t.Cleanup(func() { liveCleanup(t, cli, state, scratch, []string{plain, repo}, shorts) })
	trust(t, state, plain, repo)

	if h := a.Health(ctx); !h.Available {
		t.Fatalf("health %+v", h)
	}
	daemonBefore := daemonPIDs()
	launch := func(req, dir, kind, permission, prompt string) (execution.Intent, execution.Observation) {
		t.Helper()
		marker := execution.LaunchMarker(req)
		in := execution.Intent{RequestID: req, ConnectionID: 1, Project: dir, Directory: dir, Title: "Stage live " + req, Prompt: prompt + "\n\n" + marker, Marker: marker,
			Workspace: execution.Workspace{Kind: kind}, Config: execution.Config{Model: "haiku", Permission: permission}}
		o, err := a.Launch(ctx, in, execution.Checkpoint{}, nil)
		if err != nil {
			t.Fatalf("launch %s: %v", req, err)
		}
		if s, ok := strings.CutPrefix(o.Links.Command, "claude attach "); ok {
			shorts = append(shorts, s)
		}
		return in, o
	}
	await := func(ref execution.SessionRef, want string) execution.Observation {
		t.Helper()
		deadline := time.Now().Add(3 * time.Minute)
		for {
			o, err := a.Observe(ctx, ref)
			if err == nil && o.State == want {
				return o
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s never reached %s: %+v %v", ref.ExternalSessionID, want, o, err)
			}
			time.Sleep(2 * time.Second)
		}
	}

	// Root: a reply, found again by its marker; the first prompt opens it.
	in, o := launch("live-root", plain, "root", "", "Without using any tools, reply with exactly: stage live ok")
	done := await(o.Session, "done")
	if done.Reply == nil || !strings.Contains(done.Reply.Text, "stage live ok") || done.Project != plain {
		t.Fatalf("done %+v", done)
	}
	if c, err := a.Correlate(ctx, in); err != nil || c.Session != o.Session {
		t.Fatalf("correlate %+v %v", c, err)
	}
	if op, err := a.Opening(ctx, o.Session, false); err != nil || !strings.Contains(op.Text, "stage live ok") {
		t.Fatalf("opening %+v %v", op, err)
	}
	if after := daemonPIDs(); len(daemonBefore) == 0 && cli.Scope && len(after) > 0 {
		self, _ := os.ReadFile("/proc/self/cgroup")
		for _, pid := range after {
			cg, _ := os.ReadFile("/proc/" + pid + "/cgroup")
			if bytes.Equal(cg, self) {
				t.Fatalf("Claude's daemon %s shares Stage's cgroup %s", pid, cg)
			}
		}
	}

	// `claude rm` of the done session is the archived closure: no longer
	// listed, reply kept (the session list is reused for 2 seconds).
	short, _ := strings.CutPrefix(done.Links.Command, "claude attach ")
	if res, err := cli.Run(ctx, "", "rm", short); err != nil || res.Code != 0 {
		t.Fatalf("rm %s: %v %+v", short, err, res)
	}
	removed := await(o.Session, "idle")
	if removed.Closed == nil || removed.Closed.Signal != execution.ClosedArchived || removed.Reply == nil || !strings.Contains(removed.Reply.Text, "stage live ok") {
		t.Fatalf("removed %+v", removed)
	}

	// A permission prompt is needs input; stop interrupts it.
	_, o = launch("live-block", plain, "root", "manual", "Use the Bash tool to run exactly: touch stage-live-blocked.txt. Then reply done.")
	await(o.Session, "needs_input")
	if err := a.Stop(ctx, o.Session); err != nil {
		t.Fatal(err)
	}
	await(o.Session, "interrupted")

	// A worktree per attempt, in the repository.
	_, o = launch("live-wt", repo, "worktree", "", "Without using any tools, reply with exactly: stage wt ok")
	done = await(o.Session, "done")
	if done.Directory != claudecode.WorktreeDir(repo, claudecode.WorktreeName("live-wt")) || done.Project != repo || done.Branch != "worktree-"+claudecode.WorktreeName("live-wt") {
		t.Fatalf("worktree %+v", done)
	}
}

// trust marks dirs trusted in Claude's state file (what accepting the trust
// prompt records), keeping everything else.
func trust(t *testing.T, state string, dirs ...string) {
	t.Helper()
	editState(t, state, func(projects map[string]any) {
		for _, d := range dirs {
			projects[d] = map[string]any{"hasTrustDialogAccepted": true}
		}
	})
}

func editState(t *testing.T, state string, fn func(map[string]any)) {
	t.Helper()
	fi, err := os.Stat(state)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	projects, _ := m["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
	}
	fn(projects)
	m["projects"] = projects
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	tmp := state + ".stage-live"
	if err := os.WriteFile(tmp, out, fi.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, state); err != nil {
		t.Fatal(err)
	}
}

// liveCleanup removes the sessions (and the worktree), the folders'
// transcripts and state entries (claude purge), and the scratch folders.
func liveCleanup(t *testing.T, cli *claudecode.CLI, state, scratch string, dirs, shorts []string) {
	// t.Context() has ended by the time cleanup runs.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, s := range shorts {
		for try := 0; try < 10; try++ {
			res, err := cli.Run(ctx, "", "rm", s)
			if err == nil && (res.Code == 0 || strings.Contains(res.Stderr+res.Stdout, "No session matching")) {
				break
			}
			time.Sleep(time.Second)
		}
	}
	for _, d := range dirs {
		if res, err := cli.Run(ctx, "", "purge", "-y", d); err != nil || res.Code != 0 {
			t.Logf("purge %s: %v %+v", d, err, res)
		}
	}
	b, err := os.ReadFile(state)
	if err == nil {
		var m struct {
			Projects map[string]json.RawMessage `json:"projects"`
		}
		if json.Unmarshal(b, &m) == nil {
			for p := range m.Projects {
				if p == scratch || strings.HasPrefix(p, scratch+"/") {
					t.Logf("purge left %s in %s; removing it", p, state)
					editState(t, state, func(projects map[string]any) {
						for k := range projects {
							if k == scratch || strings.HasPrefix(k, scratch+"/") {
								delete(projects, k)
							}
						}
					})
					break
				}
			}
		}
	}
	if err := os.RemoveAll(scratch); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Logf("remove %s: %v", scratch, err)
	}
}

// daemonPIDs are the running Claude daemons.
func daemonPIDs() []string {
	out, _ := exec.Command("pgrep", "-f", "claude daemon run").Output()
	return strings.Fields(string(out))
}
