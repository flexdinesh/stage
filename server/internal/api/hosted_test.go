package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/opencode"
	"github.com/flexdinesh/stage/server/internal/remote"
	t3sync "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// hostedKit is a hosted harness with connection setup: a connect action
// and disconnect.
var hostedKit = harness.Descriptor{
	Kind: "hosted-kit", Name: "Hosted kit", Sessions: harness.SessionsMachine, LocalProcesses: true,
	Setup: harness.Setup{
		Actions: []harness.Action{{
			ID: "connect", Label: "Connect", Target: execution.TargetConnection,
			Callers: []execution.Caller{execution.CallerUser}, Availability: execution.Availability{Available: true},
		}},
		Disconnect: true,
	},
}

var hostedHarnesses = harness.MustRegistry(t3.Descriptor, opencode.Descriptor, hostedKit)

// hostedWorkspace is the one workspace the fake hosted harness serves.
const hostedWorkspace = "/home/u/repo"

// hostedAdapter is the fake hosted harness's adapter: one workspace, and a
// launch that starts a running session.
type hostedAdapter struct{}

func (hostedAdapter) Health(context.Context) execution.Health {
	return execution.Health{Availability: execution.Availability{Available: true}, Identity: "kit-home"}
}
func (hostedAdapter) Features(context.Context) (execution.Capabilities, error) {
	return execution.Capabilities{
		Operations: map[execution.Op]execution.Availability{execution.OpLaunch: {Available: true}},
		Models:     []execution.Model{{Choice: execution.Choice{ID: "m", Availability: execution.Availability{Available: true}}}},
	}, nil
}
func (hostedAdapter) Workspaces(context.Context) ([]execution.WorkspaceInfo, error) {
	return []execution.WorkspaceInfo{{ID: "repo", Title: "repo", WorkspaceRoot: hostedWorkspace}}, nil
}
func (hostedAdapter) observation(ref execution.SessionRef) execution.Observation {
	now := time.Now().UTC()
	return execution.Observation{Session: ref, Project: "repo", Title: "Kit", State: "running", Active: true, CreatedAt: now, UpdatedAt: now, Links: execution.Links{Command: "kit attach"}}
}
func (a hostedAdapter) Observe(_ context.Context, ref execution.SessionRef) (execution.Observation, error) {
	return a.observation(ref), nil
}
func (a hostedAdapter) Launch(ctx context.Context, in execution.Intent, _ execution.Checkpoint, save execution.SaveCheckpoint) (execution.Observation, error) {
	// Reserve the session before the (fake) process starts.
	id := "kit-" + in.RequestID
	cp, err := json.Marshal(map[string]string{"session": id})
	if err != nil {
		return execution.Observation{}, err
	}
	if err := save(ctx, execution.Checkpoint{Version: 1, Data: cp}); err != nil {
		return execution.Observation{}, err
	}
	return a.observation(execution.SessionRef{ConnectionID: in.ConnectionID, ExternalSessionID: id}), nil
}
func (hostedAdapter) Stop(context.Context, execution.SessionRef) error { return nil }
func (hostedAdapter) Attention(context.Context) ([]execution.SessionRef, error) {
	return nil, nil
}
func (hostedAdapter) Opening(context.Context, execution.SessionRef, bool) (*execution.Reply, error) {
	return nil, execution.ErrUnavailable
}

// hostedConnector is the fake hosted harness's setup: connect records the
// machine's connection through the identity port and installs the adapter
// (machine.Connect). calls counts every method the API reached.
type hostedConnector struct {
	identity machine.Identity
	install  machine.Install
	calls    atomic.Int64
}

