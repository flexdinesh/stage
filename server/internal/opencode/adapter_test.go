package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func TestLaunchOptionSelection(t *testing.T) {
	c := execution.Capabilities{Options: launchOptions(), Modes: []execution.Choice{choice("planner", "Plan")}, Models: []execution.Model{{Choice: choice("provider/family/model", "Model"), Options: []execution.Option{{ID: "variant", Values: []execution.OptionValue{execution.StringValue("fast")}}}}}}
	worktree := execution.ModelOption{ID: optionWorktree, Value: execution.BoolValue(true)}
	for _, tc := range []struct {
		name   string
		config execution.Config
		ok     bool
	}{
		{"harness defaults", execution.Config{}, true},
		{"worktree", execution.Config{Options: []execution.ModelOption{worktree}}, true},
		{"agent and variant in a worktree", execution.Config{Mode: "planner", Model: "provider/family/model", Options: []execution.ModelOption{worktree, {ID: "variant", Value: execution.StringValue("fast")}}}, true},
		{"worktree takes a boolean", execution.Config{Options: []execution.ModelOption{{ID: optionWorktree, Value: execution.StringValue("yes")}}}, false},
		{"base ref", execution.Config{Model: "provider/family/model", Options: []execution.ModelOption{worktree, {ID: "baseRef", Value: execution.StringValue("HEAD")}}}, false},
		{"branch", execution.Config{Model: "provider/family/model", Options: []execution.ModelOption{worktree, {ID: "branch", Value: execution.StringValue("new")}}}, false},
		{"permissions", execution.Config{Permission: "full-access"}, false},
		{"unavailable agent", execution.Config{Mode: "removed"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, e := selection(c, execution.Intent{Config: tc.config})
			var rejected *execution.Rejected
			if tc.ok && e != nil || !tc.ok && !errors.As(e, &rejected) {
				t.Fatalf("selection: %+v %v", v, e)
			}
			if tc.ok && tc.config.Model != "" && (v == nil || v.ID != "family/model" || v.Variant != "fast") {
				t.Fatal("model changed", v)
			}
		})
	}
	if err := c.Valid(); err != nil {
		t.Fatal(err)
	}
}

