package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
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
	"github.com/flexdinesh/stage/server/internal/execution"
	stageharness "github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/listen"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/notify"
	"github.com/flexdinesh/stage/server/internal/opencode"
	"github.com/flexdinesh/stage/server/internal/opencodesetup"
	t3sync "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/t3setup"
)

var testDB dbtest.DB

type harness struct {
	t       *testing.T
	url     string
	handler http.Handler // the /api router
	fake    *t3.Fake
	sync    *t3sync.Service
	store   *db.Store
	caller  agentctx.Caller
	execs   [][]string
}

func newHarness(t *testing.T) *harness {
	h := &harness{t: t, url: "http://127.0.0.1:1"}
	pool := testDB.Reset(t)
	store := db.NewStore(pool)
	h.store = store
	h.fake = t3.NewFake()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.sync = t3sync.New(store, t3.NewAdapter(h.fake), log)
	local, _ := h.sync.Machines().Local(db.LocalMachine)
	ocPorts := machine.NewStorePorts(store.OnMachine(db.LocalMachine), stageharness.MustRegistry(opencode.Descriptor))
	openCode, err := opencodesetup.New(t.Context(), opencodesetup.Ports{Settings: ocPorts, Identity: ocPorts, Install: local.Install}, config.OpenCodeSettings{}, log)
	if err != nil {
		t.Fatal(err)
	}
	setup := t3setup.New(t3setup.Options{Config: config.Config{T3URL: "http://127.0.0.1:3773", T3PublicURL: "http://t3.test"}, Settings: store.OnMachine(db.LocalMachine), Identity: machine.NewStorePorts(store.OnMachine(db.LocalMachine), stageharness.MustRegistry(t3.Descriptor)), Client: h.fake, Fake: true, Logger: log})
	reg := stageharness.MustRegistry(t3.Descriptor, opencode.Descriptor)
	h.sync.Harnesses = reg
	connectors, err := stageharness.NewConnectors(reg, map[int64]map[string]stageharness.Connector{db.LocalMachine: {
		t3.Kind: t3setup.NewConnector(setup, nil), opencode.Kind: opencodesetup.NewConnector(openCode),
	}})
	if err != nil {
		t.Fatal(err)
	}
	h.handler = api.NewRouter(api.Deps{
		Config: config.Config{T3URL: "http://127.0.0.1:3773", T3PublicURL: "http://t3.test"},
		Store:  store, Setup: setup, Sync: h.sync, Push: notify.New(store, "https://stage.test", log), Logger: log,
		Harnesses: reg, Connectors: connectors,
	})
	srv := httptest.NewServer(h.handler)
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
	tp, err := h.fake.CreateProject(ctx, t3.CreateProjectInput{Title: "P " + root, WorkspaceRoot: root})
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.sync.FullSync(ctx); err != nil {
		h.t.Fatal(err)
	}
	// The sync pass imports the T3 project.
	p, err := db.NewStore(testDB.Pool).BoundProject(ctx, db.ProjectBinding{ConnectionID: 1, ExternalProjectID: tp.ID, Workspace: root})
	if err != nil {
		h.t.Fatal(err)
	}
	return p
}

func decode[T any](t *testing.T, s string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	return v
}

// t3Session is session id s on the T3 connection (id 1).
func t3Session(s string) *db.SessionRef { return &db.SessionRef{ConnectionID: 1, ExternalSessionID: s} }