func (c *hostedConnector) active(ctx context.Context) int64 {
	id, _ := c.identity.Active(ctx, hostedKit.Kind)
	return id
}
func (c *hostedConnector) Summary(ctx context.Context) harness.Summary {
	id := c.active(ctx)
	return harness.Summary{ConnectionID: id, Configured: id != 0, Connected: id != 0}
}
func (c *hostedConnector) Status(ctx context.Context, _ bool) (harness.Status, error) {
	c.calls.Add(1)
	s := c.Summary(ctx)
	state := "disconnected"
	if s.Connected {
		state = "connected"
	}
	return harness.Status{Summary: s, State: state, Facts: []execution.Fact{{ID: "folder", Label: "Config folder", Value: "/home/u/.kit", Link: "javascript:invalid", Copyable: true}}, Actions: []harness.ActionState{{ID: "connect", Available: true}}}, nil
}
func (c *hostedConnector) Configure(context.Context, json.RawMessage) (harness.Result, error) {
	c.calls.Add(1)
	return harness.Result{}, nil
}
func (c *hostedConnector) Run(ctx context.Context, action string, _ json.RawMessage) (harness.Result, error) {
	c.calls.Add(1)
	if action != "connect" {
		return harness.Result{}, harness.UnknownAction(hostedKit, action)
	}
	return machine.Connect(ctx, c.identity, c.install, hostedKit.Kind, "/home/u/.kit", "u@kit", func(int64) execution.Adapter { return hostedAdapter{} })
}
func (c *hostedConnector) Disconnect(ctx context.Context) error {
	c.calls.Add(1)
	id := c.active(ctx)
	c.install(id, id, nil)
	return nil
}

// hostedEnv hosts the local machine with profile, and on it the fake hosted
// harness's connector; the coordinator and connectors follow the machine.
func hostedEnv(t *testing.T, profile harness.Profile) (*env, *hostedConnector) {
	t.Helper()
	var conn *hostedConnector
	e := newEnv(t, func(d *api.Deps) {
		local := machine.NewLocal(db.LocalMachine, profile, nil)
		set := machine.MustSet(local)
		svc := t3sync.NewForMachines(d.Store, hostedHarnesses, set, nil)
		local.OnChange(svc.ConnectionsChanged)
		conn = &hostedConnector{identity: machine.NewStorePorts(d.Store.OnMachine(db.LocalMachine), hostedHarnesses), install: local.Install}
		local.SetConnector(hostedKit.Kind, conn)
		d.Harnesses, d.Sync = hostedHarnesses, svc
		d.LiveConnectors = func() harness.Connectors {
			c, err := set.Connectors(hostedHarnesses)
			if err != nil {
				t.Error(err)
			}
			return c
		}
	})
	return e, conn
}

type hostedListing struct {
	Harnesses []struct {
		Kind        string `json:"kind"`
		Available   bool   `json:"available"`
		Reason      string `json:"reason"`
		Connectable bool   `json:"connectable"`
	} `json:"harnesses"`
}

func (l hostedListing) kit(t *testing.T) (available bool, reason string, connectable bool) {
	t.Helper()
	for _, h := range l.Harnesses {
		if h.Kind == hostedKit.Kind {
			return h.Available, h.Reason, h.Connectable
		}
	}
	t.Fatalf("%s not listed: %+v", hostedKit.Kind, l)
	return false, "", false
}

