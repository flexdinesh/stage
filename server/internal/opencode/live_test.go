package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Opt in with an absolute path to the supported OpenCode executable. All
// harness data, project configuration and model traffic belong to this test.
type liveFixture struct {
	a                                  *Adapter
	dir, project, home, config, binary string
	stop                               func()
	turns                              atomic.Int32
	// script, when set, answers a model call first (true: answered).
	script atomic.Pointer[func(w http.ResponseWriter, r *http.Request, body []byte) bool]
}

func (f *liveFixture) start(t *testing.T, port string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), f.binary, "serve", "--hostname", "127.0.0.1", "--port", port)
	cmd.Dir = f.dir
	cmd.Env = []string{"HOME=" + f.home, "XDG_RUNTIME_DIR=" + f.home, "TMPDIR=" + f.home, "PATH=" + os.Getenv("PATH"), "OPENCODE_PASSWORD=stage-test", "OPENCODE_DISABLE_AUTOUPDATE=1", "OPENCODE_DISABLE_MODELS_FETCH=1", "OPENCODE_DISABLE_FREE_MODELS=1", "OPENCODE_CONFIG_CONTENT=" + f.config}
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	cmd.Stderr = os.Stderr
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	f.stop = sync.OnceFunc(func() { _ = cmd.Process.Signal(os.Interrupt); _ = cmd.Wait() })
	t.Cleanup(f.stop)
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if url, ok := strings.CutPrefix(scanner.Text(), "server listening on "); ok {
				select {
				case ready <- url:
				default:
				}
			}
		}
	}()
	select {
	case url := <-ready:
		f.a, e = New(2, Settings{Endpoint: url, Username: "opencode", Password: "stage-test"})
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("isolated OpenCode did not start")
	}
}
func newFixture(t *testing.T) *liveFixture {
	t.Helper()
	binary := os.Getenv("STAGE_TEST_OPENCODE")
	if binary == "" {
		t.Skip("set STAGE_TEST_OPENCODE to the OpenCode 2.3.0 binary")
	}
	f := &liveFixture{dir: t.TempDir(), home: t.TempDir(), binary: binary}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		f.turns.Add(1)
		if script := f.script.Load(); script != nil && (*script)(w, r, b) {
			return
		}
		if strings.Contains(string(b), "fixture-failure") {
			http.Error(w, `{"error":{"message":"fixture failure"}}`, 400)
			return
		}
		if strings.Contains(string(b), "fixture-interrupt") {
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: "+`{"id":"one","object":"chat.completion.chunk","created":1,"model":"test","choices":[{"index":0,"delta":{"role":"assistant","content":"Stage live reply"},"finish_reason":null}]}`+"\n\n")
		fmt.Fprint(w, "data: "+`{"id":"one","object":"chat.completion.chunk","created":1,"model":"test","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(model.Close)
	f.config = fmt.Sprintf(`{"enabled_providers":["fixture"],"model":"fixture/test","permission":{"bash":"ask","question":"allow"},"provider":{"fixture":{"name":"Fixture","npm":"@ai-sdk/openai-compatible","options":{"baseURL":%q,"apiKey":"fixture"},"models":{"test":{"name":"Local test"}}}}}`, model.URL+"/v1")
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=Fixture", "-c", "user.email=test@invalid", "commit", "-qm", "fixture", "--allow-empty"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = f.dir
		cmd.Env = []string{"HOME=" + f.home, "PATH=" + os.Getenv("PATH")}
		if b, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("git %v: %s", e, b)
		}
	}
	f.start(t, "0")
	var loc struct{ Project struct{ ID string } }
	if e := f.a.get(t.Context(), "/api/location"+queryDirectory(f.dir), &loc); e != nil {
		t.Fatal(e)
	}
	f.project = loc.Project.ID
	return f
}
func (f *liveFixture) intent(prompt, kind string) execution.Intent {
	in := execution.Intent{RequestID: prompt, ConnectionID: 2, Project: f.project, Directory: f.dir, Title: "Stage test", Prompt: prompt, Config: execution.Config{Model: "fixture/test"}}
	if kind == "worktree" {
		in.Config.Options = []execution.ModelOption{{ID: optionWorktree, Value: execution.BoolValue(true)}}
	}
	return in
}
func observeUntil(t *testing.T, a *Adapter, ref execution.SessionRef, state string) execution.Observation {
	t.Helper()
	var o execution.Observation
	var e error
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		o, e = a.Observe(t.Context(), ref)
		if e == nil && o.State == state {
			return o
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("want %s, got %+v, error %v", state, o, e)
	return o
}
func TestLiveOpenCode(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	if h := f.a.Health(ctx); !h.Available {
		t.Fatal(h.Reason)
	}
	if c, e := f.a.FeaturesForWorkspace(ctx, f.project, f.dir); e != nil || len(c.Models) == 0 || len(c.Modes) == 0 {
		t.Fatalf("catalog %+v: %v", c, e)
	}
	for _, tc := range []struct{ prompt, kind, end string }{{"fixture-success", "worktree", "done"}, {"fixture-failure", "root", "failed"}, {"fixture-interrupt", "root", "interrupted"}} {
		t.Run(tc.prompt, func(t *testing.T) {
			in := f.intent(tc.prompt, tc.kind)
			var cp execution.Checkpoint
			save := func(_ context.Context, v execution.Checkpoint) error { cp = v; return nil }
			o, e := f.a.Launch(ctx, in, cp, save)
			if e != nil {
				o, e = f.a.Launch(ctx, in, cp, save)
			}
			if e != nil {
				t.Fatal(e)
			}
			if tc.kind == "worktree" && (o.Directory == f.dir || o.Directory == "") {
				t.Fatalf("wrong workspace: %+v", o)
			}
			if tc.kind == "worktree" {
				inventory, e := f.a.worktrees(ctx, f.project)
				if e != nil {
					t.Fatal(e)
				}
				found := false
				for _, workspace := range inventory {
					if workspace.Directory == o.Directory {
						found = true
					}
				}
				if !found {
					t.Fatal("harness-managed worktree absent from discovery")
				}
			}
			if tc.end == "interrupted" {
				observeUntil(t, f.a, o.Session, "running")
				if e = f.a.Stop(ctx, o.Session); e != nil {
					t.Fatal(e)
				}
			}
			o = observeUntil(t, f.a, o.Session, tc.end)
			if tc.end == "done" && (o.Reply == nil || o.Reply.Text != "Stage live reply") {
				t.Fatalf("final reply %+v", o.Reply)
			}
			if ref, e := f.a.ResolveLink(ctx, o.Links.Web); e != nil || ref != o.Session {
				t.Fatalf("native route %s: %v", o.Links.Web, e)
			}
			first, e := f.a.Opening(ctx, o.Session, false)
			if e != nil || first.Text != tc.prompt {
				t.Fatalf("opening %+v %v", first, e)
			}
		})
	}
}

func TestLiveOpenCodeSessionSearch(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	features, err := f.a.Features(ctx)
	if err != nil || !features.Operations[execution.OpSessionSearch].Available {
		t.Fatalf("roster capability %+v: %v", features.Operations[execution.OpSessionSearch], err)
	}
	ids := []string{"ses_stage_roster_one", "ses_stage_roster_two"}
	for _, id := range ids {
		var created envelope[session]
		input := map[string]any{"id": id, "title": "Stage roster", "location": location{Directory: f.dir}}
		if err := f.a.request(ctx, http.MethodPost, "/api/session", input, &created); err != nil || created.Data.ID != id {
			t.Fatalf("create fixture session %s: %+v, %v", id, created.Data, err)
		}
	}

	seen := make(map[string]bool)
	q := execution.SessionSearchQuery{Query: "STAGE ROSTER", Limit: 1}
	for range len(ids) + 1 {
		page, err := f.a.SearchSessions(ctx, q)
		if err != nil || len(page.Sessions) > 1 {
			t.Fatalf("bounded roster %+v: %v", page, err)
		}
		for _, item := range page.Sessions {
			id := item.Session.ExternalSessionID
			if seen[id] || item.Session.ConnectionID != f.a.connection || item.Workspace != f.dir || !item.Eligibility.Available {
				t.Fatalf("invalid roster item %+v", item)
			}
			seen[id] = true
		}
		q.Cursor = page.NextCursor
		if q.Cursor == "" {
			break
		}
	}
	if !seen[ids[0]] || !seen[ids[1]] || len(seen) != len(ids) || q.Cursor != "" || f.turns.Load() != 0 {
		t.Fatalf("roster traversal: IDs %v, at end %v, model calls %d", seen, q.Cursor == "", f.turns.Load())
	}
	q.Cursor = "not-a-valid-cursor"
	if _, err := f.a.SearchSessions(ctx, q); !errors.Is(err, execution.ErrSessionSearchCursor) {
		t.Fatalf("invalid native cursor: %v", err)
	}
}

// Browsing lists folders on the isolated host and saves no project.
func TestLiveOpenCodeBrowse(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	other := t.TempDir()
	for _, sub := range []string{"app", ".hidden", "app/src"} {
		if err := os.Mkdir(other+"/"+sub, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(other+"/notes.txt", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := f.a.projects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.a.Features(ctx)
	if err != nil || !c.Operations[execution.OpDirectoryBrowse].Available {
		t.Fatalf("declaration %+v %v", c.Operations[execution.OpDirectoryBrowse], err)
	}
	start, err := f.a.Browse(ctx, execution.BrowseRequest{})
	if err != nil || start.Path != path.Dir(f.dir) {
		t.Fatalf("default start %+v %v, want %s", start, err, path.Dir(f.dir))
	}
	got, err := f.a.Browse(ctx, execution.BrowseRequest{Path: other})
	if err != nil || len(got.Entries) != 1 || got.Entries[0] != (execution.DirEntry{Name: "app", Path: other + "/app"}) {
		t.Fatalf("listing %+v %v", got, err)
	}
	if got, err = f.a.Browse(ctx, execution.BrowseRequest{Path: other, ShowHidden: true}); err != nil || len(got.Entries) != 2 {
		t.Fatalf("hidden %+v %v", got, err)
	}
	var invalid *execution.Invalid
	for _, p := range []string{other + "/missing", other + "/notes.txt"} {
		if _, err := f.a.Browse(ctx, execution.BrowseRequest{Path: p}); !errors.As(err, &invalid) {
			t.Fatalf("%s: %v", p, err)
		}
	}
	after, err := f.a.projects(ctx)
	if err != nil || len(after) != len(before) {
		t.Fatalf("browsing saved a project: %d -> %d %v", len(before), len(after), err)
	}
}

// modelCall is the part of a chat completion request the scripted model reads.
type modelCall struct {
	Tools    []json.RawMessage `json:"tools"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
}

// text is a message's content as text: a string, or its text parts joined.
func messageText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct{ Text string }
	_ = json.Unmarshal(raw, &parts)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, p.Text)
	}
	return strings.Join(out, "\n")
}

