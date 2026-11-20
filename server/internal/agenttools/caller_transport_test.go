package agenttools_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/cli"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The second harness only implements operations exercised by this workflow.
type callerHarness struct {
	execution.Adapter
	offline atomic.Bool
}

func (h *callerHarness) Destinations(context.Context, db.SessionRef) execution.Links {
	return execution.Links{Web: "https://example.invalid/session"}
}
func (h *callerHarness) Health(context.Context) execution.Health {
	return execution.Health{Availability: execution.Availability{Available: !h.offline.Load()}}
}
func (h *callerHarness) Features(context.Context) (execution.Capabilities, error) {
	return execution.Capabilities{
		Operations: map[string]execution.Availability{"adopt": {Available: true}},
		Models: []execution.Model{
			{Choice: execution.Choice{ID: "blocked", Availability: execution.Availability{Reason: "read-only subscription"}}},
			{Choice: execution.Choice{ID: "remote-model", Availability: execution.Availability{Available: true}},
				Options: []execution.Option{{ID: "cache", Values: []execution.OptionValue{execution.BoolValue(true), execution.StringValue("true")}}}},
		},
	}, nil
}
func (h *callerHarness) Workspaces(context.Context) ([]execution.WorkspaceInfo, error) {
	return []execution.WorkspaceInfo{{ID: "external", WorkspaceRoot: "volume://remote/root"}}, nil
}
func (h *callerHarness) Observe(_ context.Context, ref db.SessionRef) (execution.Observation, error) {
	if h.offline.Load() {
		return execution.Observation{}, execution.ErrUnavailable
	}
	now := time.Now().UTC()
	return execution.Observation{Project: "external", Session: ref, Title: "OpenCode plan", State: "done", CreatedAt: now, UpdatedAt: now, Directory: "volume://remote/worktree", Reply: &execution.Reply{Text: "retained reply", UpdatedAt: now}, Links: execution.Links{Web: "https://example.invalid/session"}}, nil
}

func TestMCPSubprocess(t *testing.T) {
	if os.Getenv("STAGE_TEST_CALLER_STDIO") != "1" {
		return
	}
	os.Exit(cli.Run(context.Background(), []string{"mcp"}, cli.OSEnv()))
}

// headerTransport sends the caller header on every request.
type headerTransport struct{ value string }

func (h headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set(agentctx.CallerHeader, h.value)
	return http.DefaultTransport.RoundTrip(r)
}

// transportClient connects over HTTP MCP (caller is its Stage-Caller-Session
// header) or stdio (caller is the subprocess's STAGE_CALLER_SESSION, which a
// shared MCP server ignores).
func transportClient(t *testing.T, e *env, transport, caller string) (*mcp.ClientSession, error) {
	t.Helper()
	var tr mcp.Transport
	if transport == "http" {
		srv := httptest.NewServer(agenttools.HTTPHandler(&agenttools.Ops{Client: e.client, LocalFiles: true}))
		t.Cleanup(srv.Close)
		st := &mcp.StreamableClientTransport{Endpoint: srv.URL}
		if caller != "" {
			st.HTTPClient = &http.Client{Transport: headerTransport{caller}}
		}
		tr = st
	} else {
		cmd := exec.Command(os.Args[0], "-test.run=^TestMCPSubprocess$")
		cmd.Env = append(os.Environ(), "STAGE_TEST_CALLER_STDIO=1", "STAGE_URL="+e.url)
		if caller != "" {
			cmd.Env = append(cmd.Env, agentctx.CallerEnv+"="+caller)
		}
		tr = &mcp.CommandTransport{Command: cmd}
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "caller-test", Version: "1"}, nil).Connect(t.Context(), tr, nil)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { cs.Close() })
	return cs, nil
}