// A hosted harness works end to end on a machine that allows it: listed
// connectable, its connect action creates the connection row, and a task on
// that connection launches and is observed running.
func TestHostedHarnessEndToEnd(t *testing.T) {
	e, connector := hostedEnv(t, harness.Profile{Kind: harness.ProfileDesktop, LocalProcesses: true})
	ctx := t.Context()
	var list hostedListing
	e.must("GET", "/api/harnesses", nil, 200, &list)
	if available, reason, connectable := list.kit(t); !available || reason != "" || !connectable {
		t.Fatalf("listing %v %q %v", available, reason, connectable)
	}
	var st struct {
		ConnectionID int64  `json:"connectionId"`
		State        string `json:"state"`
		Connected    bool   `json:"connected"`
	}
	e.must("GET", "/api/harnesses/hosted-kit/status", nil, 200, &st)
	if st.ConnectionID != 0 || st.State != "disconnected" {
		t.Fatalf("before connect %+v", st)
	}
	if id := e.store.ActiveID(db.LocalMachine, hostedKit.Kind); id != 0 {
		t.Fatalf("connection row before connect: %d", id)
	}

	e.must("POST", "/api/harnesses/hosted-kit/actions/connect", map[string]any{}, 200, &st)
	conn := e.store.ActiveID(db.LocalMachine, hostedKit.Kind)
	if conn == 0 || st.ConnectionID != conn || !st.Connected {
		t.Fatalf("after connect %+v, active %d", st, conn)
	}
	probes := connector.calls.Load()
	var snapshot struct {
		Facts []execution.Fact `json:"facts"`
	}
	e.must("GET", fmt.Sprintf("/api/connections/%d/facts", conn), nil, 200, &snapshot)
	want := []execution.Fact{{ID: "folder", Label: "Config folder", Value: "/home/u/.kit", Copyable: true}}
	if !slices.Equal(snapshot.Facts, want) || connector.calls.Load() != probes {
		t.Fatalf("saved display facts %v, connector calls %d (was %d)", snapshot.Facts, connector.calls.Load(), probes)
	}

	var conns struct {
		Connections []struct {
			ID      int64  `json:"id"`
			Harness string `json:"harness"`
			Label   string `json:"label"`
			Active  bool   `json:"active"`
		} `json:"connections"`
	}
	e.must("GET", "/api/connections", nil, 200, &conns)
	found := false
	for _, c := range conns.Connections {
		found = found || (c.ID == conn && c.Harness == hostedKit.Kind && c.Label == "u@kit" && c.Active)
	}
	if !found {
		t.Fatalf("connections %+v", conns)
	}

	project, err := e.store.InsertProject(ctx, db.Project{Title: "Kit", Bindings: []db.ProjectBinding{{ConnectionID: conn, ExternalProjectID: "repo", Workspace: hostedWorkspace}}})
	if err != nil {
		t.Fatal(err)
	}
	task, err := e.store.CreateTask(ctx, db.NewTask{ProjectID: project.ID, ConnectionID: conn, Title: "Work", Prompt: "Do it", ExecutionConfig: &execution.Config{Model: "m"}})
	if err != nil {
		t.Fatal(err)
	}
	var started struct {
		State string `json:"state"`
	}
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", task.ID), nil, 200, &started)
	if started.State != string(db.StateRunning) {
		t.Fatalf("started %+v", started)
	}
	got, err := e.store.GetTask(ctx, task.ID)
	if err != nil || got.State != db.StateRunning || got.Session == nil || got.Session.ConnectionID != conn || got.Session.ExternalSessionID != "kit-"+mustRequestID(t, e, task.ID) {
		t.Fatalf("task %+v %v", got, err)
	}
	var stop struct {
		Error string `json:"error"`
	}
	e.must("POST", fmt.Sprintf("/api/tasks/%d/stop", task.ID), nil, 409, &stop)
	if stop.Error != "This connection does not support stopping tasks." {
		t.Fatalf("stop reason %q", stop.Error)
	}

}

// mustRequestID is the request id of the task's first launch attempt.
func mustRequestID(t *testing.T, e *env, task int64) string {
	t.Helper()
	a, err := e.store.Attempt(t.Context(), task, 1)
	if err != nil || a.Intent == nil {
		t.Fatalf("attempt %+v %v", a, err)
	}
	return a.Intent.RequestID
}

