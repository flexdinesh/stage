package api_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/coordinator"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/remote/remotetest"
	"github.com/flexdinesh/stage/server/internal/t3"
)

type statusEvents struct {
	execution.Adapter
	fail    atomic.Bool
	wait    atomic.Bool
	started chan struct{}
}

const streamFailure = "event delivery broke"

func (a *statusEvents) Events(ctx context.Context, _ execution.Sink) error {
	if a.wait.Load() {
		close(a.started)
		<-ctx.Done()
		return ctx.Err()
	}
	if a.fail.Load() {
		return errors.New(streamFailure)
	}
	return nil
}

type streamStatusConnector struct {
	harness.Connector
	summary harness.Summary
}

func (c streamStatusConnector) Summary(context.Context) harness.Summary { return c.summary }

func (c streamStatusConnector) Status(context.Context, bool) (harness.Status, error) {
	return harness.Status{Summary: c.summary, State: "connected"}, nil
}

func TestStreamStatusParity(t *testing.T) {
	for _, name := range []string{"local", "worker"} {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			fake := t3.NewFake()
			workspace := fake.AddProject("app", "/work/app")
			src := &statusEvents{started: make(chan struct{})}
			var logs bytes.Buffer
			var connection int64
			var route string
			var svc *coordinator.Service
			var rig *remotetest.Rig
			e := newEnv(t, func(d *api.Deps) {
				const machineID = 2
				if _, err := testDB.Pool.ExecContext(ctx, `INSERT INTO machines(id,label) VALUES(2,'Test host')`); err != nil {
					t.Fatal(err)
				}
				sw, err := d.Store.SwitchInstance(ctx, machineID, t3.Descriptor, t3.FakeEnvironmentID, name, 0)
				if err != nil {
					t.Fatal(err)
				}
				connection = sw.New
				route = fmt.Sprintf("/api/machines/%d/harnesses/t3/status", machineID)
				src.Adapter = t3.NewConnectionAdapter(fake, connection)
				connector := streamStatusConnector{summary: harness.Summary{ConnectionID: connection, Configured: true, Connected: true}}
				var set *machine.Set
				if name == "worker" {
					rig = remotetest.NewWithConnectors(t, machineID, map[int64]execution.Adapter{connection: src}, map[string]harness.Connector{t3.Kind: connector})
					set = rig.Set()
					local, ok := set.Local(set.DefaultID())
					if !ok {
						t.Fatal("local host missing")
					}
					localConnection := d.Store.ActiveID(local.ID(), t3.Kind)
					local.SetConnector(t3.Kind, streamStatusConnector{summary: harness.Summary{ConnectionID: localConnection, Configured: true, Connected: true}})
				} else {
					route = "/api/harnesses/t3/status"
					local := machine.NewLocal(machineID, harness.Profile{}, nil)
					local.Install(connection, connection, src)
					local.SetConnector(t3.Kind, connector)
					set = machine.MustSet(local)
				}
				svc = coordinator.NewForMachines(d.Store, testHarnesses, set, slog.New(slog.NewTextHandler(&logs, nil)))
				svc.PublicURL = publicURL
				d.Sync = svc
				connectors, err := set.Connectors(testHarnesses)
				if err != nil {
					t.Fatal(err)
				}
				d.LiveConnectors = func() harness.Connectors { return connectors }
			})
			if name == "local" {
				var resolved db.HarnessConnection
				e.must("GET", "/api/connections/resolve?harness=t3", nil, 200, &resolved)
				if resolved.ID != connection {
					t.Fatalf("resolved connection %d, want %d", resolved.ID, connection)
				}
			}
			p, err := e.store.InsertProject(ctx, db.Project{Title: "app", Bindings: []db.ProjectBinding{{ConnectionID: connection, ExternalProjectID: workspace.ID, Workspace: workspace.WorkspaceRoot}}})
			if err != nil {
				t.Fatal(err)
			}
			tk, err := e.store.CreateTask(ctx, db.NewTask{ProjectID: p.ID, ConnectionID: connection, Title: "Run", Prompt: "work"})
			if err != nil {
				t.Fatal(err)
			}
			tk, err = svc.StartTask(ctx, tk.ID)
			if err != nil || tk.Session == nil {
				t.Fatalf("launch: %+v %v", tk, err)
			}
			attempt, err := e.store.Attempt(ctx, tk.ID, tk.LaunchAttempt)
			if err != nil || attempt.Intent == nil {
				t.Fatalf("launch intent: %+v %v", attempt, err)
			}
			wantAPI := publicURL + "/api"
			if name == "worker" {
				wantAPI = "on this machine's Stage agent gateway socket"
			}
			if !strings.Contains(attempt.Intent.Prompt, wantAPI) {
				t.Fatalf("launch instructions missing %q: %s", wantAPI, attempt.Intent.Prompt)
			}

			checkStatus := func(want string) {
				t.Helper()
				var st harness.Status
				e.must("GET", route, nil, 200, &st)
				if st.ConnectionID != connection || !st.Connected || (want == "" && st.LastError != "") || !strings.Contains(st.LastError, want) {
					t.Fatalf("status %+v, want error %q", st, want)
				}
				if name == "local" {
					var health api.Health
					e.must("GET", "/api/health", nil, 200, &health)
					if len(health.Harnesses) != 1 {
						t.Fatalf("health harnesses: %+v", health.Harnesses)
					}
					last := health.Harnesses[0].LastError
					if (want == "" && last != nil) || (want != "" && (last == nil || !strings.Contains(*last, want))) {
						t.Fatalf("health error %v, want %q", last, want)
					}
				}
			}
			src.fail.Store(true)
			if err := svc.Stream(ctx, connection); err == nil || !strings.Contains(err.Error(), streamFailure) {
				t.Fatalf("stream error %v", err)
			}
			checkStatus(streamFailure)
			if rig != nil {
				var local harness.Status
				e.must("GET", "/api/harnesses/t3/status", nil, 200, &local)
				if local.ConnectionID == 0 || local.ConnectionID == connection || local.LastError != "" {
					t.Fatalf("worker error affected local status: %+v", local)
				}
			}
			if !strings.Contains(logs.String(), fmt.Sprintf("connection=%d", connection)) || !strings.Contains(logs.String(), streamFailure) {
				t.Fatalf("stream log %q", logs.String())
			}
			if rig != nil {
				rig.StopWorker()
				if err := svc.Stream(ctx, connection); err != nil || !strings.Contains(svc.StreamError(connection), streamFailure) {
					t.Fatalf("offline stream changed previous error to %q (returned %v)", svc.StreamError(connection), err)
				}
				rig.StartWorker()
			}

			if err := fake.SetStatus(tk.Session.ExternalSessionID, t3.StatusCompleted); err != nil {
				t.Fatal(err)
			}
			if err := svc.PollActive(ctx); err != nil {
				t.Fatal(err)
			}
			if got := e.getTask(tk.ID); got.State != "done" {
				t.Fatalf("poll during stream failure: task landed in %s", got.State)
			}
			checkStatus(streamFailure)

			before := logs.String()
			src.wait.Store(true)
			streamCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				_ = svc.Stream(streamCtx, connection)
			}()
			select {
			case <-src.started:
			case <-time.After(5 * time.Second):
				t.Fatal("stream never started")
			}
			cancel()
			select {
			case <-finished:
			case <-time.After(5 * time.Second):
				t.Fatal("stream never stopped")
			}
			if logs.String() != before {
				t.Fatalf("shutdown logged a stream failure: %s", logs.String())
			}
			checkStatus(streamFailure)
			src.wait.Store(false)
			src.fail.Store(false)
			if err := svc.Stream(ctx, connection); err != nil {
				t.Fatal(err)
			}
			checkStatus("")
		})
	}
}
