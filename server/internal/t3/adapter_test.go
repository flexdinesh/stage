package t3

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/flexdinesh/stage/server/internal/execution"
	"testing"
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
	in := execution.Intent{RequestID: "request", ConnectionID: 1, Project: "project", Title: "Task", Config: execution.Config{Mode: "default", Permission: "full-access"}, Workspace: execution.Workspace{Kind: "root"}}
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
	_, err := a.Launch(t.Context(), execution.Intent{RequestID: "request", Config: execution.Config{Mode: "default", Permission: "full-access"}, Workspace: execution.Workspace{Kind: "root"}}, execution.Checkpoint{}, func(context.Context, execution.Checkpoint) error { return errors.New("disk full") })
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

func TestUnreportedRuntimeModesInheritHarnessPermissions(t *testing.T) {
	fake := NewFake()
	fake.Catalog.Providers[0].RuntimeModes = nil
	adapter := NewAdapter(fake)
	caps, err := adapter.Features(t.Context())
	if err != nil {
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
		return execution.Intent{Config: execution.Config{Model: m.ID, Permission: permission}, Workspace: execution.Workspace{Kind: "root"}}
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