// Where the profile does not allow local processes, the hosted harness is
// listed unavailable with the reason, its setup routes refuse with it
// without reaching the connector, and a task on a connection left from a
// desktop run cannot start, for the same reason.
func TestHostedHarnessRefusedWhereTheProfileDisallowsIt(t *testing.T) {
	profile := harness.Profile{Kind: harness.ProfileServer}
	reason := profile.Allows(hostedKit).Reason
	e, conn := hostedEnv(t, profile)
	ctx := t.Context()
	var list hostedListing
	e.must("GET", "/api/harnesses", nil, 200, &list)
	if available, got, connectable := list.kit(t); available || got != reason || connectable {
		t.Fatalf("listing %v %q %v", available, got, connectable)
	}
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/harnesses/hosted-kit/status"},
		{"PUT", "/api/harnesses/hosted-kit/settings"},
		{"POST", "/api/harnesses/hosted-kit/actions/connect"},
		{"DELETE", "/api/harnesses/hosted-kit/connection"},
		{"GET", "/api/machines/1/harnesses/hosted-kit/status"},
	} {
		var out errOut
		if rec := raw(t, e.h, c.method, c.path, nil, map[string]any{}, &out); rec.Code != 409 || out.Code != "harness_unavailable" || out.Error != reason {
			t.Errorf("%s %s: %d %+v", c.method, c.path, rec.Code, out)
		}
	}
	if n := conn.calls.Load(); n != 0 {
		t.Fatalf("the connector was reached %d times", n)
	}

	// A connection a desktop run left: never hosted here, so it cannot start.
	sw, err := machine.NewStorePorts(e.store.OnMachine(db.LocalMachine), hostedHarnesses).SwitchInstance(ctx, hostedKit.Kind, "/home/u/.kit", "u@kit", 0)
	if err != nil {
		t.Fatal(err)
	}
	project, err := e.store.InsertProject(ctx, db.Project{Title: "Kit", Bindings: []db.ProjectBinding{{ConnectionID: sw.New, ExternalProjectID: "repo", Workspace: hostedWorkspace}}})
	if err != nil {
		t.Fatal(err)
	}
	task, err := e.store.CreateTask(ctx, db.NewTask{ProjectID: project.ID, ConnectionID: sw.New, Title: "Work", Prompt: "Do it", ExecutionConfig: &execution.Config{Model: "m"}})
	if err != nil {
		t.Fatal(err)
	}
	var out errOut
	if rec := raw(t, e.h, "POST", fmt.Sprintf("/api/tasks/%d/start", task.ID), nil, nil, &out); rec.Code != 409 || out.Code != "connection_unavailable" || out.Error != reason {
		t.Fatalf("start: %d %+v", rec.Code, out)
	}
}

// A remote machine whose worker never described it (offline since this
// server started) has an unknown profile: the hosted kind is listed as
// available and connectable, and its setup answers that the machine is
// offline, never a guessed hosting reason. Described profiles are covered
// in remote (allows_test.go) and workersetup.
func TestHostedHarnessOnAnUndescribedRemoteMachineAnswersOffline(t *testing.T) {
	e := newEnv(t, withPublicURL, func(d *api.Deps) {
		hub := workerchan.NewHub(t.Context(), workerchan.Options{Version: buildinfo.Version})
		set := machine.MustSet(machine.NewLocal(db.LocalMachine, harness.Profile{Kind: harness.ProfileDesktop, LocalProcesses: true}, nil))
		hosts := remote.NewHosts(set, hub, func(int64) []int64 { return nil }, nil, nil)
		hosts.SetSetup(remote.SetupHooks{Kinds: []string{t3.Kind, opencode.Kind, hostedKit.Kind}, Active: d.Store.ActiveID})
		svc := t3sync.NewForMachines(d.Store, hostedHarnesses, set, nil)
		d.Harnesses, d.Sync, d.Channels, d.Machines = hostedHarnesses, svc, hub, hosts
		d.LiveConnectors = func() harness.Connectors {
			c, err := set.Connectors(hostedHarnesses)
			if err != nil {
				t.Error(err)
			}
			return c
		}
	})
	j := e.join("laptop")
	var list hostedListing
	e.must("GET", fmt.Sprintf("/api/machines/%d/harnesses", j.MachineID), nil, 200, &list)
	available, reason, connectable := list.kit(t)
	if !available || !connectable || reason != "" {
		t.Fatalf("remote listing %v %q %v", available, reason, connectable)
	}
	var out errOut
	if rec := raw(t, e.h, "POST", fmt.Sprintf("/api/machines/%d/harnesses/hosted-kit/actions/connect", j.MachineID), nil, map[string]any{}, &out); rec.Code != 503 || out.Code != "machine_offline" {
		t.Fatalf("remote connect: %d %+v", rec.Code, out)
	}
	// The local machine still allows it.
	e.must("GET", "/api/harnesses", nil, 200, &list)
	if available, _, _ := list.kit(t); !available {
		t.Fatal("local machine refused the hosted kit")
	}
}