func mustTransport(t *testing.T, e *env, transport, caller string) *mcp.ClientSession {
	t.Helper()
	cs, err := transportClient(t, e, transport, caller)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func TestScopedCallerTransports(t *testing.T) {
	for _, transport := range []string{"http", "stdio"} {
		t.Run(transport, func(t *testing.T) {
			h := &callerHarness{}
			e := harnessEnv(t, h)
			t3project := e.project("/w/plan")
			id := e.session(t3project, "T3 plan")
			other, err := e.store.InsertProject(t.Context(), db.Project{Title: "Remote", Bindings: []db.ProjectBinding{{ConnectionID: 7, ExternalProjectID: "external", Workspace: "volume://remote/root"}}})
			if err != nil {
				t.Fatal(err)
			}
			cs := mustTransport(t, e, transport, "")
			ref := db.SessionRef{ConnectionID: 7, ExternalSessionID: id}
			meta := mcp.Meta{"ai.opencode/sessionID": id}
			calls := []struct {
				args                map[string]any
				meta                mcp.Meta
				connection, project int64
			}{
				{map[string]any{"callerSession": db.SessionRef{ConnectionID: 1, ExternalSessionID: id}}, nil, 1, t3project.ID}, {nil, meta, 7, other.ID},
			}
			owners := make([]int64, 2)
			var group sync.WaitGroup
			for i, tc := range calls {
				group.Go(func() {
					var ctx agenttools.ContextOut
					if msg := callMeta(t, cs, "get_context", tc.args, tc.meta, &ctx); msg != "" || ctx.Project == nil || ctx.Project.ID != tc.project || ctx.Session == nil || ctx.Session.ConnectionID != tc.connection {
						t.Errorf("context %d: %+v %s", i, ctx, msg)
						return
					}
					var got agenttools.AdoptOut
					if msg := callMeta(t, cs, "adopt_session", tc.args, tc.meta, &got); msg != "" || got.Task.Session == nil || got.Task.Session.ConnectionID != tc.connection {
						t.Errorf("adopt %d: %+v %s", i, got, msg)
						return
					}
					owners[i] = got.Task.ID
				})
			}
			group.Wait()
			if t.Failed() {
				return
			}
			if owners[0] == owners[1] {
				t.Fatal("same external ID conflated owners")
			}
			var task client.Task
			args := map[string]any{"title": "mixed tree", "prompt": "report", "afterMe": true, "connectionId": 1, "needs": []int64{owners[0]}, "initialNote": "initial"}
			if msg := callMeta(t, cs, "create_task", args, meta, &task); msg != "" {
				t.Fatal(msg)
			}
			if task.ConnectionID != 1 || task.ProjectID != other.ID || task.OriginSession == nil || *task.OriginSession != ref || len(task.Needs) != 2 || task.Readiness == nil || *task.Readiness != "blocked" {
				t.Fatalf("mixed task %+v", task)
			}
			var rows agenttools.TasksOut
			mustCall(t, cs, "list_tasks", map[string]any{"originSession": ref}, &rows)
			if rows.Total != 1 || rows.Tasks[0].ID != task.ID {
				t.Fatal(rows)
			}
			var page db.JournalPage
			mustCall(t, cs, "read_journal", map[string]any{"taskId": task.ID}, &page)
			if len(page.Entries) != 1 || page.Entries[0].SourceSession == nil || *page.Entries[0].SourceSession != ref {
				t.Fatal(page)
			}
			note := map[string]any{"taskId": task.ID, "requestId": "note", "text": "outcome"}
			for _, bad := range []mcp.Meta{{"ai.opencode/sessionID": false}, {"ai.opencode/sessionID": ""}} {
				if msg := callMeta(t, cs, "append_note", note, bad, nil); msg == "" {
					t.Fatal("accepted malformed metadata")
				}
			}
			note["callerSession"] = db.SessionRef{ConnectionID: 1, ExternalSessionID: id}
			if msg := callMeta(t, cs, "append_note", note, meta, nil); !strings.Contains(msg, "conflict") {
				t.Fatal("caller mismatch", msg)
			}
			delete(note, "callerSession")
			var entry db.JournalEntry
			if msg := callMeta(t, cs, "append_note", note, meta, &entry); msg != "" || entry.SourceSession == nil || *entry.SourceSession != ref {
				t.Fatal(entry, msg)
			}
			mustCall(t, cs, "get_task", map[string]any{"id": task.ID}, &task)
			if task.AcceptanceID != nil || task.Readiness == nil || *task.Readiness != "blocked" {
				t.Fatal("report released dependent", task)
			}
			note["requestId"] = "anon"
			entry = db.JournalEntry{}
			mustCall(t, cs, "append_note", note, &entry)
			if entry.SourceSession != nil {
				t.Fatal("shared connection leaked caller", entry)
			}
			h.offline.Store(true)
			var ctx agenttools.ContextOut
			mustCall(t, cs, "get_context", map[string]any{"callerSession": ref}, &ctx)
			if ctx.CallerTaskID == nil || *ctx.CallerTaskID != owners[1] || ctx.TreeID == nil || *ctx.TreeID != *task.TreeID {
				t.Fatal(ctx)
			}
			var d agenttools.RefOut
			mustCall(t, cs, "read_ref", map[string]any{"ref": fmt.Sprintf("#%d", owners[1])}, &d)
			if d.Task == nil || d.Task.FinalReply == nil || d.Task.FinalReply.Text != "retained reply" || d.Links.Web == "" || d.Session != nil {
				t.Fatal(d)
			}
			mustCall(t, cs, "adopt_session", map[string]any{"session": ref}, nil)
			var anon agenttools.ContextOut
			mustCall(t, cs, "get_context", nil, &anon)
			if anon.Session != nil || anon.CallerTaskID != nil {
				t.Fatal(anon)
			}
			t3ref := fmt.Sprintf("1:%s", id)
			named := mustTransport(t, e, transport, t3ref)
			var me agenttools.ContextOut
			mustCall(t, named, "get_context", nil, &me)
			if transport == "stdio" {
				if me.Session != nil {
					t.Fatal("shared stdio MCP server used its environment caller", me)
				}
				return
			}
			if me.ThreadID != id || me.CallerTaskID == nil || *me.CallerTaskID != owners[0] {
				t.Fatal(me)
			}
			if msg := callMeta(t, named, "get_context", nil, meta, nil); !strings.Contains(msg, "conflicts") {
				t.Fatal("header and metadata disagree", msg)
			}
			if _, err := transportClient(t, e, "http", "not-a-session"); err == nil || !strings.Contains(err.Error(), "Bad Request") {
				t.Fatal("accepted malformed caller header", err)
			}
		})
	}
}

func TestDestinationAndScheduleProvenance(t *testing.T) {
	e := harnessEnv(t, &callerHarness{})
	ctx := t.Context()
	p := e.project("/w/both")
	_, err := e.store.BindProject(ctx, db.ProjectBinding{ProjectID: p.ID, ConnectionID: 7, ExternalProjectID: "external", Workspace: "volume://remote/root"})
	if err != nil {
		t.Fatal(err)
	}
	cs := connect(t, &agenttools.Ops{Client: e.client})
	ref := db.SessionRef{ConnectionID: 7, ExternalSessionID: "remote-caller"}
	args := map[string]any{"project": fmt.Sprint(p.ID), "title": "nightly", "prompt": "test", "callerSession": ref}
	if msg := call(t, cs, "create_task", args, nil); !strings.Contains(msg, "select connectionId") {
		t.Fatal(msg)
	}
	args["connectionId"] = 7
	args["executionConfig"] = map[string]any{"model": "blocked"}
	if msg := call(t, cs, "create_task", args, nil); !strings.Contains(msg, "read-only subscription") {
		t.Fatal(msg)
	}
	args["executionConfig"] = map[string]any{"model": "remote-model", "options": []map[string]any{{"id": "cache", "value": true}}}
	args["cron"] = "0 2 * * *"
	args["tz"] = "UTC"
	args["notes"] = "template"
	var sched db.Schedule
	mustCall(t, cs, "create_schedule", args, &sched)
	if sched.ConnectionID != 7 || sched.OriginSession == nil || *sched.OriginSession != ref {
		t.Fatal(sched)
	}
	task, err := e.store.RunScheduleNow(ctx, sched.ID)
	if err != nil || task.ConnectionID != 7 || task.OriginSession == nil || *task.OriginSession != ref {
		t.Fatal(task, err)
	}
	if task.ExecutionConfig == nil || task.ExecutionConfig.Model != "remote-model" || len(task.ExecutionConfig.Options) != 1 || task.ExecutionConfig.Options[0].Value != execution.BoolValue(true) {
		t.Fatalf("schedule lost execution config: %+v", task.ExecutionConfig)
	}
	page, err := e.store.ReadJournal(ctx, db.JournalOwner{TaskID: &task.ID}, db.JournalQuery{})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].SourceSession == nil || *page.Entries[0].SourceSession != ref {
		t.Fatal(page, err)
	}
	unbound, err := e.store.InsertProject(ctx, db.Project{Title: "Offline plan"})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	env := cli.OSEnv()
	env.Stdout = &out
	env.Stderr = &out
	env.Caller = agentctx.Caller{Session: &db.SessionRef{ConnectionID: 1, ExternalSessionID: "inherited"}}
	code := cli.Run(ctx, []string{"task", "add", "--url", e.url, "--project", fmt.Sprint(unbound.ID), "--title", "CLI", "--prompt", "Work", "--caller-connection", "7", "--caller-session", ref.ExternalSessionID, "--json"}, env)
	var created client.Task
	if code != 0 || json.Unmarshal(out.Bytes(), &created) != nil || created.ConnectionID != 7 || created.OriginSession == nil || *created.OriginSession != ref {
		t.Fatal(code, out.String())
	}
}