func streamText(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	delta, _ := json.Marshal(map[string]any{"role": "assistant", "content": text})
	fmt.Fprintf(w, "data: {\"id\":\"one\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"test\",\"choices\":[{\"index\":0,\"delta\":%s,\"finish_reason\":null}]}\n\n", delta)
	fmt.Fprint(w, "data: "+`{"id":"one","object":"chat.completion.chunk","created":1,"model":"test","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
}

func streamToolCall(w http.ResponseWriter, name string, args any) {
	w.Header().Set("Content-Type", "text/event-stream")
	input, _ := json.Marshal(args)
	call, _ := json.Marshal(map[string]any{"role": "assistant", "tool_calls": []map[string]any{{"index": 0, "id": "call_" + name, "type": "function", "function": map[string]any{"name": name, "arguments": string(input)}}}})
	fmt.Fprintf(w, "data: {\"id\":\"two\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"test\",\"choices\":[{\"index\":0,\"delta\":%s,\"finish_reason\":null}]}\n\n", call)
	fmt.Fprint(w, "data: "+`{"id":"two","object":"chat.completion.chunk","created":1,"model":"test","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
}

// pendingRequest polls until the session lists one request.
func pendingRequest(t *testing.T, a *Adapter, ref execution.SessionRef) execution.Request {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		rs, err := a.Requests(t.Context(), ref)
		if err == nil && len(rs) == 1 {
			return rs[0]
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("no pending request")
	return execution.Request{}
}

// replyUntil polls until the session's final reply is text.
func replyUntil(t *testing.T, a *Adapter, ref execution.SessionRef, text string) {
	t.Helper()
	var o execution.Observation
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var err error
		if o, err = a.Observe(t.Context(), ref); err == nil && o.State == "done" && o.Reply != nil && o.Reply.Text == text {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("want reply %q, got %+v", text, o)
}

// The interaction live gate: a permission answered from Stage, a
// question form answered from Stage, and a message sent while a turn runs
// that OpenCode delivers only once the turn ends.
func TestLiveOpenCodeInteractive(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	release := make(chan struct{})
	releaseSteer := make(chan struct{})
	var mu sync.Mutex
	var calls []string // the last user text of each scripted model call, "tool" after a tool result
	var queuedBody string
	script := func(w http.ResponseWriter, r *http.Request, b []byte) bool {
		var c modelCall
		if json.Unmarshal(b, &c) != nil || len(c.Tools) == 0 || len(c.Messages) == 0 {
			return false
		}
		last := c.Messages[len(c.Messages)-1]
		user := ""
		for _, m := range c.Messages {
			if m.Role == "user" {
				user = messageText(m.Content)
			}
		}
		mu.Lock()
		if last.Role == "tool" {
			calls = append(calls, "tool")
		} else {
			calls = append(calls, user)
		}
		if strings.Contains(user, "fixture-queued") {
			queuedBody = string(b)
		}
		mu.Unlock()
		switch {
		case strings.Contains(user, "fixture-steered"):
			streamText(w, "steered reply")
		case last.Role == "tool":
			streamText(w, "tool done")
		case strings.Contains(user, "fixture-permission"):
			streamToolCall(w, "bash", map[string]any{"command": "echo stage", "description": "Print stage"})
		case strings.Contains(user, "fixture-question"):
			streamToolCall(w, "question", map[string]any{"questions": []map[string]any{{"question": "Which manager?", "header": "Manager",
				"options": []map[string]any{{"label": "pnpm", "description": "Fast"}, {"label": "npm", "description": "Default"}}}}})
		case strings.Contains(user, "fixture-slow"):
			select {
			case <-release:
			case <-r.Context().Done():
				return true
			}
			streamText(w, "slow reply")
		case strings.Contains(user, "fixture-steer-slow"):
			select {
			case <-releaseSteer:
			case <-r.Context().Done():
				return true
			}
			streamToolCall(w, "read", map[string]string{"path": path.Join(f.dir, "scratch-read.txt")})
		case strings.Contains(user, "fixture-queued"):
			streamText(w, "queued reply")
		case strings.Contains(user, "activity follow-up"):
			streamText(w, "activity response")
		case strings.Contains(user, "fixture-diff"):
			streamToolCall(w, "write", map[string]string{"path": path.Join(f.dir, "scratch.go"), "content": "package scratch\n"})
		default:
			return false
		}
		return true
	}
	f.script.Store(&script)

	features, err := f.a.Features(ctx)
	if err != nil || !features.Operations[execution.OpRespond].Available || !features.Operations[execution.OpSend].Available {
		t.Fatalf("interaction capability %+v: %v", features.Operations, err)
	}
	// launch retries once, as TestLiveOpenCode does: the first launch may
	// race OpenCode's own session update.
	launch := func(t *testing.T, prompt string) execution.SessionRef {
		in := f.intent(prompt, "root")
		var cp execution.Checkpoint
		save := func(_ context.Context, v execution.Checkpoint) error { cp = v; return nil }
		o, err := f.a.Launch(ctx, in, cp, save)
		if err != nil {
			o, err = f.a.Launch(ctx, in, cp, save)
		}
		if err != nil {
			t.Fatal(err)
		}
		return o.Session
	}

	t.Run("permission", func(t *testing.T) {
		ref := launch(t, "fixture-permission")
		req := pendingRequest(t, f.a, ref)
		if req.Kind != execution.RequestApproval || req.Options[0].ID != decisionOnce || req.TextOption != decisionReject || !strings.Contains(req.Preview, "echo stage") {
			t.Fatalf("permission request %+v", req)
		}
		// bash carries save patterns, so OpenCode offers Allow always.
		if len(req.Options) != 3 || req.Options[1].ID != decisionAlways {
			t.Fatalf("permission options %+v", req.Options)
		}
		observeUntil(t, f.a, ref, "needs_input")
		if err := f.a.Respond(ctx, ref, req.ID, execution.Response{Option: decisionOnce}); err != nil {
			t.Fatal(err)
		}
		replyUntil(t, f.a, ref, "tool done")
		if err := f.a.Respond(ctx, ref, req.ID, execution.Response{Option: decisionOnce}); !errors.Is(err, execution.ErrNotFound) {
			t.Fatalf("answered twice: %v", err)
		}
	})

	t.Run("form", func(t *testing.T) {
		ref := launch(t, "fixture-question")
		req := pendingRequest(t, f.a, ref)
		if req.Kind != execution.RequestQuestion || req.Title != "OpenCode asks" || len(req.Questions) != 1 || req.Questions[0].ID != "q0" {
			t.Fatalf("question request %+v", req)
		}
		if err := f.a.Respond(ctx, ref, req.ID, execution.Response{Answers: map[string][]string{"q0": {"pnpm"}}}); err != nil {
			t.Fatal(err)
		}
		replyUntil(t, f.a, ref, "tool done")
	})

	t.Run("queue while busy", func(t *testing.T) {
		ref := launch(t, "fixture-slow")
		observeUntil(t, f.a, ref, "running")
		var busy *execution.Busy
		if err := f.a.Send(ctx, ref, execution.Message{Text: "too early"}); !errors.As(err, &busy) || busy.Reason != busyRunning {
			t.Fatalf("send while running: %v", err)
		}
		msg := execution.Message{Text: "fixture-queued", RequestID: "steering-live-1", Delivery: execution.DeliveryQueue}
		for range 2 { // a retry with the same RequestID is admitted once
			if err := f.a.Send(ctx, ref, msg); err != nil {
				t.Fatal(err)
			}
		}
		time.Sleep(300 * time.Millisecond)
		mu.Lock()
		early := slices.ContainsFunc(calls, func(c string) bool { return strings.Contains(c, "fixture-queued") })
		mu.Unlock()
		if early {
			t.Fatal("queued message reached the model before the turn ended")
		}
		close(release)
		replyUntil(t, f.a, ref, "queued reply")
		mu.Lock()
		defer mu.Unlock()
		if !strings.Contains(queuedBody, "slow reply") || strings.Count(queuedBody, "fixture-queued") != 1 {
			t.Fatalf("queued turn did not follow the slow turn once: %s", queuedBody)
		}
	})

	t.Run("idle send and recent activity", func(t *testing.T) {
		ref := launch(t, "activity opening")
		observeUntil(t, f.a, ref, "done")
		if err := f.a.Send(ctx, ref, execution.Message{Text: "activity follow-up"}); err != nil {
			t.Fatal(err)
		}
		replyUntil(t, f.a, ref, "activity response")
		activity, err := f.a.Activity(ctx, ref, execution.ActivityQuery{})
		if err != nil || activity.Model != "fixture/test" || !slices.ContainsFunc(activity.Items, func(item execution.ActivityItem) bool {
			return item.Kind == "message_user" && item.Text == "activity follow-up"
		}) {
			t.Fatalf("recent activity %+v: %v", activity, err)
		}
	})

	t.Run("steer the current turn", func(t *testing.T) {
		resume := sync.OnceFunc(func() { close(releaseSteer) })
		defer resume()
		if err := os.WriteFile(path.Join(f.dir, "scratch-read.txt"), []byte("Steer fixture\n"), 0600); err != nil {
			t.Fatal(err)
		}
		ref := launch(t, "fixture-steer-slow")
		before := observeUntil(t, f.a, ref, "running")
		msg := execution.Message{Text: "fixture-steered", RequestID: "steer-live", Delivery: execution.DeliverySteer}
		if err := f.a.Send(ctx, ref, msg); err != nil {
			t.Fatal(err)
		}
		resume()
		replyUntil(t, f.a, ref, "steered reply")
		// A retry after the turn ended uses the delivered message as its receipt.
		if err := f.a.Send(ctx, ref, msg); err != nil {
			t.Fatal(err)
		}
		after := observeUntil(t, f.a, ref, "done")
		if after.ExecutionID != before.ExecutionID {
			t.Fatalf("steer started a different turn: before %s after %s", before.ExecutionID, after.ExecutionID)
		}
	})

	t.Run("native turn diff", func(t *testing.T) {
		ref := launch(t, "fixture-diff")
		replyUntil(t, f.a, ref, "tool done")
		activity, err := f.a.Activity(ctx, ref, execution.ActivityQuery{})
		if err != nil || !slices.Equal(activity.Files, []execution.FileChange{{Path: "scratch.go", Additions: 1, Status: "added"}}) {
			t.Fatalf("activity files %+v: %v", activity, err)
		}
	})
}
