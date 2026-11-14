package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/cli"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/notify"
	t3sync "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
)

var testDB dbtest.DB

type harness struct {
	t      *testing.T
	url    string
	fake   *t3.Fake
	sync   *t3sync.Service
	caller agentctx.Caller
	execs  [][]string
}

func newHarness(t *testing.T) *harness {
	h := &harness{t: t, url: "http://127.0.0.1:1"}
	pool := testDB.Reset(t)
	store := db.NewStore(pool)
	h.fake = t3.NewFake()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.sync = t3sync.New(store, h.fake, log)
	srv := httptest.NewServer(api.NewRouter(api.Deps{
		Config: config.Config{T3URL: "http://127.0.0.1:3773", T3PublicURL: "http://t3.test"},
		Store:  store, T3: h.fake, Sync: h.sync, Push: notify.New(store, "https://stage.test", log), Logger: log,
	}))
	t.Cleanup(srv.Close)
	h.url = srv.URL
	return h
}

// run executes the CLI; returns exit code, stdout, stderr.
func (h *harness) run(stdin string, args ...string) (int, string, string) {
	h.t.Helper()
	var out, errb bytes.Buffer
	env := cli.Env{
		Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errb,
		Getenv: func(k string) string {
			if k == cli.URLEnv {
				return h.url
			}
			return ""
		},
		Caller: h.caller, Home: h.t.TempDir(), Executable: "/usr/local/bin/stage",
		Exec: func(name string, args ...string) error {
			h.execs = append(h.execs, append([]string{name}, args...))
			return nil
		},
		LookPath: func(name string) error {
			if name == "codex" {
				return errors.New("not found")
			}
			return nil
		},
	}
	code := cli.Run(context.Background(), args, env)
	return code, out.String(), errb.String()
}

