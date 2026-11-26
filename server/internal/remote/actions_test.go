package remote_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/remote"
	"github.com/flexdinesh/stage/server/internal/remote/remotetest"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

type actingProbe struct {
	probe
	act func(context.Context, execution.ActionRequest) (execution.ActionResult, error)
}

func (p *actingProbe) Act(ctx context.Context, r execution.ActionRequest) (execution.ActionResult, error) {
	return p.act(ctx, r)
}

func TestRemoteActionTargetsAndResult(t *testing.T) {
	requests := make(chan execution.ActionRequest, 6)
	want := execution.ActionResult{
		Message: "Done", Link: "https://example.com/session/s",
		Facts: []execution.Fact{{ID: "session", Label: "Session", Value: "s", Tone: execution.ToneOK}},
	}
	p := &actingProbe{act: func(_ context.Context, r execution.ActionRequest) (execution.ActionResult, error) {
		requests <- r
		return want, nil
	}}
	rig := remotetest.New(t, 2, map[int64]execution.Adapter{5: p, 6: &probe{}})
	a := rig.Adapter(5)
	actor, ok := execution.As[execution.Actor](a)
	if !ok || execution.TraitOf[execution.Actor]() != "actor" {
		t.Fatal("ready did not advertise the actor trait")
	}
	session := execution.SessionRef{ConnectionID: 5, ExternalSessionID: "s"}
	workspace := execution.ActionWorkspace{ExternalProjectID: "p", Directory: "/worker/w"}
	input := json.RawMessage(`{"value":42}`)
	for _, r := range []execution.ActionRequest{
		{ID: "session-action", Target: execution.TargetSession, Session: &session, Input: input},
		{ID: "project-action", Target: execution.TargetProject, Project: &workspace, Input: input},
	} {
		got, err := actor.Act(t.Context(), r)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("Act(%s) = %+v, %v", r.Target, got, err)
		}
		select {
		case got := <-requests:
			if !reflect.DeepEqual(got, r) {
				t.Fatalf("worker got %+v, want %+v", got, r)
			}
		default:
			t.Fatal("worker did not run action")
		}
	}
	for _, r := range []execution.ActionRequest{
		{ID: "session-action", Target: execution.TargetSession, Session: &execution.SessionRef{ConnectionID: 6, ExternalSessionID: "s"}},
		{ID: "session-action", Target: execution.TargetSession, Session: &session, Project: &workspace},
		{ID: "project-action", Target: execution.TargetProject},
		{ID: "project-action", Target: execution.TargetConnection, Project: &workspace},
	} {
		var invalid *execution.Invalid
		if _, err := actor.Act(t.Context(), r); !errors.As(err, &invalid) {
			t.Errorf("Act(%+v) error = %v, want invalid", r, err)
		}
	}
	if len(requests) != 0 {
		t.Fatal("worker ran an invalid target")
	}
	if _, ok := execution.As[execution.Actor](rig.Adapter(6)); ok {
		t.Fatal("worker offered Actor on an adapter without it")
	}
	withoutActor := rig.Adapter(6).(*remote.Adapter)
	if _, err := withoutActor.Act(t.Context(), execution.ActionRequest{}); !errors.Is(err, execution.ErrUnavailable) {
		t.Fatalf("unannounced trait error = %v", err)
	}
	rig.StopWorker()
	rig.SetMissingRequestKinds(remote.KindAct)
	rig.StartWorker()
	if _, ok := execution.As[execution.Actor](a); ok {
		t.Fatal("worker offered Actor without the action request kind")
	}
	if _, err := actor.Act(t.Context(), execution.ActionRequest{}); !errors.Is(err, execution.ErrUnavailable) {
		t.Fatalf("unannounced request kind error = %v", err)
	}
	rig.StopWorker()
	if _, ok := execution.As[execution.Actor](a); ok {
		t.Fatal("offline worker offered Actor")
	}
	if _, err := actor.Act(t.Context(), execution.ActionRequest{}); !errors.Is(err, remote.ErrOffline) {
		t.Fatalf("offline actor error = %v", err)
	}
}

