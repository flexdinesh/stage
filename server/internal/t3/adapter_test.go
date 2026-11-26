package t3

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/flexdinesh/stage/server/internal/execution"
	"strings"
	"testing"
	"time"
)

func TestAdapterNormalizesExecutionWithoutSettlementAuthority(t *testing.T) {
	for _, tc := range []struct {
		status ThreadStatus
		want   string
	}{
		{StatusRunning, "running"}, {StatusPreparing, "running"}, {StatusQueued, "running"}, {StatusStarting, "running"}, {StatusWaiting, "running"},
		{StatusCompleted, "done"}, {StatusFailed, "failed"}, {StatusInterrupted, "interrupted"}, {StatusCancelled, "cancelled"}, {StatusRolledBack, "cancelled"}, {StatusIdle, "idle"},
	} {
		d := ThreadDetail{Thread: Thread{ThreadID: "thread", ProjectID: "project", Status: tc.status}}
		o := d.Observation()
		if err := o.Valid(); err != nil || o.State != tc.want {
			t.Fatalf("%s -> %+v %v", tc.status, o, err)
		}
		d.Settled = true
		d.ActiveRunID = "active"
		d.PendingRequestCount = 1
		o = d.Observation()
		if o.State != "needs_input" || !o.Active {
			t.Fatal("settlement hid activity", o)
		}
	}
	malformed := (ThreadDetail{Thread: Thread{ThreadID: "thread", ProjectID: "project", Status: "surprise"}}).Observation()
	if malformed.Valid() == nil {
		t.Fatal("unknown status accepted")
	}
}
func TestUnstartedCancelledRunDoesNotDecideState(t *testing.T) {
	at := func(m int) *time.Time { v := time.Date(2026, 11, 22, 5, m, 0, 0, time.UTC); return &v }
	unstarted := Run{RunID: "r3", Status: StatusCancelled, RequestedAt: *at(49), CompletedAt: at(51)}
	for _, tc := range []struct {
		name   string
		status ThreadStatus
		runs   []Run
		want   string
		ended  *time.Time
	}{
		{"completed then unstarted cancel", StatusCancelled, []Run{unstarted, {RunID: "r2", Status: StatusCompleted, StartedAt: at(30), CompletedAt: at(50)}}, "done", at(50)},
		{"unstarted cancels skipped", StatusCancelled, []Run{unstarted, unstarted, {RunID: "r2", Status: StatusCompleted, StartedAt: at(30), CompletedAt: at(50)}}, "done", at(50)},
		{"idle thread", StatusIdle, []Run{unstarted, {RunID: "r2", Status: StatusCompleted, StartedAt: at(30), CompletedAt: at(50)}}, "done", at(50)},
		{"unstarted rollback", StatusRolledBack, []Run{{RunID: "r3", Status: StatusRolledBack, CompletedAt: at(51)}, {RunID: "r2", Status: StatusCompleted, StartedAt: at(30), CompletedAt: at(50)}}, "done", at(50)},
		{"failed then unstarted cancel", StatusCancelled, []Run{unstarted, {RunID: "r2", Status: StatusFailed, StartedAt: at(30), CompletedAt: at(50)}}, "failed", at(50)},
		{"started cancel", StatusCancelled, []Run{{RunID: "r3", Status: StatusCancelled, StartedAt: at(49), CompletedAt: at(51)}, {RunID: "r2", Status: StatusCompleted, StartedAt: at(30), CompletedAt: at(50)}}, "cancelled", at(51)},
		{"only unstarted cancel", StatusCancelled, []Run{unstarted}, "cancelled", at(51)},
	} {
		d := ThreadDetail{Thread: Thread{ThreadID: "thread", ProjectID: "project", Status: tc.status, Settled: true}, RunCount: len(tc.runs), RecentRuns: tc.runs}
		o := d.Observation()
		if err := o.Valid(); err != nil || o.State != tc.want || o.EndedAt == nil || !o.EndedAt.Equal(*tc.ended) {
			t.Fatalf("%s: state %q ended %v err %v; want %q ended %v", tc.name, o.State, o.EndedAt, err, tc.want, tc.ended)
		}
	}
}
func TestSettledThreadReportsClosure(t *testing.T) {
	d := ThreadDetail{Thread: Thread{ThreadID: "thread", ProjectID: "project", Status: StatusCompleted}}
	if o := d.Observation(); o.Closed != nil {
		t.Fatalf("unsettled closed: %+v", o.Closed)
	}
	d.Settled = true
	if o := d.Observation(); o.Closed == nil || o.Closed.Signal != execution.ClosedSettled || o.State != "done" {
		t.Fatalf("settled: %+v", o)
	}
	d.Status = StatusFailed
	if o := d.Observation(); o.Closed == nil || o.State != "failed" {
		t.Fatalf("settled failure: %+v", o)
	}
}
func TestLaunchIdentityUsesAuthenticationSession(t *testing.T) {
	token := func(sid string) string {
		raw, _ := json.Marshal(map[string]string{"kind": "session", "sid": sid})
		return base64.RawURLEncoding.EncodeToString(raw) + ".signature"
	}
	scope, expected := tokenScope(token("one"), "request")
	again, other := tokenScope(token("two"), "request")
	if scope != "client:one" || again != "client:two" || expected == other || expected == "" {
		t.Fatal(scope, again, expected, other)
	}
}