func TestSeparateWebClientResolvesTheSameSessionOnBothOrigins(t *testing.T) {
	cfg := Settings{Endpoint: "http://stage-internal:7400", BrowserEndpoint: "https://service.example/oc", WebClient: "https://client.example/ui"}
	a, err := New(2, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ref := execution.SessionRef{ConnectionID: 2, ExternalSessionID: "ses_origin"}
	links := a.Destinations(t.Context(), ref)
	path, ok := strings.CutPrefix(links.Web, cfg.WebClient)
	if !ok || !strings.HasPrefix(path, "/server/") {
		t.Fatal(links)
	}
	serviceURL := cfg.BrowserEndpoint + path
	for _, input := range []string{ref.ExternalSessionID, links.Web, serviceURL} {
		got, err := a.ResolveLink(t.Context(), input)
		if err != nil || got != ref {
			t.Errorf("%q: %+v, %v", input, got, err)
		}
	}
	for _, input := range []string{strings.Replace(links.Web, "client.example", "foreign.example", 1), links.Web + "?token=x", strings.Replace(serviceURL, "/session/", "/project/", 1)} {
		if _, err := a.ResolveLink(t.Context(), input); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
	cfg.WebClient = ""
	a, err = New(2, cfg)
	if err != nil {
		t.Fatal(err)
	}
	direct := a.Destinations(t.Context(), ref)
	if direct.Web != serviceURL {
		t.Fatal(direct)
	}
}
func TestObservationEvidence(t *testing.T) {
	base := wireTime{Created: 10, Updated: 40, Busy: 30, Idle: 40}
	for _, tc := range []struct {
		name, state, outcome    string
		stamp                   wireTime
		active, stale, mismatch bool
	}{
		{"succeeded", "done", "succeeded", base, false, false, false},
		{"missing outcome", "", "", base, false, false, false},
		{"old terminal", "running", "succeeded", wireTime{Created: 10, Updated: 60, Busy: 60, Idle: 40}, true, false, false},
		{"absent active owner", "", "succeeded", wireTime{Created: 10, Updated: 60, Busy: 60, Idle: 40}, false, false, false},
		{"snapshot changed", "", "succeeded", base, false, true, false},
		{"session mismatch", "", "succeeded", base, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/session/ses_test":
					reads++
					id := "ses_test"
					if tc.mismatch {
						id = "ses_other"
					}
					stamp := tc.stamp
					if tc.stale && reads > 1 {
						stamp.Busy = 90
						stamp.Updated = 90
					}
					_ = json.NewEncoder(w).Encode(envelope[session]{Data: session{ID: id, Time: stamp, Outcome: tc.outcome, ProjectID: "project", Location: location{Directory: "/repo"}}})
				case "/api/session/active":
					if tc.active {
						fmt.Fprint(w, `{"data":{"ses_test":{"type":"running"}}}`)
					} else {
						fmt.Fprint(w, `{"data":{}}`)
					}
				case "/api/session/ses_test/permission", "/api/session/ses_test/form":
					fmt.Fprint(w, `{"data":[]}`)
				case "/api/session/ses_test/message":
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": []interface{}{map[string]interface{}{"id": "msg_done", "type": "assistant", "time": map[string]int{"created": 32, "completed": 38}, "content": []map[string]string{{"type": "reasoning", "text": "private"}, {"type": "text", "text": strings.Repeat("界", 4001)}}}}})
				default:
					t.Errorf("unexpected request %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			a, e := New(2, Settings{Endpoint: srv.URL})
			if e != nil {
				t.Fatal(e)
			}
			o, e := a.Observe(t.Context(), execution.SessionRef{ConnectionID: 2, ExternalSessionID: "ses_test"})
			if tc.state == "" {
				if e == nil {
					t.Fatal("accepted inconclusive observation", o)
				}
				return
			}
			if e != nil || o.State != tc.state {
				t.Fatal(o, e)
			}
			if tc.state == "done" && (o.Reply == nil || !o.Reply.Truncated || o.Reply.Text != strings.Repeat("界", 4000)) {
				t.Fatal("invalid bounded final evidence")
			}
		})
	}
}

func TestArchivedSessionReportsClosure(t *testing.T) {
	for _, archived := range []int64{0, 50} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/session/ses_test":
				_ = json.NewEncoder(w).Encode(envelope[session]{Data: session{ID: "ses_test", Time: wireTime{Created: 10, Updated: 50, Busy: 30, Idle: 40, Archived: archived}, Outcome: "succeeded", ProjectID: "project", Location: location{Directory: "/repo"}}})
			case "/api/session/active":
				fmt.Fprint(w, `{"data":{}}`)
			case "/api/session/ses_test/permission", "/api/session/ses_test/form", "/api/session/ses_test/message":
				fmt.Fprint(w, `{"data":[]}`)
			default:
				http.NotFound(w, r)
			}
		}))
		a, e := New(2, Settings{Endpoint: srv.URL})
		if e != nil {
			t.Fatal(e)
		}
		o, e := a.Observe(t.Context(), execution.SessionRef{ConnectionID: 2, ExternalSessionID: "ses_test"})
		srv.Close()
		if e != nil || o.State != "done" {
			t.Fatal(o, e)
		}
		if archived == 0 && o.Closed != nil || archived > 0 && (o.Closed == nil || o.Closed.Signal != execution.ClosedArchived || !o.Archived) {
			t.Fatalf("archived %d: closed %+v", archived, o.Closed)
		}
	}
}