func TestRemoteActionErrorsKeepTheirMeaning(t *testing.T) {
	p := &actingProbe{act: func(_ context.Context, r execution.ActionRequest) (execution.ActionResult, error) {
		switch r.ID {
		case "invalid":
			return execution.ActionResult{}, &execution.Invalid{Reason: "invalid input"}
		case "busy":
			return execution.ActionResult{}, &execution.Busy{Reason: "busy session"}
		case "unavailable":
			return execution.ActionResult{}, execution.ErrUnavailable
		default:
			return execution.ActionResult{}, context.DeadlineExceeded
		}
	}}
	rig := remotetest.New(t, 2, map[int64]execution.Adapter{5: p})
	actor, ok := execution.As[execution.Actor](rig.Adapter(5))
	if !ok {
		t.Fatal("missing actor")
	}
	run := func(id string) error {
		_, err := actor.Act(t.Context(), execution.ActionRequest{ID: id, Target: execution.TargetProject, Project: &execution.ActionWorkspace{Directory: "/worker/w"}})
		return err
	}
	var invalid *execution.Invalid
	if err := run("invalid"); !errors.As(err, &invalid) || invalid.Reason != "invalid input" {
		t.Fatalf("invalid action error = %v", err)
	}
	var busy *execution.Busy
	if err := run("busy"); !errors.As(err, &busy) || busy.Reason != "busy session" {
		t.Fatalf("busy action error = %v", err)
	}
	if err := run("unavailable"); !errors.Is(err, execution.ErrUnavailable) {
		t.Fatalf("unavailable action error = %v", err)
	}
	var uncertain *execution.ActionUncertain
	if err := run("deadline"); !errors.As(err, &uncertain) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("worker deadline error = %v", err)
	}
}

func TestRemoteActionLostOrMalformedReplyIsUncertain(t *testing.T) {
	p := &actingProbe{}
	p.act = func(context.Context, execution.ActionRequest) (execution.ActionResult, error) {
		p.effect()
		return execution.ActionResult{}, nil
	}
	rig := remotetest.New(t, 2, map[int64]execution.Adapter{5: p})
	actor, ok := execution.As[execution.Actor](rig.Adapter(5))
	if !ok {
		t.Fatal("missing actor")
	}
	r := execution.ActionRequest{ID: "act", Target: execution.TargetProject, Project: &execution.ActionWorkspace{Directory: "/worker/w"}}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	var uncertain *execution.ActionUncertain
	if _, err := actor.Act(cancelled, r); !errors.Is(err, context.Canceled) || errors.As(err, &uncertain) {
		t.Fatalf("preflight cancellation error = %v", err)
	}
	rig.LoseResult(remote.KindAct, 1)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := actor.Act(ctx, r); !errors.As(err, &uncertain) || !errors.Is(err, workerchan.ErrLostReply) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost action reply error = %v", err)
	}
	if got := p.count(); got != 1 {
		t.Fatalf("worker ran action %d times", got)
	}
	rig.StopWorker()
	rig.StartWorker()
	rig.ReplaceResult(remote.KindAct, json.RawMessage(`null`))
	if _, err := actor.Act(t.Context(), r); !errors.As(err, &uncertain) {
		t.Fatalf("malformed action reply error = %v", err)
	}
	if got := p.count(); got != 2 {
		t.Fatalf("worker ran action %d times", got)
	}
}

type actionConnector struct {
	harness.Connector
	active    atomic.Int64
	runs      atomic.Int64
	statusErr error
	configure func()
}

func (c *actionConnector) Summary(context.Context) harness.Summary {
	return harness.Summary{ConnectionID: c.active.Load(), Connected: true}
}

func (c *actionConnector) Status(ctx context.Context, _ bool) (harness.Status, error) {
	return harness.Status{Summary: c.Summary(ctx)}, c.statusErr
}

func (c *actionConnector) Configure(context.Context, json.RawMessage) (harness.Result, error) {
	c.configure()
	return harness.Result{}, nil
}

func (c *actionConnector) Run(context.Context, string, json.RawMessage) (harness.Result, error) {
	c.runs.Add(1)
	return harness.Result{Message: "Ran", Replaced: 3}, nil
}