func TestTaskCommands(t *testing.T) {
	h := newHarness(t)
	p := h.project("/w/cli")
	out, err := h.fake.LaunchThread(context.Background(), t3.LaunchInput{ProjectID: p.Bindings[0].ExternalProjectID, Title: "Me"})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.sync.RefreshScopedSession(context.Background(), execution.SessionRef{ConnectionID: h.store.LegacyT3ID(), ExternalSessionID: out.ThreadID}); err != nil {
		t.Fatal(err)
	}
	h.caller = agentctx.Caller{Session: t3Session(out.ThreadID), Cwd: "/elsewhere"}

	// Prompt from stdin; flags after the subcommand; session picks the project.
	code, stdout, stderr := h.run("multi\nline prompt\n", "task", "add", "--project", "1", "--title", "From agent", "--prompt", "-", "--after-me", "--json")
	if code != 0 {
		t.Fatalf("add: %d %s", code, stderr)
	}
	tk := decode[client.Task](t, stdout)
	if tk.ProjectID != p.ID || tk.Prompt != "multi\nline prompt\n" || len(tk.Needs) != 1 ||
		tk.OriginSession == nil || *tk.OriginSession != *t3Session(out.ThreadID) {
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
	if code != 0 || !strings.Contains(stdout, "Renamed") || !strings.Contains(stdout, fmt.Sprintf("needs #%d", tk.Needs[0])) {
		t.Fatalf("ls human: %s", stdout)
	}
	code, stdout, _ = h.run("", "task", "show", fmt.Sprint(tk.ID))
	if code != 0 || !strings.Contains(stdout, "created by connection 1 session "+out.ThreadID) || !strings.Contains(stdout, "line prompt") {
		t.Fatalf("show: %s", stdout)
	}
	if code, _, _ = h.run("", "task", "unlink", fmt.Sprint(tk.ID)); code != 2 {
		t.Fatalf("task unlink is gone: exit %d", code)
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
	if code, _, _ := h.run("", "task", "add", "--project", "1", "--title", "x"); code != 2 {
		t.Fatalf("missing prompt exit %d", code)
	}
	if code, _, _ := h.run("", "task", "show"); code != 2 {
		t.Fatalf("missing id exit %d", code)
	}
}

func TestTreeCommandsAndSessionBatch(t *testing.T) {
	h := newHarness(t)
	p := h.project("/w/cli-o")
	out, err := h.fake.LaunchThread(context.Background(), t3.LaunchInput{ProjectID: p.Bindings[0].ExternalProjectID, Title: "Planner"})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.sync.RefreshScopedSession(context.Background(), execution.SessionRef{ConnectionID: h.store.LegacyT3ID(), ExternalSessionID: out.ThreadID}); err != nil {
		t.Fatal(err)
	}
	h.caller = agentctx.Caller{Session: t3Session(out.ThreadID)}
	add := func(args ...string) client.Task {
		t.Helper()
		code, stdout, stderr := h.run("", append([]string{"task", "add", "--project", "1", "--prompt", "p", "--json"}, args...)...)
		if code != 0 {
			t.Fatalf("add: %s", stderr)
		}
		return decode[client.Task](t, stdout)
	}
	first := add("--title", "first")
	second := add("--title", "second", "--needs", fmt.Sprint(first.ID))
	loose := add("--title", "loose")
	if !slices.Equal(second.Needs, []int64{first.ID}) || second.TreeID == nil {
		t.Fatalf("--needs: %+v", second)
	}
	if code, _, _ := h.run("", "task", "add", "--project", "1", "--title", "x", "--prompt", "p", "--needs", "nope"); code != 2 {
		t.Fatalf("bad --needs exit %d", code)
	}
	code, stdout, _ := h.run("", "task", "ls", "--origin-connection", "1", "--origin-session", out.ThreadID, "--json")
	if rows := decode[agenttools.TasksOut](t, stdout); code != 0 || rows.Total != 3 {
		t.Fatalf("batch: %s", stdout)
	}
	if code, _, _ := h.run("", "task", "ls", "--origin-thread", out.ThreadID); code != 2 {
		t.Fatalf("removed --origin-thread exit %d", code)
	}

	// tree new with ID:NEED members, then show/ls/edit/add/rm/start.
	third, fourth := add("--title", "third"), add("--title", "fourth")
	code, stdout, stderr := h.run("", "tree", "new", "--title", "Stack", "--start-mode", "manual", fmt.Sprint(third.ID), fmt.Sprintf("%d:%d", fourth.ID, third.ID), "--json")
	if code != 0 {
		t.Fatalf("tree new: %d %s", code, stderr)
	}
	tree := decode[client.TreeDetail](t, stdout)
	if tree.Title != "Stack" || tree.StartMode != db.StartManual || len(tree.Levels) != 2 || !slices.Equal(tree.Levels[1], []int64{fourth.ID}) {
		t.Fatalf("tree new: %+v", tree)
	}
	if code, _, _ := h.run("", "tree", "new", fmt.Sprint(loose.ID)); code != 2 {
		t.Fatalf("tree new without --title exit %d", code)
	}
	if code, _, _ := h.run("", "tree", "new", "--title", "x", "1:nope"); code != 2 {
		t.Fatalf("tree new bad member exit %d", code)
	}
	code, stdout, _ = h.run("", "tree", "show", fmt.Sprint(tree.ID))
	for _, want := range []string{
		fmt.Sprintf("tree %d  Stack  (manual)  2 tasks, 2 levels", tree.ID),
		fmt.Sprintf("#%d  backlog  third   -      ready", third.ID),
		fmt.Sprintf("#%d  backlog  fourth  #%d", fourth.ID, third.ID),
		"blocked",
	} {
		if code != 0 || !strings.Contains(stdout, want) {
			t.Fatalf("tree show missing %q:\n%s", want, stdout)
		}
	}
	code, stdout, _ = h.run("", "show", fmt.Sprintf("https://phone.lan/trees/%d", tree.ID))
	if code != 0 || !strings.Contains(stdout, fmt.Sprintf("tree %d  Stack  (manual)", tree.ID)) || !strings.Contains(stdout, "add_to_tree") {
		t.Fatalf("show tree link:\n%s", stdout)
	}
	code, stdout, _ = h.run("", "tree", "ls")
	if code != 0 || !strings.Contains(stdout, "Stack") || !strings.Contains(stdout, "ready 1, blocked 1") {
		t.Fatalf("tree ls:\n%s", stdout)
	}
	code, stdout, _ = h.run("", "tree", "ls", "--project", p.Title, "--json")
	if rows := decode[agenttools.TreesOut](t, stdout); code != 0 || len(rows.Trees) != 2 {
		t.Fatalf("tree ls --json: %s", stdout)
	}
	code, stdout, _ = h.run("", "tree", "edit", fmt.Sprint(tree.ID), "--title", "Stacked", "--start-mode", "auto", "--json")
	if tr := decode[client.TreeDetail](t, stdout); code != 0 || tr.Title != "Stacked" || tr.StartMode != db.StartAuto {
		t.Fatalf("tree edit: %s", stdout)
	}
	code, stdout, stderr = h.run("", "tree", "add", fmt.Sprint(tree.ID), fmt.Sprint(loose.ID), "--needs", fmt.Sprint(fourth.ID), "--json")
	if tr := decode[client.TreeDetail](t, stdout); code != 0 || len(tr.Levels) != 3 || !slices.Equal(tr.Levels[2], []int64{loose.ID}) {
		t.Fatalf("tree add: %d %s %s", code, stdout, stderr)
	}
	// task needs replaces needs (space or comma separated); none clears.
	code, stdout, stderr = h.run("", "task", "needs", fmt.Sprint(loose.ID), fmt.Sprint(third.ID), "--json")
	if tk := decode[client.Task](t, stdout); code != 0 || !slices.Equal(tk.Needs, []int64{third.ID}) {
		t.Fatalf("task needs: %d %s %s", code, stdout, stderr)
	}
	code, stdout, _ = h.run("", "task", "needs", fmt.Sprint(loose.ID), "--json")
	if tk := decode[client.Task](t, stdout); code != 0 || len(tk.Needs) != 0 {
		t.Fatalf("task needs clear: %s", stdout)
	}
	code, stdout, _ = h.run("", "task", "show", fmt.Sprint(loose.ID))
	if code != 0 || !strings.Contains(stdout, fmt.Sprintf("tree     %d Stacked (start auto)", tree.ID)) || !strings.Contains(stdout, "readiness ready") {
		t.Fatalf("task show tree + readiness:\n%s", stdout)
	}
	code, stdout, _ = h.run("", "tree", "rm", fmt.Sprint(loose.ID))
	if code != 0 || stdout != fmt.Sprintf("removed task %d from tree %d\n", loose.ID, tree.ID) {
		t.Fatalf("tree rm: %q", stdout)
	}
	if code, _, stderr := h.run("", "tree", "rm", fmt.Sprint(loose.ID)); code != 1 || !strings.Contains(stderr, "not in a tree") {
		t.Fatalf("tree rm twice: %d %s", code, stderr)
	}
	code, stdout, _ = h.run("", "tree", "start", fmt.Sprint(tree.ID))
	if code != 0 || !strings.Contains(stdout, fmt.Sprintf("started #%d [running] third", third.ID)) || strings.Contains(stdout, "fourth") {
		t.Fatalf("tree start:\n%s", stdout)
	}
	if code, _, _ := h.run("", "tree", "frob"); code != 2 {
		t.Fatalf("unknown tree verb exit %d", code)
	}
}

func TestSessionsAdoptCommand(t *testing.T) {
	h := newHarness(t)
	p := h.project("/w/cli-adopt")
	launch := func(title string) string {
		t.Helper()
		out, err := h.fake.LaunchThread(context.Background(), t3.LaunchInput{ProjectID: p.Bindings[0].ExternalProjectID, Title: title})
		if err != nil {
			t.Fatal(err)
		}
		if err := h.sync.RefreshScopedSession(context.Background(), execution.SessionRef{ConnectionID: h.store.LegacyT3ID(), ExternalSessionID: out.ThreadID}); err != nil {
			t.Fatal(err)
		}
		return out.ThreadID
	}
	me, other := launch("Mine"), launch("Theirs")
	if code, _, stderr := h.run("", "sessions", "adopt"); code != 1 || !strings.Contains(stderr, "session is required") {
		t.Fatalf("adopt without a session: %d %s", code, stderr)
	}
	if code, _, stderr := h.run("", "sessions", "adopt", other); code != 2 || !strings.Contains(stderr, "--target-session") {
		t.Fatalf("bare session id: %d %s", code, stderr)
	}
	h.caller = agentctx.Caller{Session: t3Session(me)}
	code, stdout, stderr := h.run("", "sessions", "adopt")
	if code != 0 || !strings.Contains(stdout, "adopted session as a task") || !strings.Contains(stdout, "adopted  tracks an existing session") {
		t.Fatalf("adopt own: %d %s %s", code, stdout, stderr)
	}
	code, stdout, _ = h.run("", "sessions", "adopt", "--json")
	mine := decode[agenttools.AdoptOut](t, stdout)
	if code != 0 || mine.Created || !mine.Task.Adopted || mine.Task.T3ThreadID == nil || *mine.Task.T3ThreadID != me {
		t.Fatalf("adopt own again: %s", stdout)
	}
	code, stdout, stderr = h.run("", "tree", "new", "--title", "T", fmt.Sprint(mine.Task.ID), "--json")
	if code != 0 {
		t.Fatalf("tree new: %s", stderr)
	}
	tree := decode[client.TreeDetail](t, stdout)
	code, stdout, stderr = h.run("", "sessions", "adopt", "--target-connection", "1", "--target-session", other, "--title", "Side", "--initial-note", "n", "--tree", fmt.Sprint(tree.ID), "--json")
	got := decode[agenttools.AdoptOut](t, stdout)
	if code != 0 || !got.Created || got.Task.Title != "Side" || got.Task.TreeID == nil || *got.Task.TreeID != tree.ID {
		t.Fatalf("adopt other: %d %s %s", code, stdout, stderr)
	}
	code, stdout, stderr = h.run("", "task", "notes", fmt.Sprint(got.Task.ID), "--json")
	if code != 0 {
		t.Fatalf("adopt initial note: %s", stderr)
	}
	notes := decode[db.JournalPage](t, stdout)
	if len(notes.Entries) != 1 || notes.Entries[0].Text != "n" {
		t.Fatalf("adopt initial note %+v", notes)
	}
	if code, _, _ := h.run("", "sessions", "adopt", "a", "b"); code != 2 {
		t.Fatalf("two thread ids exit %d", code)
	}
}

func TestTreeFlags(t *testing.T) {
	h := newHarness(t)
	h.project("/w/cli-tree-flags")
	h.caller = agentctx.Caller{Cwd: "/w/cli-tree-flags"}
	add := func(args ...string) client.Task {
		t.Helper()
		code, stdout, stderr := h.run("", append([]string{"task", "add", "--project", "1", "--prompt", "p", "--json"}, args...)...)
		if code != 0 {
			t.Fatalf("add %v: %s", args, stderr)
		}
		return decode[client.Task](t, stdout)
	}
	a := add("--title", "a")
	b := add("--title", "b", "--needs", fmt.Sprint(a.ID), "--start-mode", "manual")
	if b.TreeID == nil || b.StartMode == nil || *b.StartMode != db.StartManual {
		t.Fatalf("--start-mode manual: %+v", b)
	}
	c := add("--title", "c", "--tree", fmt.Sprint(*b.TreeID), "--needs", fmt.Sprintf("%d,%d", a.ID, b.ID),
		"--base-ref", "origin/main", "--base", "predecessor", "--base-task", fmt.Sprint(b.ID))
	if c.TreeID == nil || *c.TreeID != *b.TreeID || c.Workspace.BaseTaskID == nil || *c.Workspace.BaseTaskID != b.ID {
		t.Fatalf("--tree --base-task: %+v", c)
	}
	code, stdout, _ := h.run("", "task", "show", fmt.Sprint(c.ID))
	if code != 0 || !strings.Contains(stdout, fmt.Sprintf("worktree from #%d's branch", b.ID)) {
		t.Fatalf("show base task:\n%s", stdout)
	}
	code, stdout, _ = h.run("", "task", "edit", fmt.Sprint(c.ID), "--start-mode", "manual", "--base-task", fmt.Sprint(a.ID), "--json")
	if tk := decode[client.Task](t, stdout); code != 0 || tk.StartMode == nil || *tk.StartMode != db.StartManual ||
		tk.Workspace.BaseTaskID == nil || *tk.Workspace.BaseTaskID != a.ID {
		t.Fatalf("edit --start-mode manual --base-task: %s", stdout)
	}
	code, stdout, _ = h.run("", "task", "edit", fmt.Sprint(c.ID), "--start-mode", "inherit", "--json")
	if tk := decode[client.Task](t, stdout); code != 0 || tk.StartMode != nil || tk.EffectiveStartMode == nil || *tk.EffectiveStartMode != db.StartAuto {
		t.Fatalf("edit --start-mode inherit: %s", stdout)
	}
	if code, _, stderr := h.run("", "task", "edit", fmt.Sprint(c.ID), "--start-mode", "later"); code != 1 || !strings.Contains(stderr, "invalid startMode") {
		t.Fatalf("bad --start-mode: %d %s", code, stderr)
	}
	if code, _, _ := h.run("", "task", "add", "--project", "1", "--title", "x", "--prompt", "p", "--tree", "nope"); code != 2 {
		t.Fatalf("bad --tree exit %d", code)
	}
}

func TestScheduleAndContextCommands(t *testing.T) {
	h := newHarness(t)
	h.project("/w/cli-s")
	h.caller = agentctx.Caller{Cwd: "/w/cli-s/pkg"}
	code, stdout, stderr := h.run("", "context", "--json")
	if cx := decode[agenttools.ContextOut](t, stdout); code != 0 || cx.Project != nil || cx.ProjectSource != "" {
		t.Fatalf("context: %d %s %s", code, stdout, stderr)
	}
	code, stdout, stderr = h.run("", "schedule", "add", "--project", "1", "--title", "n", "--prompt", "p", "--cron", "0 2 * * *", "--tz", "UTC", "--json")
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
	h.execs = nil
	if code, _, _ := h.run("", "setup", "mcp", "--claude", "--t3-session-env"); code != 2 || len(h.execs) != 0 {
		t.Fatalf("removed --t3-session-env still accepted: %d %v", code, h.execs)
	}
	// A unix socket URL passes through unchanged to the stdio server; agents
	// cannot reach HTTP MCP over a socket.
	h.execs = nil
	h.url = "unix:///run/stage/stage.sock"
	if code, _, stderr := h.run("", "setup", "mcp", "--claude"); code != 0 {
		t.Fatalf("unix: %s", stderr)
	}
	if got := fmt.Sprint(h.execs); got != "[[claude mcp remove --scope user stage] [claude mcp add --scope user -e STAGE_URL=unix:///run/stage/stage.sock stage -- /usr/local/bin/stage mcp]]" {
		t.Fatalf("unix execs %s", got)
	}
	h.execs = nil
	if code, _, stderr := h.run("", "setup", "mcp", "--claude", "--http"); code != 2 || !strings.Contains(stderr, "--http") || h.execs != nil {
		t.Fatalf("unix --http: code %d, execs %v, stderr %s", code, h.execs, stderr)
	}
}

// The CLI reaches a server that listens only on a unix socket
// (STAGE_URL=unix:///abs/path.sock).
func TestCLIOverUnixSocket(t *testing.T) {
	h := newHarness(t)
	dir, err := os.MkdirTemp("", "stc") // short: unix socket paths are length-limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	addr, err := listen.Parse("unix:" + filepath.Join(dir, "stage.sock"))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := addr.Listen()
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h.handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	h.url = addr.URL()
	if !strings.HasPrefix(h.url, "unix:///") {
		t.Fatalf("socket URL %q", h.url)
	}

	p := h.project("/w/cli-unix")
	code, stdout, stderr := h.run("", "task", "add", "--project", "1", "--project", fmt.Sprint(p.ID), "--title", "Over unix", "--prompt", "go", "--json")
	if code != 0 {
		t.Fatalf("task add over unix socket: %d %s", code, stderr)
	}
	task := decode[client.Task](t, stdout)
	code, stdout, stderr = h.run("", "task", "show", fmt.Sprint(task.ID), "--json")
	if code != 0 || decode[client.TaskDetail](t, stdout).Title != "Over unix" {
		t.Fatalf("task show over unix socket: %d %s %s", code, stdout, stderr)
	}
	code, stdout, _ = h.run("", "context", "--json")
	if code != 0 || decode[agenttools.ContextOut](t, stdout).APIURL != h.url {
		t.Fatalf("context apiUrl: %s", stdout)
	}
}

func TestHandoffFlags(t *testing.T) {
	h := newHarness(t)
	h.project("/w/cli-handoff")
	h.caller = agentctx.Caller{Cwd: "/w/cli-handoff"}
	code, stdout, stderr := h.run("", "task", "add", "--project", "1", "--title", "a", "--prompt", "x", "--json")
	if code != 0 {
		t.Fatalf("add: %d %s", code, stderr)
	}
	a := decode[client.Task](t, stdout)
	if !a.Handoff {
		t.Fatalf("handoff defaults on: %+v", a)
	}
	code, stdout, stderr = h.run("", "task", "add", "--project", "1", "--title", "b", "--prompt", "y", "--needs", fmt.Sprint(a.ID), "--handoff=false", "--json")
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
}

func TestBaseFlag(t *testing.T) {
	h := newHarness(t)
	h.project("/w/cli-base")
	h.caller = agentctx.Caller{Cwd: "/w/cli-base"}
	code, stdout, stderr := h.run("", "task", "add", "--project", "1", "--title", "a", "--prompt", "x", "--json")
	if code != 0 {
		t.Fatalf("add: %d %s", code, stderr)
	}
	a := decode[client.Task](t, stdout)
	code, stdout, stderr = h.run("", "task", "add", "--project", "1", "--title", "b", "--prompt", "y", "--needs", fmt.Sprint(a.ID),
		"--base-ref", "origin/main", "--base", "predecessor", "--json")
	if code != 0 {
		t.Fatalf("add b: %d %s", code, stderr)
	}
	b := decode[client.Task](t, stdout)
	if !b.Workspace.FromPredecessor() || b.Workspace.BaseRef != "origin/main" {
		t.Fatalf("--base predecessor: %+v", b.Workspace)
	}
	code, stdout, _ = h.run("", "task", "show", fmt.Sprint(b.ID))
	if code != 0 || !strings.Contains(stdout, fmt.Sprintf("worktree from #%d's branch (configured ref origin/main, branch -)", a.ID)) {
		t.Fatalf("show: %s", stdout)
	}
	code, stdout, _ = h.run("", "task", "edit", fmt.Sprint(b.ID), "--base", "ref", "--json")
	if w := decode[client.Task](t, stdout).Workspace; code != 0 || w.FromPredecessor() || w.BaseRef != "origin/main" {
		t.Fatalf("edit --base ref: %s", stdout)
	}
	if code, _, _ = h.run("", "task", "add", "--project", "1", "--title", "c", "--prompt", "z", "--base", "predecessor"); code == 0 {
		t.Fatal("--base without --base-ref must fail")
	}
}
