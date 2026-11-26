package claudecode_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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

	// Hooks: every launch passes the settings file; Events records what
	// they push.
	// Images go to Stage's own folder, outside every trusted one, as under
	// the data dir.
	imageRoot := filepath.Join(scratch, "stage-claude", "images")
	hooks, err := claudecode.OpenHooks(claudecode.HooksOptions{Dir: filepath.Join(scratch, "stage-claude"), ImageRoot: imageRoot})
	if err != nil {
		t.Fatal(err)
	}
	a := claudecode.New(claudecode.Options{Connection: 1, CLI: cli, Identity: au.ConfigDirectory, ImageRoot: imageRoot, Hooks: hooks})
	var shorts []string
	t.Cleanup(func() { liveCleanup(t, cli, state, scratch, []string{plain, repo}, shorts) })
	t.Cleanup(func() { _ = hooks.Close() })
	trust(t, state, plain, repo)
	var pushMu sync.Mutex
	pushes := map[execution.SessionRef][]string{}
	eventsCtx, stopEvents := context.WithCancel(ctx)
	t.Cleanup(stopEvents)
	go func() {
		_ = a.Events(eventsCtx, func(_ context.Context, o execution.Observation) error {
			pushMu.Lock()
			pushes[o.Session] = append(pushes[o.Session], o.State)
			pushMu.Unlock()
			return nil
		})
	}()
	// pushed checks that a hook pushed ref in state want (Events, not a
	// poll).
	pushed := func(ref execution.SessionRef, want string) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			pushMu.Lock()
			got := slices.Clone(pushes[ref])
			pushMu.Unlock()
			if slices.Contains(got, want) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("no %s pushed for %s (pushed %v; last hook %+v)", want, ref.ExternalSessionID, got, hooks.Status())
			}
			time.Sleep(200 * time.Millisecond)
		}
	}

	if h := a.Health(ctx); !h.Available {
		t.Fatalf("health %+v", h)
	}
	daemonBefore := daemonPIDs()
	launch := func(req, dir, kind, permission, prompt string, images ...execution.Image) (execution.Intent, execution.Observation) {
		t.Helper()
		marker := execution.LaunchMarker(req)
		in := execution.Intent{RequestID: req, ConnectionID: 1, Project: dir, Directory: dir, Title: "Stage live " + req, Prompt: prompt + "\n\n" + marker, Marker: marker,
			Workspace: execution.Workspace{Kind: kind}, Config: execution.Config{Model: "haiku", Permission: permission}, Images: images}
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
	pushed(o.Session, "done")
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

	// A follow-up wakes the done session (its process still lives) under
	// its own id, never a copy; the new turn's reply replaces the last.
	if err := a.Send(ctx, o.Session, execution.Message{Text: "Without using any tools, reply with exactly: stage follow-up ok"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	pushed(o.Session, "running")
	followed := await(o.Session, "done")
	if followed.Reply == nil || !strings.Contains(followed.Reply.Text, "stage follow-up ok") || followed.ExecutionID == done.ExecutionID || followed.Links.Command != done.Links.Command {
		t.Fatalf("follow-up %+v", followed)
	}
	if es, err := cli.Run(ctx, "", "agents", "--json", "--all"); err != nil || strings.Count(es.Stdout, filepath.Base(plain)) != 1 {
		t.Fatalf("a copy started: %v %s", err, es.Stdout)
	}

	// `claude rm` of the done session is the archived closure: no longer
	// listed, reply kept (the session list is reused for 2 seconds).
	short, _ := strings.CutPrefix(done.Links.Command, "claude attach ")
	if res, err := cli.Run(ctx, "", "rm", short); err != nil || res.Code != 0 {
		t.Fatalf("rm %s: %v %+v", short, err, res)
	}
	removed := await(o.Session, "idle")
	if removed.Closed == nil || removed.Closed.Signal != execution.ClosedArchived || removed.Reply == nil || !strings.Contains(removed.Reply.Text, "stage follow-up ok") {
		t.Fatalf("removed %+v", removed)
	}

	// An image reaches Claude as a file it reads without a permission
	// prompt (manual mode): its folder is under Stage's image root, outside
	// the trusted workspace, and the settings' Read rule allows it. The
	// launch removes what earlier versions wrote in the project.
	legacy := claudecode.LegacyImageRoot(plain)
	if err := os.MkdirAll(filepath.Join(legacy, "stage-old"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, ".gitignore"), []byte("# Stage: task images for Claude Code launches\n*\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 220, A: 255}}, image.Point{}, draw.Src)
	var red bytes.Buffer
	if err := png.Encode(&red, img); err != nil {
		t.Fatal(err)
	}
	_, o = launch("live-img", plain, "root", "manual", "Look at the attached image. Reply with exactly one lowercase word: its color.",
		execution.Image{Name: "swatch.png", MimeType: "image/png", Data: red.Bytes()})
	if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old project images left: %v", err)
	}
	for deadline := time.Now().Add(3 * time.Minute); ; time.Sleep(2 * time.Second) {
		cur, err := a.Observe(ctx, o.Session)
		if err == nil && cur.State == "needs_input" {
			rs, _ := a.Requests(ctx, o.Session)
			t.Fatalf("reading the image asked for permission: %+v %+v", cur, rs)
		}
		if err == nil && cur.State == "done" {
			done = cur
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("image session never finished: %+v %v", cur, err)
		}
	}
	if done.Reply == nil || !strings.Contains(strings.ToLower(done.Reply.Text), "red") {
		t.Fatalf("image reply %+v", done.Reply)
	}
	t.Logf("image reply: %q", done.Reply.Text)
	ws, err := a.Workspaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range ws {
		if strings.HasPrefix(w.WorkspaceRoot, imageRoot) {
			t.Fatalf("the images folder became a workspace: %+v", w)
		}
	}

	// A permission prompt is needs input, listed as a request through the
	// PermissionRequest hook; Allow from Stage runs the tool.
	_, o = launch("live-allow", plain, "root", "manual", "Use the Bash tool to run exactly: touch stage-live-allowed.txt. Then reply done.")
	await(o.Session, "needs_input")
	var rs []execution.Request
	for deadline := time.Now().Add(30 * time.Second); len(rs) == 0; time.Sleep(time.Second) {
		if rs, err = a.Requests(ctx, o.Session); err != nil || time.Now().After(deadline) {
			t.Fatalf("requests %+v %v", rs, err)
		}
	}
	if rs[0].Kind != "approval" || !strings.Contains(rs[0].Preview, "stage-live-allowed.txt") {
		t.Fatalf("request %+v", rs[0])
	}
	if err := a.Respond(ctx, o.Session, rs[0].ID, execution.Response{Option: "allow"}); err != nil {
		t.Fatal(err)
	}
	await(o.Session, "done")
	if _, err := os.Stat(filepath.Join(plain, "stage-live-allowed.txt")); err != nil {
		t.Fatalf("allowed command did not run: %v", err)
	}

	// Another prompt; stop interrupts it.
	_, o = launch("live-block", plain, "root", "manual", "Use the Bash tool to run exactly: touch stage-live-blocked.txt. Then reply done.")
	await(o.Session, "needs_input")
	pushed(o.Session, "needs_input")
	// The attention feed lists it (discovery reads the same session list).
	refs, err := a.Attention(ctx)
	if err != nil || !slices.Contains(refs, o.Session) {
		t.Fatalf("attention %v %v, want %v", refs, err, o.Session)
	}
	if err := a.Stop(ctx, o.Session); err != nil {
		t.Fatal(err)
	}
	await(o.Session, "interrupted")
	pushed(o.Session, "interrupted") // SessionEnd

	// A worktree per attempt, in the repository.
	_, o = launch("live-wt", repo, "worktree", "", "Without using any tools, reply with exactly: stage wt ok")
	done = await(o.Session, "done")
	if done.Directory != claudecode.WorktreeDir(repo, claudecode.WorktreeName("live-wt")) || done.Project != repo || done.Branch != "worktree-"+claudecode.WorktreeName("live-wt") || !done.Base.Available {
		t.Fatalf("worktree %+v", done)
	}

	// A predecessor base: Claude's next worktree starts at the predecessor
	// worktree's commit (docs/adr/0005 E10).
	gitIn := func(dir string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=stage", "-c", "user.email=stage@example.invalid"}, args...)...).Output()
		if err != nil {
			t.Fatalf("git %v in %s: %v", args, dir, err)
		}
		return strings.TrimSpace(string(out))
	}
	gitIn(done.Directory, "commit", "-q", "--allow-empty", "-m", "predecessor")
	tip := gitIn(done.Directory, "rev-parse", "HEAD")
	marker := execution.LaunchMarker("live-dep")
	dep := execution.Intent{RequestID: "live-dep", ConnectionID: 1, Project: repo, Directory: repo, Title: "Stage live live-dep", Prompt: "Without using any tools, reply with exactly: stage dep ok\n\n" + marker, Marker: marker,
		Workspace: execution.Workspace{Kind: "worktree", Base: done.Branch}, Config: execution.Config{Model: "haiku"}}
	o, err = a.Launch(ctx, dep, execution.Checkpoint{}, nil)
	if err != nil {
		t.Fatalf("launch live-dep: %v", err)
	}
	if s, ok := strings.CutPrefix(o.Links.Command, "claude attach "); ok {
		shorts = append(shorts, s)
	}
	depDone := await(o.Session, "done")
	if depDone.Directory != claudecode.WorktreeDir(repo, claudecode.WorktreeName("live-dep")) || depDone.Project != repo {
		t.Fatalf("dependent %+v", depDone)
	}
	if head := gitIn(depDone.Directory, "rev-parse", "HEAD"); head != tip {
		t.Fatalf("dependent starts at %s, want the predecessor's %s", head, tip)
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
	// Newest first: a dependent launched from a predecessor's worktree
	// keeps Claude from removing that worktree until it is gone.
	rm := func(s string, tries int) (bool, string) {
		last := ""
		for try := 0; try < tries; try++ {
			res, err := cli.Run(ctx, "", "rm", s)
			if err == nil && (res.Code == 0 || strings.Contains(res.Stderr+res.Stdout, "No session matching")) {
				return true, ""
			}
			last = res.Stderr + res.Stdout
			if err != nil {
				last = err.Error()
			}
			time.Sleep(time.Second)
		}
		return false, last
	}
	for i := len(shorts) - 1; i >= 0; i-- {
		s := shorts[i]
		ok, out := rm(s, 10)
		if !ok {
			// Claude keeps a worktree that has commits (the predecessor's);
			// with the scratch worktrees gone it removes the session.
			t.Logf("claude rm %s: %s; removing the scratch worktrees", s, out)
			for _, d := range dirs {
				_ = os.RemoveAll(filepath.Join(d, ".claude", "worktrees"))
			}
			ok, out = rm(s, 3)
		}
		if !ok {
			t.Errorf("claude rm %s kept the session (%s); remove it by hand", s, out)
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