func TestRemoteSetupActionResultDoesNotDependOnStatus(t *testing.T) {
	h := &actionConnector{statusErr: errors.New("status unavailable")}
	h.active.Store(5)
	rig := remotetest.NewWithConnectors(t, 2, nil, map[string]harness.Connector{"fixture": h})
	proxy := rig.Machine().Connectors()["fixture"].(*remote.Connector)
	ctx := t.Context()
	if _, err := proxy.Status(ctx, false); err == nil {
		t.Fatal("fixture returned a status")
	}
	if result, err := proxy.RunForConnection(ctx, 5, "run", nil); err != nil || result.Replaced != 3 || result.Message != "Ran" {
		t.Fatalf("successful action result = %+v, %v", result, err)
	}
	if got := proxy.Summary(ctx); got.ConnectionID != 5 || !got.Connected {
		t.Fatalf("setup action lost the advertised summary: %+v", got)
	}
	var retired *db.ConflictError
	if _, err := proxy.RunForConnection(ctx, 4, "run", nil); !errors.As(err, &retired) || retired.Code != "connection_retired" {
		t.Fatalf("stale action error = %v", err)
	}
	if count := h.runs.Load(); count != 1 {
		t.Fatalf("worker ran action %d times", count)
	}
	rig.StopWorker()
	var offline *remote.UnavailableError
	if _, err := proxy.Run(ctx, "run", nil); !errors.As(err, &offline) || offline.Code != remote.CodeMachineOffline {
		t.Fatalf("offline setup action error = %v", err)
	}
	rig.SetMissingRequestKinds(remote.KindAct)
	rig.StartWorker()
	if _, err := proxy.RunForConnection(ctx, 5, "run", nil); !errors.As(err, &offline) || offline.Code != remote.CodeMachineNeedsUpdate {
		t.Fatalf("unfenced worker scoped action error = %v", err)
	}
	if _, err := proxy.Run(ctx, "run", nil); err != nil {
		t.Fatalf("unscoped action on older peer = %v", err)
	}
	if count := h.runs.Load(); count != 2 {
		t.Fatalf("worker ran action %d times", count)
	}
}

func TestRemoteSetupActionWaitsForConnectionMutation(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	h := &actionConnector{}
	h.active.Store(5)
	h.configure = func() {
		close(started)
		<-release
		h.active.Store(6)
	}
	rig := remotetest.NewWithConnectors(t, 2, nil, map[string]harness.Connector{"fixture": h})
	proxy := rig.Machine().Connectors()["fixture"].(*remote.Connector)
	configured := make(chan error, 1)
	go func() {
		_, err := proxy.Configure(t.Context(), nil)
		configured <- err
	}()
	defer func() {
		close(release)
		if err := <-configured; err != nil {
			t.Errorf("configure: %v", err)
		}
	}()
	select {
	case <-started:
	case err := <-configured:
		configured <- err
		t.Fatal("configure never started", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	var uncertain *execution.ActionUncertain
	if _, err := proxy.RunForConnection(ctx, 5, "run", nil); !errors.As(err, &uncertain) {
		t.Fatalf("action finished while configure was pending: %v", err)
	}
	if count := h.runs.Load(); count != 0 {
		t.Fatalf("worker ran %d actions while configure was pending", count)
	}
}

func TestRemoteSetupActionLostOrMalformedReplyIsUncertain(t *testing.T) {
	h := &actionConnector{}
	rig := remotetest.NewWithConnectors(t, 2, nil, map[string]harness.Connector{"fixture": h})
	proxy := rig.Machine().Connectors()["fixture"]
	rig.LoseResult(remote.KindConnectorRun, 1)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	var uncertain *execution.ActionUncertain
	if _, err := proxy.Run(ctx, "run", nil); !errors.As(err, &uncertain) {
		t.Fatalf("lost setup action reply error = %v", err)
	}
	rig.ReplaceResult(remote.KindConnectorRun, json.RawMessage(`{"status":{}}`))
	if _, err := proxy.Run(t.Context(), "run", nil); !errors.As(err, &uncertain) {
		t.Fatalf("malformed setup action reply error = %v", err)
	}
	if count := h.runs.Load(); count != 2 {
		t.Fatalf("worker ran action %d times", count)
	}
}
