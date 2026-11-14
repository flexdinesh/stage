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

var testDB *dbtest.DB

func TestMain(m *testing.M) {
	testDB = dbtest.Open("stage_test_cli")
	if testDB.Skip != "" {
		fmt.Println("cli integration tests skipped:", testDB.Skip)
	}
	code := m.Run()
	testDB.Close()
	os.Exit(code)
}

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
	if testDB.Skip != "" {
		return h
	}
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

func (h *harness) needDB() {
	if testDB.Skip != "" {
		h.t.Skip(testDB.Skip)
	}
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
	h.needDB()
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

func TestScheduleAndContextCommands(t *testing.T) {
	h := newHarness(t)
	h.needDB()
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
	h.url = "http://controlbox:7380"
	code, stdout, stderr := h.run("", "setup", "mcp")
	if code != 0 {
		t.Fatalf("setup mcp: %s", stderr)
	}
	got := fmt.Sprint(h.execs)
	want := "[[claude mcp remove --scope user stage] [claude mcp add --scope user -e STAGE_URL=http://controlbox:7380 stage -- /usr/local/bin/stage mcp] [codex mcp remove stage]]"
	if got != want || !strings.Contains(stdout, "skipped codex") {
		t.Fatalf("execs %s\nstdout %s", got, stdout)
	}
	h.execs = nil
	if code, _, _ := h.run("", "setup", "mcp", "--claude", "--http"); code != 0 {
		t.Fatal("http")
	}
	if got := fmt.Sprint(h.execs); got != "[[claude mcp remove --scope user stage] [claude mcp add --scope user --transport http stage http://controlbox:7380/mcp]]" {
		t.Fatalf("http execs %s", got)
	}
}