func (h *harness) project(root string) db.Project {
	h.t.Helper()
	ctx := context.Background()
	if _, err := h.fake.CreateProject(ctx, t3.CreateProjectInput{Title: "P " + root, WorkspaceRoot: root}); err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.sync.FullSync(ctx); err != nil {
		h.t.Fatal(err)
	}
	ps, err := client.New(h.url).Projects(ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	for _, p := range ps {
		if p.WorkspaceRoot == root {
			return p
		}
	}
	h.t.Fatalf("no project %s", root)
	return db.Project{}
}

func decode[T any](t *testing.T, s string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	return v
}

func TestTaskCommands(t *testing.T) {
	h := newHarness(t)
	p := h.project("/w/cli")
	out, err := h.fake.LaunchThread(context.Background(), t3.LaunchInput{ProjectID: p.T3ProjectID, Title: "Me"})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.sync.RefreshSession(context.Background(), out.ThreadID); err != nil {
		t.Fatal(err)
	}
	h.caller = agentctx.Caller{ThreadID: out.ThreadID, Cwd: "/elsewhere"}

	// Prompt from stdin; flags after the subcommand; session picks the project.
	code, stdout, stderr := h.run("multi\nline prompt\n", "task", "add", "--title", "From agent", "--prompt", "-", "--after-me", "--tag", "a", "--tag", "b", "--json")
	if code != 0 {
		t.Fatalf("add: %d %s", code, stderr)
	}
	tk := decode[client.Task](t, stdout)
	if tk.ProjectID != p.ID || tk.Prompt != "multi\nline prompt\n" || tk.AfterThreadID == nil || *tk.AfterThreadID != out.ThreadID ||
		tk.OriginThreadID == nil || len(tk.Tags) != 2 {
		t.Fatalf("task %+v", tk)
	}

	// Positional id before flags.
	code, stdout, stderr = h.run("", "task", "edit", fmt.Sprint(tk.ID), "--title", "Renamed", "--json")
	if code != 0 || decode[client.Task](t, stdout).Title != "Renamed" {
		t.Fatalf("edit: %d %s %s", code, stdout, stderr)
	}
	code, stdout, _ = h.run("", "task", "ls", "--json", "--state", "backlog")
	if rows := decode[agenttools.TasksOut](t, stdout); code != 0 || rows.Total != 1 || rows.Tasks[0].Title != "Renamed" {
		t.Fatalf("ls: %d %s", code, stdout)
	}
	code, stdout, _ = h.run("", "task", "ls")
	if code != 0 || !strings.Contains(stdout, "Renamed") || !strings.Contains(stdout, "session "+out.ThreadID) {
		t.Fatalf("ls human: %s", stdout)
	}
	code, stdout, _ = h.run("", "task", "show", fmt.Sprint(tk.ID))
	if code != 0 || !strings.Contains(stdout, "created by session") || !strings.Contains(stdout, "line prompt") {
		t.Fatalf("show: %s", stdout)
	}
	code, stdout, _ = h.run("", "task", "unlink", fmt.Sprint(tk.ID), "--json")
	if code != 0 || decode[client.Task](t, stdout).AfterThreadID != nil {
		t.Fatalf("unlink: %s", stdout)
	}

	// wait times out with exit 3.
	code, _, _ = h.run("", "task", "wait", fmt.Sprint(tk.ID), "--until", "running", "--timeout", "1s")
	if code != 3 {
		t.Fatalf("wait timeout exit %d", code)
	}
	code, stdout, _ = h.run("", "task", "rm", fmt.Sprint(tk.ID))
	if code != 0 || !strings.Contains(stdout, "deleted task") {
		t.Fatalf("rm: %d %s", code, stdout)
	}

	// API errors exit 1; usage errors exit 2.
	if code, _, stderr := h.run("", "task", "show", "999"); code != 1 || !strings.Contains(stderr, "404") {
		t.Fatalf("missing task: %d %s", code, stderr)
	}
	if code, _, _ := h.run("", "task", "add", "--title", "x"); code != 2 {
		t.Fatalf("missing prompt exit %d", code)
	}
	if code, _, _ := h.run("", "task", "show"); code != 2 {
		t.Fatalf("missing id exit %d", code)
	}
}

func TestShowPrintsPullRequestsAndRuns(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	p := h.project("/w/cli-prs")
	code, stdout, stderr := h.run("", "task", "add", "--project", fmt.Sprint(p.ID), "--title", "Ship", "--prompt", "go", "--start", "--json")
	if code != 0 {
		t.Fatalf("add: %d %s", code, stderr)
	}
	tk := decode[client.Task](t, stdout)
	if tk.T3ThreadID == nil {
		t.Fatalf("not launched: %+v", tk)
	}
	thread := *tk.T3ThreadID
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	if err := h.fake.SetThread(thread, func(d *t3.ThreadDetail) {
		d.PullRequests = []t3.PullRequest{{Host: "github.com", Repository: "o/r", Number: 7, URL: "https://github.com/o/r/pull/7", Source: "created", State: "merged"}}
		d.RecentRuns = nil
		for i := 12; i >= 1; i-- {
			req := base.Add(time.Duration(i) * time.Minute)
			start, done := req.Add(2*time.Second), req.Add(32*time.Second)
			d.RecentRuns = append(d.RecentRuns, t3.Run{RunID: fmt.Sprint("r", i), Ordinal: i, Status: t3.StatusCompleted,
				Model: "m", ExecutedModel: "m", RequestedAt: req, StartedAt: &start, CompletedAt: &done})
		}
		d.RecentRuns[0].ExecutedModel = "m-big"
		d.RunCount = 12
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.sync.RefreshSession(ctx, thread); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{{"task", "show", fmt.Sprint(tk.ID)}, {"session", "show", thread}} {
		code, stdout, stderr = h.run("", args...)
		if code != 0 {
			t.Fatalf("%v: %d %s", args, code, stderr)
		}
		for _, want := range []string{"prs", "o/r#7", "merged", "https://github.com/o/r/pull/7", "runs", "#12", "m -> m-big", "30s", "2s", "(10 of 12 runs)"} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("%v: missing %q in\n%s", args, want, stdout)
			}
		}
		if strings.Contains(stdout, "#2 ") {
			t.Fatalf("%v: runs block not capped:\n%s", args, stdout)
		}
	}

	code, stdout, _ = h.run("", "session", "show", thread, "--json")
	d := decode[client.ThreadDetail](t, stdout)
	if code != 0 || len(d.Thread.RecentRuns) != 12 || d.Thread.RunCount != 12 || len(d.Thread.PullRequests) != 1 {
		t.Fatalf("session json: %d runs=%d count=%d prs=%d", code, len(d.Thread.RecentRuns), d.Thread.RunCount, len(d.Thread.PullRequests))
	}
	code, stdout, _ = h.run("", "task", "show", fmt.Sprint(tk.ID), "--json")
	td := decode[client.TaskDetail](t, stdout)
	if code != 0 || len(td.PullRequests) != 1 || td.Thread == nil || len(td.Thread.RecentRuns) != 12 {
		t.Fatalf("task json: %s", stdout)
	}
}

func TestTaskOrderAndSessionBatch(t *testing.T) {
	h := newHarness(t)
	p := h.project("/w/cli-o")
	out, err := h.fake.LaunchThread(context.Background(), t3.LaunchInput{ProjectID: p.T3ProjectID, Title: "Planner"})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.sync.RefreshSession(context.Background(), out.ThreadID); err != nil {
		t.Fatal(err)
	}
	h.caller = agentctx.Caller{ThreadID: out.ThreadID}
	ids := make([]string, 3)
	for i, title := range []string{"first", "second", "loose"} {
		code, stdout, stderr := h.run("", "task", "add", "--title", title, "--prompt", "p", "--json")
		if code != 0 {
			t.Fatalf("add: %s", stderr)
		}
		ids[i] = fmt.Sprint(decode[client.Task](t, stdout).ID)
	}
	code, stdout, stderr := h.run("", "task", "order", ids[1], ids[0])
	if code != 0 || !strings.Contains(stdout, "1. #"+ids[1]+" [backlog] second") || !strings.Contains(stdout, "2. #"+ids[0]) {
		t.Fatalf("order: %d %s %s", code, stdout, stderr)
	}
	if code, _, _ := h.run("", "task", "order", ids[0]); code != 2 {
		t.Fatalf("order one id exit %d", code)
	}
	code, stdout, _ = h.run("", "sessions", "show", out.ThreadID)
	want := "\ncreated tasks (3)\n  chain #" + ids[1] + " [backlog] second\n        #" + ids[0] + " [backlog] first\n  task  #" + ids[2] + " [backlog] loose\n"
	if code != 0 || !strings.Contains(stdout, want) {
		t.Fatalf("sessions show:\n%s\nwant:\n%s", stdout, want)
	}
	code, stdout, _ = h.run("", "sessions", "show", out.ThreadID, "--json")
	if d := decode[client.ThreadDetail](t, stdout); code != 0 || len(d.CreatedTasks.Chains) != 1 || len(d.CreatedTasks.Independent) != 1 {
		t.Fatalf("sessions show json: %s", stdout)
	}
}

func TestScheduleAndContextCommands(t *testing.T) {
	h := newHarness(t)
	p := h.project("/w/cli-s")
	h.caller = agentctx.Caller{Cwd: "/w/cli-s/pkg"}
	code, stdout, stderr := h.run("", "context", "--json")
	if cx := decode[agenttools.ContextOut](t, stdout); code != 0 || cx.Project == nil || cx.Project.ID != p.ID || cx.ProjectSource != "cwd" {
		t.Fatalf("context: %d %s %s", code, stdout, stderr)
	}
	code, stdout, stderr = h.run("", "schedule", "add", "--title", "n", "--prompt", "p", "--cron", "0 2 * * *", "--tz", "UTC", "--json")
	if code != 0 {
		t.Fatalf("schedule add: %s", stderr)
	}
	sc := decode[db.Schedule](t, stdout)
	code, stdout, _ = h.run("", "schedule", "pause", fmt.Sprint(sc.ID), "--json")
	if code != 0 || !decode[db.Schedule](t, stdout).Paused {
		t.Fatalf("pause: %s", stdout)
	}
	code, stdout, _ = h.run("", "schedule", "preview", "--cron", "@hourly", "--tz", "UTC")
	if code != 0 || len(strings.Fields(stdout)) != 3 {
		t.Fatalf("preview: %s", stdout)
	}
	code, stdout, _ = h.run("", "projects", "ls")
	if code != 0 || !strings.Contains(stdout, "/w/cli-s") {
		t.Fatalf("projects: %s", stdout)
	}
}

func TestSessionsSearchCommand(t *testing.T) {
	h := newHarness(t)
	p := h.project("/w/cli-search")
	ctx := context.Background()
	res, err := h.fake.LaunchThread(ctx, t3.LaunchInput{ProjectID: p.T3ProjectID, Title: "What more?"})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.sync.RefreshSession(ctx, res.ThreadID); err != nil {
		t.Fatal(err)
	}
	h.fake.AddMessage(res.ThreadID, t3.SearchUser, "…find sessions by what was said…")

	code, stdout, stderr := h.run("", "sessions", "search", "what", "was", "said")
	if code != 0 || !strings.Contains(stdout, "user") || !strings.Contains(stdout, "What more?") ||
		!strings.Contains(stdout, "…find sessions by what was said…") || strings.Contains(stdout, "titles only") {
		t.Fatalf("search: %d %q %q", code, stdout, stderr)
	}
	code, stdout, _ = h.run("", "sessions", "search", "what was said", "--project", p.Title, "--json")
	out := decode[agenttools.SearchSessionsOut](t, stdout)
	if code != 0 || out.T3 != "ok" || len(out.Results) != 1 || out.Results[0].T3ThreadID != res.ThreadID {
		t.Fatalf("search --json: %d %s", code, stdout)
	}
	h.fake.SearchErr = errors.New("down")
	code, stdout, _ = h.run("", "sessions", "search", "nothing here")
	if code != 0 || !strings.Contains(stdout, "no matching sessions") || !strings.Contains(stdout, "(T3 offline: titles only)") {
		t.Fatalf("search offline: %d %q", code, stdout)
	}
	if code, _, _ := h.run("", "sessions", "search"); code == 0 {
		t.Fatal("search without query succeeded")
	}
}

func TestSetupInstructionsIdempotent(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(path, []byte("# Mine\n\nkeep this\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if code, _, stderr := h.run("", "setup", "instructions", "--file", path); code != 0 {
			t.Fatalf("setup: %s", stderr)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Count(s, "<!-- stage:begin -->") != 1 || !strings.HasPrefix(s, "# Mine\n\nkeep this\n\n") || !strings.Contains(s, "afterMe") {
		t.Fatalf("file:\n%s", s)
	}
	if code, stdout, _ := h.run("", "setup", "instructions", "--file", path); code != 0 || !strings.Contains(stdout, "unchanged") {
		t.Fatalf("rerun: %s", stdout)
	}
}

func TestSetupMCP(t *testing.T) {
	h := newHarness(t)
	h.url = "http://localhost:7380"
	code, stdout, stderr := h.run("", "setup", "mcp")
	if code != 0 {
		t.Fatalf("setup mcp: %s", stderr)
	}
	got := fmt.Sprint(h.execs)
	want := "[[claude mcp remove --scope user stage] [claude mcp add --scope user -e STAGE_URL=http://localhost:7380 stage -- /usr/local/bin/stage mcp] [codex mcp remove stage]]"
	if got != want || !strings.Contains(stdout, "skipped codex") {
		t.Fatalf("execs %s\nstdout %s", got, stdout)
	}
	h.execs = nil
	if code, _, _ := h.run("", "setup", "mcp", "--claude", "--http"); code != 0 {
		t.Fatal("http")
	}
	if got := fmt.Sprint(h.execs); got != "[[claude mcp remove --scope user stage] [claude mcp add --scope user --transport http stage http://localhost:7380/mcp]]" {
		t.Fatalf("http execs %s", got)
	}
}

func TestHandoffFlags(t *testing.T) {
	h := newHarness(t)
	h.project("/w/cli-handoff")
	h.caller = agentctx.Caller{Cwd: "/w/cli-handoff"}
	code, stdout, stderr := h.run("", "task", "add", "--title", "a", "--prompt", "x", "--json")
	if code != 0 {
		t.Fatalf("add: %d %s", code, stderr)
	}
	a := decode[client.Task](t, stdout)
	if !a.Handoff {
		t.Fatalf("handoff defaults on: %+v", a)
	}
	code, stdout, stderr = h.run("", "task", "add", "--title", "b", "--prompt", "y", "--after-task", fmt.Sprint(a.ID), "--handoff=false", "--json")
	if code != 0 {
		t.Fatalf("add b: %d %s", code, stderr)
	}
	b := decode[client.Task](t, stdout)
	if b.Handoff {
		t.Fatalf("--handoff=false: %+v", b)
	}
	code, stdout, _ = h.run("", "task", "show", fmt.Sprint(b.ID))
	if code != 0 || !strings.Contains(stdout, "handoff  off") {
		t.Fatalf("show: %s", stdout)
	}
	code, stdout, _ = h.run("", "task", "edit", fmt.Sprint(b.ID), "--title", "b2", "--json")
	if code != 0 || decode[client.Task](t, stdout).Handoff {
		t.Fatalf("edit without --handoff keeps it: %s", stdout)
	}
	code, stdout, _ = h.run("", "task", "edit", fmt.Sprint(b.ID), "--handoff=true", "--json")
	if code != 0 || !decode[client.Task](t, stdout).Handoff {
		t.Fatalf("edit --handoff=true: %s", stdout)
	}
	code, stdout, _ = h.run("", "task", "link", fmt.Sprint(b.ID), "--after-task", fmt.Sprint(a.ID), "--handoff=false", "--json")
	if code != 0 || decode[client.Task](t, stdout).Handoff {
		t.Fatalf("link --handoff=false: %s", stdout)
	}
}

func TestSessionQuestionsCommand(t *testing.T) {
	h := newHarness(t)
	p := h.project("/w/cliq")
	out, err := h.fake.LaunchThread(context.Background(), t3.LaunchInput{ProjectID: p.T3ProjectID, Title: "Asks"})
	if err != nil {
		t.Fatal(err)
	}
	no := false
	if _, err := h.fake.AddUserInput(out.ThreadID, []t3.Question{{ID: "q", Header: "Scope", Question: "Which scope?", MultiSelect: true, AllowCustomAnswer: &no,
		Options: []t3.QuestionOption{{Label: "Server", Description: "Go only"}, {Label: "Web"}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.fake.AddApproval(out.ThreadID); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := h.run("", "sessions", "questions", out.ThreadID)
	if code != 0 || !strings.Contains(stdout, "2 pending request(s): 1 question(s), 1 approval(s) in T3") ||
		!strings.Contains(stdout, "1. [Scope] Which scope?  (multi-select)") || !strings.Contains(stdout, "- Server: Go only") ||
		strings.Contains(stdout, "custom answer") {
		t.Fatalf("questions: %d %s %s", code, stdout, stderr)
	}
	code, stdout, _ = h.run("", "sessions", "questions", out.ThreadID, "--json")
	if pq := decode[client.PendingQuestions](t, stdout); code != 0 || pq.ApprovalCount != 1 || len(pq.Requests) != 1 {
		t.Fatalf("questions json: %d %s", code, stdout)
	}
	if code, _, _ := h.run("", "sessions", "questions"); code == 0 {
		t.Fatal("missing id accepted")
	}
}

func TestBaseFlag(t *testing.T) {
	h := newHarness(t)
	h.project("/w/cli-base")
	h.caller = agentctx.Caller{Cwd: "/w/cli-base"}
	code, stdout, stderr := h.run("", "task", "add", "--title", "a", "--prompt", "x", "--json")
	if code != 0 {
		t.Fatalf("add: %d %s", code, stderr)
	}
	a := decode[client.Task](t, stdout)
	code, stdout, stderr = h.run("", "task", "add", "--title", "b", "--prompt", "y", "--after-task", fmt.Sprint(a.ID),
		"--base-ref", "origin/main", "--base", "predecessor", "--json")
	if code != 0 {
		t.Fatalf("add b: %d %s", code, stderr)
	}
	b := decode[client.Task](t, stdout)
	if !b.Workspace.FromPredecessor() || b.Workspace.BaseRef != "origin/main" {
		t.Fatalf("--base predecessor: %+v", b.Workspace)
	}
	code, stdout, _ = h.run("", "task", "show", fmt.Sprint(b.ID))
	if code != 0 || !strings.Contains(stdout, fmt.Sprintf("worktree from #%d's branch (fallback origin/main, branch -)", a.ID)) {
		t.Fatalf("show: %s", stdout)
	}
	code, stdout, _ = h.run("", "task", "edit", fmt.Sprint(b.ID), "--base", "ref", "--json")
	if w := decode[client.Task](t, stdout).Workspace; code != 0 || w.FromPredecessor() || w.BaseRef != "origin/main" {
		t.Fatalf("edit --base ref: %s", stdout)
	}
	if code, _, _ = h.run("", "task", "add", "--title", "c", "--prompt", "z", "--base", "predecessor"); code == 0 {
		t.Fatal("--base without --base-ref must fail")
	}
}
