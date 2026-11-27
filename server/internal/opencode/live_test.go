package opencode

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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
	f.config = fmt.Sprintf(`{"enabled_providers":["fixture"],"model":"fixture/test","provider":{"fixture":{"name":"Fixture","npm":"@ai-sdk/openai-compatible","options":{"baseURL":%q,"apiKey":"fixture"},"models":{"test":{"name":"Local test"}}}}}`, model.URL+"/v1")
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