type rotatingClient struct {
	*Fake
	scope, environment, expected string
	calls                        int
	lose                         bool
}

func (c *rotatingClient) LaunchScope(context.Context, string) (string, string, string, error) {
	return c.scope, c.environment, c.expected, nil
}
func (c *rotatingClient) LaunchPinned(ctx context.Context, in LaunchInput, scope, env string) (LaunchResult, error) {
	c.calls++
	if c.lose {
		return LaunchResult{}, errors.New("timeout")
	}
	return c.Fake.LaunchThread(ctx, in)
}
func TestAdapterNeverReplaysAcrossPairingOrEnvironment(t *testing.T) {
	c := &rotatingClient{Fake: NewFake(), scope: "client:first", environment: "env", lose: true}
	a := NewAdapter(c)
	in := execution.Intent{RequestID: "request", ConnectionID: 1, Project: "project", Title: "Task", Config: execution.Config{Mode: "default", Permission: "full-access"}}
	var cp execution.Checkpoint
	save := func(_ context.Context, next execution.Checkpoint) error { cp = next; return nil }
	if _, err := a.Launch(t.Context(), in, cp, save); err == nil {
		t.Fatal("lost acknowledgement succeeded")
	}
	if c.calls != 1 || cp.Version != 1 {
		t.Fatal("launch not checkpointed")
	}
	c.scope = "client:second"
	c.lose = false
	if _, err := a.Launch(t.Context(), in, cp, save); err == nil || c.calls != 1 {
		t.Fatal("re-pair replayed launch", err, c.calls)
	}
	c.scope = "client:first"
	c.environment = "other"
	if _, err := a.Launch(t.Context(), in, cp, save); err == nil || c.calls != 1 {
		t.Fatal("environment replayed launch", err, c.calls)
	}
	if _, err := a.Launch(t.Context(), in, execution.Checkpoint{Version: 1, Data: json.RawMessage("{}")}, save); err == nil || c.calls != 1 {
		t.Fatal("incomplete checkpoint replayed launch")
	}
}
func TestAdapterCheckpointFailurePreventsDispatch(t *testing.T) {
	c := &rotatingClient{Fake: NewFake(), scope: "client:first", environment: "env"}
	a := NewAdapter(c)
	_, err := a.Launch(t.Context(), execution.Intent{RequestID: "request", Config: execution.Config{Mode: "default", Permission: "full-access"}}, execution.Checkpoint{}, func(context.Context, execution.Checkpoint) error { return errors.New("disk full") })
	if err == nil || c.calls != 0 {
		t.Fatal("dispatched before durable identity", err)
	}
}

func TestAdapterLinkKeepsOpaqueSessionIdentity(t *testing.T) {
	adapter := NewAdapter(NewFake())
	id := "provider:session/with%id"
	ref, err := adapter.ResolveLink(t.Context(), AppThreadURL(FakeEnvironmentID, id))
	if err != nil || ref.ExternalSessionID != id || ref.ConnectionID != 1 {
		t.Fatal(ref, err)
	}
	if _, err = adapter.ResolveLink(t.Context(), "t3code://threads/other/session"); err == nil {
		t.Fatal("foreign environment accepted")
	}
}

// T3 web links resolve through the connection's adapter, scoped to it and
// to its public URL.
func TestAdapterResolvesWebLinks(t *testing.T) {
	fake := NewFake()
	fake.PublicURL = "https://t3.example/base"
	adapter := NewConnectionAdapter(fake, 7)
	ref, err := adapter.ResolveLink(t.Context(), WebThreadURL(fake.PublicURL, FakeEnvironmentID, "thread-x"))
	if err != nil || ref != (execution.SessionRef{ConnectionID: 7, ExternalSessionID: "thread-x"}) {
		t.Fatal(ref, err)
	}
	if ref, err = adapter.ResolveLink(t.Context(), AppThreadURL(FakeEnvironmentID, "thread-y")); err != nil || ref.ConnectionID != 7 {
		t.Fatal(ref, err)
	}
	for _, raw := range []string{"https://other.example/base/" + FakeEnvironmentID + "/thread-x", "https://t3.example/elsewhere/thread-x"} {
		if _, err := adapter.ResolveLink(t.Context(), raw); err == nil {
			t.Fatalf("%s accepted", raw)
		}
	}
}