func TestLiveCheckpointWrites(t *testing.T) {
	f := newFixture(t)
	a := f.a
	for _, phase := range []string{"worktree-requested", "workspace-created", "session-requested", "prompt-requested", "admitted"} {
		t.Run(phase, func(t *testing.T) {
			posts := map[string]int{}
			a.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method == "POST" {
					posts[r.URL.Path]++
				}
				return http.DefaultTransport.RoundTrip(r)
			})
			defer func() { a.http.Transport = nil }()
			in := f.intent(phase, "worktree")
			var saved execution.Checkpoint
			var latest launchRecord
			fail := errors.New("checkpoint storage unavailable")
			save := func(_ context.Context, cp execution.Checkpoint) error {
				if e := json.Unmarshal(cp.Data, &latest); e != nil {
					return e
				}
				if latest.Phase == phase {
					return fail
				}
				saved = cp
				return nil
			}
			if _, e := a.Launch(t.Context(), in, saved, save); !errors.Is(e, fail) {
				t.Fatalf("unexpected failure: %v", e)
			}
			tree, session, prompt := posts["/api/worktree"], posts["/api/session"], posts["/api/session/"+latest.Session+"/prompt"]
			expected := map[string][3]int{"worktree-requested": {0, 0, 0}, "workspace-created": {1, 0, 0}, "session-requested": {1, 0, 0}, "prompt-requested": {1, 1, 0}, "admitted": {1, 1, 1}}
			if got := [3]int{tree, session, prompt}; got != expected[phase] {
				t.Fatal("effect occurred after failed save", got)
			}
		})
	}
}

func TestLiveWorktreeSetupFailure(t *testing.T) {
	f := newFixture(t)
	a, ctx := f.a, t.Context()
	// The native service registers the worktree, then runs project setup.
	if e := a.request(ctx, "PATCH", "/api/project/"+f.project, map[string]any{"commands": map[string]string{"start": "exit 1"}}, nil); e != nil {
		t.Fatal(e)
	}
	in := f.intent("failed setup", "worktree")
	var cp execution.Checkpoint
	save := func(_ context.Context, next execution.Checkpoint) error { cp = next; return nil }
	before, e := a.worktrees(ctx, f.project)
	if e != nil {
		t.Fatal(e)
	}
	_, e = a.Launch(ctx, in, cp, save)
	var rejected *execution.Rejected
	if e == nil || errors.As(e, &rejected) {
		t.Fatalf("partially created worktree treated as safe to repeat: %v", e)
	}
	for range 2 {
		if _, e = a.Launch(ctx, in, cp, save); e == nil || !strings.Contains(e.Error(), "unconfirmed") {
			t.Fatal("setup failure was forgotten", e)
		}
	}
	after, e := a.worktrees(ctx, f.project)
	if e != nil || len(after) != len(before)+1 || f.turns.Load() != 0 {
		t.Fatalf("partial setup: trees %d to %d, turns %d, err %v", len(before), len(after), f.turns.Load(), e)
	}
}

func TestStoredExecutionUsesChronologicalTime(t *testing.T) {
	var temp dbtest.DB
	pool := temp.Reset(t)
	store := db.NewStore(pool)
	ctx := t.Context()
	if _, err := pool.ExecContext(ctx, "INSERT INTO harness_connections(id,harness) VALUES(2,'opencode')"); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(1700000000, 0).UTC()
	old := execution.Observation{Session: execution.SessionRef{ConnectionID: 2, ExternalSessionID: "ses_order"}, Project: "p", State: "done", UpdatedAt: stamp, ExecutionID: "one", Reply: &execution.Reply{Text: "old reply"}}
	next := old
	next.State = "running"
	next.Active = true
	next.UpdatedAt = stamp.Add(time.Millisecond)
	next.ExecutionID = "two"
	next.Reply = nil
	for _, sample := range []execution.Observation{old, next, old} {
		if err := store.PutObservation(ctx, sample); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.Observation(ctx, old.Session)
	if err != nil || got.State != "running" || got.ExecutionID != "two" || got.Reply != nil {
		t.Fatalf("stale observation or reply: %+v %v", got, err)
	}
}

func TestLiveSessionCollision(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	in := f.intent("reserved identity", "root")
	var cp execution.Checkpoint
	save := func(_ context.Context, next execution.Checkpoint) error {
		cp = next
		var r launchRecord
		if e := json.Unmarshal(next.Data, &r); e != nil {
			return e
		}
		if r.Phase == "session-requested" {
			return f.a.request(ctx, "POST", "/api/session", map[string]any{"id": r.Session, "title": "other owner's session", "location": location{Directory: f.dir}}, nil)
		}
		return nil
	}
	for range 2 {
		if _, e := f.a.Launch(ctx, in, cp, save); e == nil {
			t.Fatal("accepted colliding session without its attempt marker")
		}
	}
	if f.turns.Load() != 0 {
		t.Fatal("dispatched to the wrong session")
	}
}