func TestAtomicScopedAdoption(t *testing.T) {
	e := harnessEnv(t, &callerHarness{})
	p, err := e.store.InsertProject(t.Context(), db.Project{Title: "Rollback", Bindings: []db.ProjectBinding{{ConnectionID: 7, ExternalProjectID: "external", Workspace: "volume://remote/root"}}})
	if err != nil {
		t.Fatal(err)
	}
	cs := connect(t, &agenttools.Ops{Client: e.client})
	ref := db.SessionRef{ConnectionID: 7, ExternalSessionID: "opaque/id"}
	if msg := call(t, cs, "create_task", map[string]any{"project": fmt.Sprint(p.ID), "callerSession": ref, "afterMe": true, "needs": []int{9999}, "title": "invalid dependency", "prompt": "work"}, nil); msg == "" {
		t.Fatal("accepted invalid need")
	}
	var tasks agenttools.TasksOut
	mustCall(t, cs, "list_tasks", nil, &tasks)
	if tasks.Total != 0 {
		t.Fatal("rolled back adoption left tasks", tasks)
	}
	replies := make([]agenttools.AdoptOut, 2)
	var group sync.WaitGroup
	for i := range replies {
		group.Go(func() {
			if msg := call(t, cs, "adopt_session", map[string]any{"session": ref, "callerSession": db.SessionRef{ConnectionID: 1, ExternalSessionID: "different-session"}, "initialNote": "my note"}, &replies[i]); msg != "" {
				t.Error(msg)
			}
		})
	}
	group.Wait()
	if replies[0].Created == replies[1].Created || replies[0].Task.ID != replies[1].Task.ID {
		t.Fatal(replies)
	}
	var page db.JournalPage
	mustCall(t, cs, "read_journal", map[string]any{"taskId": replies[0].Task.ID}, &page)
	if len(page.Entries) != 1 || page.Entries[0].SourceSession == nil || *page.Entries[0].SourceSession != (db.SessionRef{ConnectionID: 1, ExternalSessionID: "different-session"}) {
		t.Fatal(page)
	}
	var scoped agenttools.RefOut
	mustCall(t, cs, "read_ref", map[string]any{"ref": "/connections/7/sessions/opaque%2Fid"}, &scoped)
	if scoped.SessionRef == nil || *scoped.SessionRef != ref || scoped.Task == nil || scoped.Task.ID != replies[0].Task.ID {
		t.Fatal(scoped)
	}
}
