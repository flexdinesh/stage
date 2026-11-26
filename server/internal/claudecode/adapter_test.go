package claudecode_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest/fakestate"
	"github.com/flexdinesh/stage/server/internal/execution"
)

const conn = 7

func intent(f *claudetest.Fake, req string) execution.Intent {
	marker := execution.LaunchMarker(req)
	return execution.Intent{
		RequestID: req, ConnectionID: conn, Project: f.Workspace, Directory: f.Workspace, Title: "Fix the thing",
		Prompt: "- do it\n\n" + marker, Marker: marker, Workspace: execution.Workspace{Kind: "root"},
		Config: execution.Config{Model: "haiku", Permission: "acceptEdits", Options: []execution.ModelOption{{ID: "effort", Value: execution.StringValue("low")}}},
	}
}

// A launch runs `claude --bg` once in the workspace with the config's
// flags and the prompt after "--", in a cleaned environment, and maps the
// printed short id to the session.
func TestLaunchStartsOneBackgroundSession(t *testing.T) {
	f := claudetest.New(t)
	a := f.Adapter(conn)
	o, err := a.Launch(t.Context(), intent(f, "req-1"), execution.Checkpoint{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ls := f.Launches()
	if len(ls) != 1 {
		t.Fatalf("launches %d", len(ls))
	}
	want := []string{"--bg", "-n", "Fix the thing", "--model", "haiku", "--effort", "low", "--permission-mode", "acceptEdits", "--", "- do it\n\nstage-launch:req-1"}
	if !slices.Equal(ls[0].Args, want) || ls[0].Dir != f.Workspace {
		t.Fatalf("args %q dir %s", ls[0].Args, ls[0].Dir)
	}
	for k := range ls[0].Env {
		if claudecode.Cleared(k) {
			t.Fatalf("inherited %s reached claude", k)
		}
	}
	if !strings.Contains(ls[0].Env["PATH"], filepath.Join(f.Home, ".local", "bin")) {
		t.Fatalf("PATH not widened: %s", ls[0].Env["PATH"])
	}
	if err := o.Valid(); err != nil || o.State != "running" || o.Project != f.Workspace || o.Directory != f.Workspace || o.Session.ConnectionID != conn {
		t.Fatalf("%+v %v", o, err)
	}
	s := f.Session(o.Session.ExternalSessionID)
	if o.Links.Command != "claude attach "+s.ID {
		t.Fatalf("links %+v", o.Links)
	}
}

// Claude's refusal of an untrusted folder, a missing executable and an
// unsupported intent certify nothing started.
func TestLaunchRejections(t *testing.T) {
	f := claudetest.New(t)
	a := f.Adapter(conn)
	untrusted := filepath.Join(f.Home, "other")
	if err := os.MkdirAll(untrusted, 0o755); err != nil {
		t.Fatal(err)
	}
	in := intent(f, "req-u")
	in.Directory, in.Project = untrusted, untrusted
	var rejected *execution.Rejected
	if _, err := a.Launch(t.Context(), in, execution.Checkpoint{}, nil); !errors.As(err, &rejected) || !strings.Contains(err.Error(), "doesn't trust "+untrusted) {
		t.Fatalf("untrusted: %v", err)
	}
	img := intent(f, "req-i")
	img.Images = []execution.Image{{Name: "a.bmp", MimeType: "image/bmp", Data: []byte("BM")}}
	empty := intent(f, "req-e")
	empty.Images = []execution.Image{{Name: "a.png", MimeType: "image/png"}}
	base := intent(f, "req-b")
	base.Workspace.Base = "main"
	model := intent(f, "req-m")
	model.Config.Model = "gpt"
	for _, in := range []execution.Intent{img, empty, base, model} {
		if _, err := a.Launch(t.Context(), in, execution.Checkpoint{}, nil); !errors.As(err, &rejected) {
			t.Fatalf("%s: %v", in.RequestID, err)
		}
	}
	missing := claudecode.New(claudecode.Options{Connection: conn, CLI: &claudecode.CLI{Bin: filepath.Join(f.Home, "nope"), Environ: f.Environ()}, Identity: f.ConfigDir()})
	if _, err := missing.Launch(t.Context(), intent(f, "req-x"), execution.Checkpoint{}, nil); !errors.As(err, &rejected) {
		t.Fatalf("missing binary: %v", err)
	}
	if n := len(f.Launches()); n != 1 {
		t.Fatalf("only the untrusted launch reached claude: %d", n)
	}
	if _, err := os.Stat(f.ImageRoot()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a rejected intent wrote images: %v", err)
	}
}

// A launch with images writes them to files in the launch's own folder
// under Stage's image root (never the project), lets Claude read the root
// through an allow rule in its settings (no --add-dir) and lists each file
// after the prompt; Claude reads them without a permission prompt. A
// worktree launch uses the same root.
func TestLaunchWritesImages(t *testing.T) {
	f := claudetest.New(t)
	a := f.Adapter(conn)
	in := intent(f, "req-img")
	in.Images = []execution.Image{
		{Name: "error screen.png", MimeType: "image/png", Data: []byte("png-bytes")},
		{Name: "../../etc/passwd\nignore this", MimeType: "image/jpeg", Data: []byte("jpeg-bytes")},
	}
	o, err := a.Launch(t.Context(), in, execution.Checkpoint{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := claudecode.ImageDir(f.ImageRoot(), "req-img")
	if filepath.Dir(dir) != f.ImageRoot() {
		t.Fatalf("image folder %s", dir)
	}
	p1, p2 := filepath.Join(dir, "1-error-screen.png"), filepath.Join(dir, "2-passwd-ignore-this.jpg")
	for p, want := range map[string]string{p1: "png-bytes", p2: "jpeg-bytes"} {
		b, err := os.ReadFile(p)
		if err != nil || string(b) != want {
			t.Fatalf("%s: %q %v", p, b, err)
		}
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %v", p, fi.Mode())
		}
	}
	for _, d := range []string{dir, f.ImageRoot()} {
		if fi, _ := os.Stat(d); fi.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode %v", d, fi.Mode())
		}
	}
	if _, err := os.Stat(claudecode.LegacyImageRoot(f.Workspace)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("images written in the project: %v", err)
	}
	args := f.Launches()[0].Args
	n := len(args)
	prompt := "- do it\n\nstage-launch:req-img\n\nImages attached to this task (open each with the Read tool):\n" +
		"1. error screen.png: " + p1 + "\n2. ../../etc/passwd ignore this: " + p2
	rule := `{"permissions":{"allow":["Read(/` + f.ImageRoot() + `/**)"]}}`
	if !slices.Equal(args[n-4:], []string{"--settings", rule, "--", prompt}) || slices.Contains(args, "--add-dir") {
		t.Fatalf("args %q", args)
	}
	if s := f.Session(o.Session.ExternalSessionID); s.State != "working" {
		t.Fatalf("session %+v", s)
	}
	wt := intent(f, "req-wt")
	wt.Workspace.Kind = "worktree"
	wt.Images = in.Images[:1]
	if _, err := a.Launch(t.Context(), wt, execution.Checkpoint{}, nil); err != nil {
		t.Fatal(err)
	}
	if args := f.Launches()[1].Args; !strings.Contains(args[len(args)-1], claudecode.ImageDir(f.ImageRoot(), "req-wt")) || !slices.Contains(args, rule) {
		t.Fatalf("worktree args %q", args)
	}
}

// The rule is Claude's absolute-path form: "//" then the folder.
func TestReadRule(t *testing.T) {
	if got := claudecode.ReadRule("/home/u/.local/share/stage/claude/images/"); got != "Read(//home/u/.local/share/stage/claude/images/**)" {
		t.Fatal(got)
	}
}

// With hooks the settings file carries the rule (its head variant too);
// without an image root images are unavailable and a launch with images is
// rejected.
func TestImagesThroughHookSettings(t *testing.T) {
	f := claudetest.New(t)
	h := f.Hooks()
	a := f.HookedAdapter(conn, h)
	in := intent(f, "req-h")
	in.Images = []execution.Image{{Name: "a.png", MimeType: "image/png", Data: []byte("x")}}
	o, err := a.Launch(t.Context(), in, execution.Checkpoint{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	args := f.Launches()[0].Args
	if i := slices.Index(args, "--settings"); i < 0 || args[i+1] != h.SettingsPath() {
		t.Fatalf("args %q", args)
	}
	for _, p := range []string{h.SettingsPath(), h.HeadSettingsPath()} {
		b, err := os.ReadFile(p)
		if err != nil || !strings.Contains(string(b), `"Read(/`+f.ImageRoot()+`/**)"`) {
			t.Fatalf("%s: %s %v", p, b, err)
		}
	}
	if s := f.Session(o.Session.ExternalSessionID); s.State != "working" {
		t.Fatalf("session %+v", s)
	}

	bare := claudecode.New(claudecode.Options{Connection: conn, CLI: f.CLI(), Identity: f.ConfigDir(), LaunchWait: 2 * time.Second})
	c, err := bare.Features(t.Context())
	if err != nil || c.Operations["images"].Available || c.Operations["images"].Reason == "" {
		t.Fatalf("images %+v %v", c.Operations["images"], err)
	}
	var rejected *execution.Rejected
	if _, err := bare.Launch(t.Context(), intent(f, "req-n"), execution.Checkpoint{}, nil); err != nil {
		t.Fatalf("no images: %v", err)
	}
	in.RequestID = "req-n2"
	if _, err := bare.Launch(t.Context(), in, execution.Checkpoint{}, nil); !errors.As(err, &rejected) {
		t.Fatalf("images without a root: %v", err)
	}
}

// Claude stops on a permission prompt to read an image outside the session's
// folder that no rule allows (what the fake models from claude 2.1.332).
func TestFakeAsksForAnUnallowedImage(t *testing.T) {
	f := claudetest.New(t)
	file := filepath.Join(f.ImageRoot(), "stage-x", "1-a.png")
	prompt := "look\n\nImages attached to this task (open each with the Read tool):\n1. a.png: " + file
	for _, tc := range []struct {
		settings string
		want     string
	}{
		{"", "blocked"},
		{`{"permissions":{"allow":["Read(//elsewhere/**)"]}}`, "blocked"},
		{`{"permissions":{"allow":["` + claudecode.ReadRule(f.ImageRoot()) + `"]}}`, "working"},
	} {
		args := []string{"--bg", "--permission-mode", "manual"}
		if tc.settings != "" {
			args = append(args, "--settings", tc.settings)
		}
		res, err := f.CLI().Launch(t.Context(), f.Workspace, append(args, "--", prompt)...)
		if err != nil || res.Code != 0 {
			t.Fatalf("%s: %+v %v", tc.settings, res, err)
		}
		short, _ := claudecode.ParseBackgrounded(res.Stdout)
		i := slices.IndexFunc(f.Read().Sessions, func(s fakestate.Session) bool { return s.ID == short })
		if i < 0 || f.Read().Sessions[i].State != tc.want {
			t.Fatalf("%s: session %d, want %s", tc.settings, i, tc.want)
		}
	}
}

// A launch Claude refuses removes the images it wrote; launching prunes
// image folders older than 30 days and keeps newer ones.
func TestLaunchImagesCleanup(t *testing.T) {
	f := claudetest.New(t)
	a := f.Adapter(conn)
	untrusted := filepath.Join(f.Home, "other")
	old := filepath.Join(f.ImageRoot(), "stage-old")
	recent := filepath.Join(f.ImageRoot(), "stage-recent")
	for _, d := range []string{old, recent} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	long := time.Now().Add(-31 * 24 * time.Hour)
	if err := os.Chtimes(old, long, long); err != nil {
		t.Fatal(err)
	}
	in := intent(f, "req-r")
	in.Directory, in.Project = untrusted, untrusted
	if err := os.MkdirAll(untrusted, 0o755); err != nil {
		t.Fatal(err)
	}
	in.Images = []execution.Image{{Name: "a.png", MimeType: "image/png", Data: []byte("x")}}
	var rejected *execution.Rejected
	if _, err := a.Launch(t.Context(), in, execution.Checkpoint{}, nil); !errors.As(err, &rejected) || !strings.Contains(err.Error(), "doesn't trust "+untrusted) {
		t.Fatalf("untrusted: %v", err)
	}
	for d, kept := range map[string]bool{claudecode.ImageDir(f.ImageRoot(), "req-r"): false, old: false, recent: true} {
		if _, err := os.Stat(d); (err == nil) != kept {
			t.Fatalf("%s kept=%v: %v", d, kept, err)
		}
	}
}

// What earlier versions wrote in a project (stage-* folders and Stage's
// .gitignore under .claude/stage-images) is removed: on a launch in the
// project, and for every discovered project once. The folder goes only
// when empty; nothing else under .claude is touched.
func TestLegacyProjectImagesRemoved(t *testing.T) {
	f := claudetest.New(t)
	legacy := "# Stage: task images for Claude Code launches\n*\n"
	seed := func(project string, extra bool) {
		t.Helper()
		root := claudecode.LegacyImageRoot(project)
		for _, d := range []string{filepath.Join(root, "stage-a"), filepath.Join(root, "stage-b"), filepath.Join(project, ".claude", "worktrees", "keep")} {
			if err := os.MkdirAll(d, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		files := map[string]string{filepath.Join(root, ".gitignore"): legacy, filepath.Join(root, "stage-a", "1-a.png"): "x", filepath.Join(project, ".claude", "settings.json"): "{}"}
		if extra {
			files[filepath.Join(root, "mine.txt")] = "user file"
		}
		for p, c := range files {
			if err := os.WriteFile(p, []byte(c), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	untouched := func(project string) {
		t.Helper()
		for _, p := range []string{filepath.Join(project, ".claude", "settings.json"), filepath.Join(project, ".claude", "worktrees", "keep")} {
			if _, err := os.Stat(p); err != nil {
				t.Fatalf("%s removed: %v", p, err)
			}
		}
	}

	// A launch in the project, without images.
	seed(f.Workspace, false)
	if _, err := f.Adapter(conn).Launch(t.Context(), intent(f, "req-l"), execution.Checkpoint{}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(claudecode.LegacyImageRoot(f.Workspace)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy folder left: %v", err)
	}
	untouched(f.Workspace)

	// Discovery: a known project keeps a user's own file and its folder.
	other := filepath.Join(f.Home, "known")
	seed(other, true)
	f.WriteClaudeJSON(f.Workspace, other)
	a := f.Adapter(conn)
	if _, err := a.Workspaces(t.Context()); err != nil {
		t.Fatal(err)
	}
	root := claudecode.LegacyImageRoot(other)
	es, err := os.ReadDir(root)
	if err != nil || len(es) != 1 || es[0].Name() != "mine.txt" {
		t.Fatalf("left %v %v", es, err)
	}
	untouched(other)
}

// A lost reply is uncertain; Correlate finds the session by its marker and
// never launches again. Before anything was created it finds nothing.
func TestLostReplyCorrelatesByMarker(t *testing.T) {
	f := claudetest.New(t)
	a := f.Adapter(conn)
	f.Update(func(s *fakestate.State) { s.Launch = fakestate.CrashAfterCreate })
	in := intent(f, "req-lost")
	var rejected *execution.Rejected
	if _, err := a.Launch(t.Context(), in, execution.Checkpoint{}, nil); err == nil || errors.As(err, &rejected) {
		t.Fatalf("lost reply must be uncertain: %v", err)
	}
	other := intent(f, "req-other")
	f.Update(func(s *fakestate.State) { s.Launch = "" })
	if _, err := a.Launch(t.Context(), other, execution.Checkpoint{}, nil); err != nil {
		t.Fatal(err)
	}
	o, err := a.Correlate(t.Context(), in)
	if err != nil || o.State != "running" {
		t.Fatalf("%+v %v", o, err)
	}
	tr, _ := claudecode.ReadHead(filepath.Join(f.ProjectsDir(), claudecode.EncodeProject(f.Workspace), o.Session.ExternalSessionID+".jsonl"))
	if !strings.Contains(tr.FirstPrompt, in.Marker) {
		t.Fatalf("correlated the wrong session: %q", tr.FirstPrompt)
	}
	if _, err := a.Correlate(t.Context(), intent(f, "req-never")); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("never created: %v", err)
	}
	if n := len(f.Launches()); n != 2 {
		t.Fatalf("correlation launched: %d", n)
	}
}

// A worktree launch names its worktree per attempt and correlates in it.
func TestWorktreeLaunch(t *testing.T) {
	f := claudetest.New(t)
	a := f.Adapter(conn)
	in := intent(f, "req-w")
	in.Workspace.Kind = "worktree"
	f.Update(func(s *fakestate.State) { s.Launch = fakestate.CrashAfterCreate })
	if _, err := a.Launch(t.Context(), in, execution.Checkpoint{}, nil); err == nil {
		t.Fatal("lost reply reported success")
	}
	if args := f.Launches()[0].Args; !slices.Contains(args, "-w") || !slices.Contains(args, claudecode.WorktreeName("req-w")) {
		t.Fatalf("args %q", args)
	}
	o, err := a.Correlate(t.Context(), in)
	wt := claudecode.WorktreeDir(f.Workspace, claudecode.WorktreeName("req-w"))
	if err != nil || o.Directory != wt || o.Project != f.Workspace {
		t.Fatalf("%+v %v", o, err)
	}
}

// Observe maps the session list; a removed session with a transcript is
// idle with its reply, one without either is not found; replies are
// bounded and shown once the session is not running.
func TestObserveStatesAndReply(t *testing.T) {
	f := claudetest.New(t)
	a := f.Adapter(conn)
	o, err := a.Launch(t.Context(), intent(f, "req-o"), execution.Checkpoint{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref, id := o.Session, o.Session.ExternalSessionID
	f.Reply(id, "Which file?", true)
	f.SetState(id, "blocked", "idle", "")
	if o, err = a.Observe(t.Context(), ref); err != nil || o.State != "needs_input" || !o.Active || o.Reply == nil || o.Reply.Text != "Which file?" {
		t.Fatalf("blocked %+v %v", o, err)
	}
	f.Reply(id, strings.Repeat("é", 5000), true)
	f.SetState(id, "done", "idle", "")
	if o, err = a.Observe(t.Context(), ref); err != nil || o.State != "done" || o.Active || o.EndedAt == nil || !o.Reply.Truncated || len([]rune(o.Reply.Text)) != 4000 {
		t.Fatalf("done %+v %v", o, err)
	}
	f.SetState(id, "working", "busy", "")
	if o, err = a.Observe(t.Context(), ref); err != nil || o.State != "running" || o.Reply != nil {
		t.Fatalf("working again %+v %v", o, err)
	}
	f.SetState(id, "failed", "", "")
	if o, _ = a.Observe(t.Context(), ref); o.State != "failed" {
		t.Fatalf("failed %+v", o)
	}
	f.Remove(id)
	o, err = a.Observe(t.Context(), ref)
	if err != nil || o.State != "idle" || o.Reply == nil || !strings.HasPrefix(o.Links.Command, "cd "+f.Workspace+" && claude --resume "+id) {
		t.Fatalf("removed %+v %v", o, err)
	}
	if _, err := a.Observe(t.Context(), execution.SessionRef{ConnectionID: conn, ExternalSessionID: "11111111-1111-4111-8111-111111111111"}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := a.Observe(t.Context(), execution.SessionRef{ConnectionID: conn, ExternalSessionID: "not-a-uuid"}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("malformed: %v", err)
	}
	if op, err := a.Opening(t.Context(), ref, false); err != nil || !strings.HasPrefix(op.Text, "- do it") {
		t.Fatalf("opening %+v %v", op, err)
	}
	f.Update(func(s *fakestate.State) { s.FailAgents = true })
	if _, err := a.Observe(t.Context(), ref); err == nil || errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("an unreadable list must stay uncertain: %v", err)
	}
}

// Usage comes from the transcript plus its subagents' transcripts (tokens
// only), never with a cost; a session with no transcript has none.
func TestObserveUsage(t *testing.T) {
	f := claudetest.New(t)
	a := f.Adapter(conn)
	o, err := a.Launch(t.Context(), intent(f, "req-usage"), execution.Checkpoint{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref, id := o.Session, o.Session.ExternalSessionID
	f.Reply(id, "one", true)
	f.Prompt(id, "again")
	f.Reply(id, "two", false)
	if o, err = a.Observe(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	want := execution.Usage{Turns: 2, InputTokens: 20, CacheReadTokens: 200, CacheWriteTokens: 40, OutputTokens: 10, WorkingMs: fakestate.ReplyDurationMs}
	if o.Usage == nil || *o.Usage != want {
		t.Fatalf("usage %+v, want %+v", o.Usage, want)
	}

	transcript := fakestate.TranscriptPath(f.ProjectsDir(), f.Session(id).Cwd, id)
	subagents := filepath.Join(strings.TrimSuffix(transcript, ".jsonl"), "subagents")
	if err := os.MkdirAll(subagents, 0o755); err != nil {
		t.Fatal(err)
	}
	sub := `{"type":"assistant","isSidechain":true,"sessionId":"` + id + `","timestamp":"2026-11-25T03:00:00Z","message":{"id":"msg_sub","model":"claude-haiku-5-5","content":[],"usage":{"input_tokens":1,"cache_read_input_tokens":2,"cache_creation_input_tokens":3,"output_tokens":4}}}` + "\n"
	if err := os.WriteFile(filepath.Join(subagents, "agent-a1.jsonl"), []byte(sub), 0o600); err != nil {
		t.Fatal(err)
	}
	if o, err = a.Observe(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	want.InputTokens, want.CacheReadTokens, want.CacheWriteTokens, want.OutputTokens = 21, 202, 43, 14
	if o.Usage == nil || *o.Usage != want {
		t.Fatalf("with a subagent %+v, want %+v", o.Usage, want)
	}

	if err := os.Remove(transcript); err != nil {
		t.Fatal(err)
	}
	if o, err = a.Observe(t.Context(), ref); err != nil || o.Usage != nil {
		t.Fatalf("no transcript: usage %+v %v", o.Usage, err)
	}
}

// `claude rm` of a background session whose turn ended is the archived
// closure; the idle reap (entry kept, no process), a removal mid-turn and an
// interactive session leaving the list are not.
func TestRemovalIsTheArchivedClosure(t *testing.T) {
	f := claudetest.New(t)
	a := f.Adapter(conn)
	o, err := a.Launch(t.Context(), intent(f, "req-rm"), execution.Checkpoint{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref, id := o.Session, o.Session.ExternalSessionID
	f.Reply(id, "Done.", true)
	f.SetState(id, "done", "idle", "")
	if o, err = a.Observe(t.Context(), ref); err != nil || o.State != "done" || o.Closed != nil {
		t.Fatalf("done %+v %v", o, err)
	}
	f.Update(func(s *fakestate.State) { // reaped: no process, entry and state kept
		for i := range s.Sessions {
			if s.Sessions[i].SessionID == id {
				s.Sessions[i].PID, s.Sessions[i].Status = 0, ""
			}
		}
	})
	if o, err = a.Observe(t.Context(), ref); err != nil || o.State != "done" || o.Closed != nil {
		t.Fatalf("reaped %+v %v", o, err)
	}
	f.Rm(id)
	o, err = a.Observe(t.Context(), ref)
	if err != nil || o.State != "idle" || o.Active || o.Archived || o.Closed == nil || o.Closed.Signal != execution.ClosedArchived || o.ExecutionID == "" || o.Reply == nil || o.Reply.Text != "Done." {
		t.Fatalf("removed %+v %v", o, err)
	}

	mid, err := a.Launch(t.Context(), intent(f, "req-rm-mid"), execution.Checkpoint{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	midID := mid.Session.ExternalSessionID
	f.Reply(midID, "First.", true)
	f.Prompt(midID, "And another thing")
	f.Rm(midID)
	if o, err = a.Observe(t.Context(), mid.Session); err != nil || o.State != "idle" || o.Closed != nil {
		t.Fatalf("removed mid-turn %+v %v", o, err)
	}

	term := "66666666-6666-4666-8666-666666666666"
	f.AddInteractive(term, f.Workspace, "cli", "from a terminal")
	if err := fakestate.AppendReply(f.ProjectsDir(), f.Workspace, term, "Hi.", true); err != nil {
		t.Fatal(err)
	}
	f.Remove(term) // its process exited
	if o, err = a.Observe(t.Context(), execution.SessionRef{ConnectionID: conn, ExternalSessionID: term}); err != nil || o.State != "idle" || o.Closed != nil {
		t.Fatalf("interactive exit %+v %v", o, err)
	}
}

// Stop stops a background session; it maps to interrupted.
func TestStop(t *testing.T) {
	f := claudetest.New(t)
	a := f.Adapter(conn)
	o, err := a.Launch(t.Context(), intent(f, "req-s"), execution.Checkpoint{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Stop(t.Context(), o.Session); err != nil {
		t.Fatal(err)
	}
	if o, err = a.Observe(t.Context(), o.Session); err != nil || o.State != "interrupted" {
		t.Fatalf("%+v %v", o, err)
	}
	if err := a.Stop(t.Context(), execution.SessionRef{ConnectionID: conn, ExternalSessionID: "11111111-1111-4111-8111-111111111111"}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	f.AddInteractive("22222222-2222-4222-8222-222222222222", f.Workspace, "cli", "hi")
	if err := a.Stop(t.Context(), execution.SessionRef{ConnectionID: conn, ExternalSessionID: "22222222-2222-4222-8222-222222222222"}); err == nil {
		t.Fatal("stopped an interactive session")
	}
}

// Attention lists every session waiting for its user: blocked background
// sessions (also retired ones, with no process) and waiting terminal
// sessions, whatever client started them (the coordinator leaves out
// wrapped ones), never ones that are not waiting.
func TestAttentionListsWaitingSessions(t *testing.T) {
	f := claudetest.New(t)
	a := f.Adapter(conn)
	launch := func(req string) string {
		t.Helper()
		o, err := a.Launch(t.Context(), intent(f, req), execution.Checkpoint{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return o.Session.ExternalSessionID
	}
	attention := func(want ...string) {
		t.Helper()
		refs, err := a.Attention(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for _, r := range refs {
			if r.ConnectionID != conn {
				t.Fatalf("connection %+v", r)
			}
			got = append(got, r.ExternalSessionID)
		}
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatalf("attention %v, want %v", got, want)
		}
	}
	_, blocked, done := launch("req-a1"), launch("req-a2"), launch("req-a3")
	attention()
	f.SetState(blocked, "blocked", "waiting", "permission prompt")
	f.SetState(done, "done", "idle", "")
	attention(blocked)
	// Retired after an hour idle: no process, still blocked.
	f.Update(func(s *fakestate.State) {
		for i := range s.Sessions {
			if s.Sessions[i].SessionID == blocked {
				s.Sessions[i].PID, s.Sessions[i].Status, s.Sessions[i].WaitingFor = 0, "", ""
			}
		}
	})
	attention(blocked)
	term, t3, idle := "44444444-4444-4444-8444-444444444444", "33333333-3333-4333-8333-333333333333", "55555555-5555-4555-8555-555555555555"
	f.AddInteractive(term, f.Workspace, "cli", "from a terminal")
	f.AddInteractive(t3, f.Workspace, "t3-code", "from T3")
	f.AddInteractive(idle, f.Workspace, "cli", "idle terminal")
	f.SetState(term, "", "waiting", "permission prompt")
	f.SetState(t3, "", "waiting", "input needed")
	f.SetState(idle, "", "idle", "")
	attention(blocked, term, t3)
	f.Update(func(s *fakestate.State) { s.FailAgents = true })
	if _, err := a.Attention(t.Context()); err == nil {
		t.Fatal("an unreadable list must be an error")
	}
}

// A terminal session is answered where it runs: no command while its
// process lives, the resume command once Claude no longer lists it.
func TestInteractiveLinks(t *testing.T) {
	f := claudetest.New(t)
	a := f.Adapter(conn)
	term := "44444444-4444-4444-8444-444444444444"
	f.AddInteractive(term, f.Workspace, "cli", "from a terminal")
	f.SetState(term, "", "waiting", "permission prompt")
	ref := execution.SessionRef{ConnectionID: conn, ExternalSessionID: term}
	o, err := a.Observe(t.Context(), ref)
	if err != nil || o.State != "needs_input" || o.Links != (execution.Links{}) {
		t.Fatalf("live %+v %v", o, err)
	}
	if l := a.Destinations(t.Context(), ref); l != (execution.Links{}) {
		t.Fatalf("destinations %+v", l)
	}
	f.Remove(term)
	if o, err = a.Observe(t.Context(), ref); err != nil || o.Links.Command != "cd "+f.Workspace+" && claude --resume "+term {
		t.Fatalf("exited %+v %v", o, err)
	}
}

// A session reports the client that started it raw (its origin), for the
// coordinator to refuse a wrapped one; a terminal session can be adopted
// by UUID or short id.
func TestSessionOriginAndLinksResolve(t *testing.T) {
	f := claudetest.New(t)
	a := f.Adapter(conn)
	wrapped := "33333333-3333-4333-8333-333333333333"
	f.AddInteractive(wrapped, f.Workspace, "wrapper-entrypoint", "from a wrapper")
	ref := execution.SessionRef{ConnectionID: conn, ExternalSessionID: wrapped}
	if o, err := a.Observe(t.Context(), ref); err != nil || o.Origin != "wrapper-entrypoint" {
		t.Fatalf("observe: %+v %v", o, err)
	}
	if r, err := a.ResolveLink(t.Context(), wrapped); err != nil || r != ref {
		t.Fatalf("resolve: %+v %v", r, err)
	}
	term := "44444444-4444-4444-8444-444444444444"
	f.AddInteractive(term, f.Workspace, "cli", "from a terminal")
	if r, err := a.ResolveLink(t.Context(), "claude --resume "+term); err != nil || r.ExternalSessionID != term {
		t.Fatalf("%+v %v", r, err)
	}
	if o, err := a.Observe(t.Context(), execution.SessionRef{ConnectionID: conn, ExternalSessionID: term}); err != nil || o.State != "running" || o.Origin != "cli" {
		t.Fatalf("interactive %+v %v", o, err)
	}
	o, err := a.Launch(t.Context(), intent(f, "req-r"), execution.Checkpoint{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	short := f.Session(o.Session.ExternalSessionID).ID
	if r, err := a.ResolveLink(t.Context(), "claude attach "+short); err != nil || r != o.Session {
		t.Fatalf("short %+v %v", r, err)
	}
	if _, err := a.ResolveLink(t.Context(), "https://example.com"); err == nil {
		t.Fatal("resolved garbage")
	}
}

// Health follows the login and the config directory; the inventory lists
// Claude's folders, importable when they are Git checkouts.
func TestHealthAndWorkspaces(t *testing.T) {
	f := claudetest.New(t)
	a := f.Adapter(conn)
	if h := a.Health(t.Context()); !h.Available || h.Identity != f.ConfigDir() {
		t.Fatalf("%+v", h)
	}
	f.Update(func(s *fakestate.State) { s.LoggedIn = false })
	if h := claudecode.New(claudecode.Options{Connection: conn, CLI: f.CLI(), Identity: f.ConfigDir()}).Health(t.Context()); h.Available || !strings.Contains(h.Reason, "claude auth login") {
		t.Fatalf("logged out %+v", h)
	}
	if h := claudecode.New(claudecode.Options{Connection: conn, CLI: f.CLI(), Identity: "/elsewhere"}).Health(t.Context()); h.Available {
		t.Fatalf("other identity %+v", h)
	}
	plain := filepath.Join(f.Home, "notes")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	f.WriteClaudeJSON(f.Workspace, plain, f.Workspace+"/.claude/worktrees/x", "relative")
	ws, err := a.Workspaces(t.Context())
	if err != nil || len(ws) != 2 {
		t.Fatalf("%+v %v", ws, err)
	}
	for _, w := range ws {
		if w.ID != w.WorkspaceRoot || w.Importable != (w.ID == f.Workspace) {
			t.Fatalf("%+v", w)
		}
	}
	if err := os.WriteFile(filepath.Join(f.Home, ".claude.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Workspaces(t.Context()); err == nil {
		t.Fatal("a malformed state file must fail, not empty the inventory")
	}
	if err := os.Remove(filepath.Join(f.Home, ".claude.json")); err != nil {
		t.Fatal(err)
	}
	if ws, err := a.Workspaces(t.Context()); err != nil || len(ws) != 0 {
		t.Fatalf("no file: %+v %v", ws, err)
	}
}

// Features: Claude's aliases, effort, permission modes and workspaces; the
// operations it lacks say why.
func TestFeatures(t *testing.T) {
	f := claudetest.New(t)
	c, err := f.Adapter(conn).Features(t.Context())
	if err != nil || c.Valid() != nil || c.DeclaresOperations() != nil {
		t.Fatal(err, c.Valid(), c.DeclaresOperations())
	}
	for _, op := range []execution.Op{execution.OpWorktreeBase, execution.OpRespond} {
		if a := c.Operations[op]; a.Available || a.Reason == "" {
			t.Fatalf("%s %+v", op, a)
		}
	}
	for _, op := range []execution.Op{execution.OpImages, execution.OpPredecessorBase, execution.OpSend, execution.OpAttention} {
		if a := c.Operations[op]; !a.Available {
			t.Fatalf("%s %+v", op, a)
		}
	}
	if err := c.ValidateConfig(execution.Config{}); err != nil {
		t.Fatal("Claude's defaults", err)
	}
	if err := c.ValidateConfig(execution.Config{Model: "opus", Permission: "plan", Options: []execution.ModelOption{{ID: "effort", Value: execution.StringValue("max")}}}); err != nil {
		t.Fatal(err)
	}
}