func TestUnreportedRuntimeModesInheritHarnessPermissions(t *testing.T) {
	fake := NewFake()
	fake.Catalog.Providers[0].RuntimeModes = nil
	adapter := NewAdapter(fake)
	caps, err := adapter.Features(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := caps.DeclaresOperations(); err != nil {
		t.Fatal(err)
	}
	m := caps.Models[0]
	if m.Permissions != nil {
		t.Fatalf("unreported runtime modes reported as %+v", m.Permissions)
	}
	if got := caps.PermissionsFor(m); len(got) != 4 || got[3].ID != "full-access" {
		t.Fatalf("inherited permissions = %+v", got)
	}
	intent := func(permission string) execution.Intent {
		return execution.Intent{Config: execution.Config{Model: m.ID, Permission: permission}}
	}
	if err := adapter.validate(t.Context(), intent("full-access")); err != nil {
		t.Fatal("harness permission rejected:", err)
	}
	if err := caps.ValidateConfig(intent("full-access").Config); err != nil {
		t.Fatal("ValidateConfig rejected harness permission:", err)
	}
	if adapter.validate(t.Context(), intent("bogus")) == nil || caps.ValidateConfig(intent("bogus").Config) == nil {
		t.Fatal("unknown permission accepted")
	}
	fake.Catalog.Providers[0].RuntimeModes = []RuntimeMode{RuntimeAuto}
	caps, err = adapter.Features(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := caps.PermissionsFor(caps.Models[0]); len(got) != 1 || got[0].ID != "auto" {
		t.Fatalf("reported runtime modes = %+v", got)
	}
	if adapter.validate(t.Context(), intent("full-access")) == nil {
		t.Fatal("permission outside reported modes accepted")
	}
}

// The launch options become T3's workspaceStrategy; startFromOrigin follows
// a remote base ref.
func TestLaunchOptionsTranslateToWorkspaceStrategy(t *testing.T) {
	on := execution.ModelOption{ID: OptionWorktree, Value: execution.BoolValue(true)}
	text := func(id, v string) execution.ModelOption {
		return execution.ModelOption{ID: id, Value: execution.StringValue(v)}
	}
	for _, tc := range []struct {
		name   string
		opts   []execution.ModelOption
		want   *WorkspaceStrategy
		reject string
	}{
		{"project checkout by default", nil, &WorkspaceStrategy{Type: WorkspaceRoot}, ""},
		{"worktree off", []execution.ModelOption{{ID: OptionWorktree, Value: execution.BoolValue(false)}}, &WorkspaceStrategy{Type: WorkspaceRoot}, ""},
		{"origin ref", []execution.ModelOption{on, text(OptionBaseRef, "origin/main"), text(OptionBranch, "stage/x")}, &WorkspaceStrategy{Type: WorkspaceWorktree, BaseRef: "origin/main", Branch: "stage/x"}, ""},
		{"upstream ref", []execution.ModelOption{on, text(OptionBaseRef, "upstream/dev")}, &WorkspaceStrategy{Type: WorkspaceWorktree, BaseRef: "upstream/dev"}, ""},
		{"local ref", []execution.ModelOption{on, text(OptionBaseRef, "main")}, &WorkspaceStrategy{Type: WorkspaceWorktree, BaseRef: "main"}, ""},
		{"worktree needs a base ref", []execution.ModelOption{on}, nil, "Set Base ref"},
		{"base ref needs a worktree", []execution.ModelOption{text(OptionBaseRef, "main")}, nil, "applies only when worktree is true"},
		{"ref is one word", []execution.ModelOption{on, text(OptionBaseRef, "--orphan")}, nil, "not a Git ref name"},
		{"worktree takes a boolean", []execution.ModelOption{{ID: OptionWorktree, Value: execution.StringValue("yes")}}, nil, "takes true or false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &rotatingClient{Fake: NewFake(), scope: "client:first", environment: "env"}
			a := NewAdapter(c)
			in := execution.Intent{RequestID: "request", ConnectionID: 1, Project: "project", Title: "Task", Config: execution.Config{Mode: "default", Permission: "full-access", Options: tc.opts}}
			_, err := a.Launch(t.Context(), in, execution.Checkpoint{}, func(context.Context, execution.Checkpoint) error { return nil })
			if tc.reject != "" {
				var rejected *execution.Rejected
				if !errors.As(err, &rejected) || !strings.Contains(err.Error(), tc.reject) || c.calls != 0 {
					t.Fatalf("want rejection %q, got %v (calls %d)", tc.reject, err, c.calls)
				}
				return
			}
			if err != nil || len(c.Launched) != 1 {
				t.Fatal(err, c.Launched)
			}
			got := c.Launched[0]
			if got.ModelSelection != nil && len(got.ModelSelection.Options) != 0 {
				t.Fatalf("launch options leaked into the model selection: %+v", got.ModelSelection)
			}
			w := got.Workspace
			if w == nil || w.Type != tc.want.Type || w.BaseRef != tc.want.BaseRef || w.Branch != tc.want.Branch {
				t.Fatalf("workspace %+v, want %+v", w, tc.want)
			}
			remote := strings.HasPrefix(w.BaseRef, "origin/") || strings.HasPrefix(w.BaseRef, "upstream/")
			if w.Type == WorkspaceWorktree && (w.StartFromOrigin == nil || *w.StartFromOrigin != remote) || w.Type == WorkspaceRoot && w.StartFromOrigin != nil {
				t.Fatalf("startFromOrigin %+v", w)
			}
		})
	}
}
