package api_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/coordinator"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/remote"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

type observationResults struct {
	Results []struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	} `json:"results"`
}

// pushed is a bridge's observation of session on connection, as JSON.
func pushed(connection int64, session, project, state string, at time.Time) map[string]any {
	active := state == "running" || state == "needs_input"
	return map[string]any{
		"session": map[string]any{"connectionId": connection, "externalSessionId": session},
		"project": project, "title": "Pushed", "state": state, "active": active,
		"createdAt": at, "updatedAt": at,
	}
}

func pushBody(obs ...map[string]any) map[string]any {
	return map[string]any{"observations": obs}
}

// A pushed observation moves the task that owns its session exactly like an
// EventSource event, without polling; unowned sessions are left to
// discovery and malformed ones change nothing.
func TestPushObservationsIngest(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/push")
	tk := e.task(p.ID, "Pushed", map[string]any{"start": true})
	if tk.State != "running" || tk.ExternalSessionID == nil {
		t.Fatalf("started %+v", tk)
	}
	session := *tk.ExternalSessionID
	path := "/api/connections/1/observations"
	at := time.Now().UTC().Add(time.Minute)

	var out observationResults
	e.must("POST", path, pushBody(
		pushed(1, session, p.T3ProjectID, "needs_input", at),
		pushed(1, "not-tracked", p.T3ProjectID, "running", at),
	), 200, &out)
	if len(out.Results) != 2 || out.Results[0].Status != "ingested" || out.Results[1].Status != "ignored" || out.Results[1].Reason == "" {
		t.Fatalf("results %+v", out)
	}
	if got := e.getTask(tk.ID); got.State != "needs_input" {
		t.Fatalf("after needs_input push: %s", got.State)
	}
	if _, err := e.store.TaskBySession(t.Context(), db.SessionRef{ConnectionID: 1, ExternalSessionID: "not-tracked"}); err == nil {
		t.Fatal("unowned session got a task")
	}

	e.must("POST", path, pushBody(
		pushed(1, session, p.T3ProjectID, "running", at.Add(time.Second)),
		pushed(1, session, p.T3ProjectID, "done", at.Add(2*time.Second)),
	), 200, &out)
	if len(out.Results) != 2 || out.Results[0].Status != "ingested" || out.Results[1].Status != "ingested" {
		t.Fatalf("results %+v", out)
	}
	got := e.getTask(tk.ID)
	if got.State != "done" {
		t.Fatalf("after done push: %s", got.State)
	}
	last := got.Events[len(got.Events)-1]
	if last.ToState != "done" || last.Detail != "execution observed done" {
		t.Fatalf("last event %+v", last)
	}

	// Malformed: unknown state, contradictory activity, missing updatedAt,
	// updatedAt far ahead of the server's clock (it would pin the stored
	// snapshot), and a lost owner reported by a harness that has not
	// declared that reason.
	bogus := pushed(1, session, p.T3ProjectID, "exploded", at.Add(3*time.Second))
	contradictory := pushed(1, session, p.T3ProjectID, "failed", at.Add(3*time.Second))
	contradictory["active"] = true
	undated := pushed(1, session, p.T3ProjectID, "failed", at.Add(3*time.Second))
	delete(undated, "updatedAt")
	future := pushed(1, session, p.T3ProjectID, "failed", time.Now().UTC().Add(time.Hour))
	ownerExited := pushed(1, session, p.T3ProjectID, "interrupted", at.Add(3*time.Second))
	ownerExited["stateReason"] = "owner_exited"
	unownedBogus := pushed(1, "not-tracked", p.T3ProjectID, "exploded", at)
	e.must("POST", path, pushBody(bogus, contradictory, undated, future, ownerExited, unownedBogus), 200, &out)
	if len(out.Results) != 6 {
		t.Fatalf("results %+v", out)
	}
	for i, r := range out.Results {
		if r.Status != "invalid" || r.Reason == "" {
			t.Fatalf("result %d %+v", i, r)
		}
	}
	after := e.getTask(tk.ID)
	if after.State != "done" || len(after.Events) != len(got.Events) {
		t.Fatalf("malformed push changed the task: %s, %d events", after.State, len(after.Events))
	}
	o, err := e.store.Observation(t.Context(), db.SessionRef{ConnectionID: 1, ExternalSessionID: session})
	if err != nil || o.State != "done" {
		t.Fatalf("stored observation %+v %v", o, err)
	}
}

func TestPushObservationsRefused(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/push-refused")
	tk := e.task(p.ID, "Pushed", map[string]any{"start": true})
	session := *tk.ExternalSessionID
	at := time.Now().UTC().Add(time.Minute)

	// A session of another connection fails the whole batch: nothing is ingested.
	e.must("POST", "/api/connections/1/observations", pushBody(
		pushed(1, session, p.T3ProjectID, "done", at),
		pushed(2, session, p.T3ProjectID, "done", at),
	), 400, nil)
	if got := e.getTask(tk.ID); got.State != "running" {
		t.Fatalf("rejected batch changed the task: %s", got.State)
	}
	// Malformed JSON, unknown fields and an empty or oversized batch are 400.
	e.must("POST", "/api/connections/1/observations", map[string]any{"observation": []any{}}, 400, nil)
	e.must("POST", "/api/connections/1/observations", pushBody(), 400, nil)
	many := make([]map[string]any, 101)
	for i := range many {
		many[i] = pushed(1, fmt.Sprintf("s%d", i), p.T3ProjectID, "running", at)
	}
	e.must("POST", "/api/connections/1/observations", pushBody(many...), 400, nil)
	if rec := raw(t, e.h, "POST", "/api/connections/1/observations", map[string]string{"Content-Type": "application/json"}, nil, nil); rec.Code != 400 {
		t.Fatalf("empty body: %d", rec.Code)
	}

	// Unknown connection.
	e.must("POST", "/api/connections/999/observations", pushBody(pushed(999, session, p.T3ProjectID, "done", at)), 404, nil)

	// A replaced (retired) connection.
	ctx := t.Context()
	if _, err := e.store.SwitchInstance(ctx, db.LocalMachine, t3.Descriptor, "env-a", "box a", 0); err != nil {
		t.Fatal(err)
	}
	sw, err := e.store.SwitchInstance(ctx, db.LocalMachine, t3.Descriptor, "env-b", "box b", 0)
	if err != nil || !sw.Changed || sw.Old != 1 {
		t.Fatalf("switch %+v %v", sw, err)
	}
	var conflict errOut
	e.must("POST", "/api/connections/1/observations", pushBody(pushed(1, session, p.T3ProjectID, "done", at)), 409, &conflict)
	if conflict.Code != "connection_retired" {
		t.Fatalf("retired %+v", conflict)
	}
	if got := e.getTask(tk.ID); got.State != "running" {
		t.Fatalf("push on a retired connection changed the task: %s", got.State)
	}
}

// Through a worker's agent gateway, a machine pushes only for its own
// connections.
func TestPushObservationsThroughGateway(t *testing.T) {
	e := gatewayEnv(t)
	j := e.join("laptop")
	p := e.project("/w/push-gw")
	tk := e.task(p.ID, "Pushed", map[string]any{"start": true})
	var refused errOut
	rec := e.gw(j.Token, "POST", "/api/connections/1/observations", pushBody(pushed(1, *tk.ExternalSessionID, p.T3ProjectID, "done", time.Now().UTC().Add(time.Minute))), &refused)
	if rec.Code != 403 || refused.Code != "gateway_forbidden" {
		t.Fatalf("gateway push for another machine: %d %s", rec.Code, rec.Body)
	}
	if got := e.getTask(tk.ID); got.State != "running" {
		t.Fatalf("refused push changed the task: %s", got.State)
	}
}

// An offline worker is an offline harness: a push for a connection on its
// machine changes nothing and answers as a launch there would.
func TestPushObservationsForAnOfflineMachine(t *testing.T) {
	e := newEnv(t, withPublicURL, func(d *api.Deps) {
		hub := workerchan.NewHub(t.Context(), workerchan.Options{Version: buildinfo.Version})
		set := machine.MustSet(machine.NewLocal(db.LocalMachine, harness.Profile{Kind: harness.ProfileServer}, nil))
		store := d.Store
		hosts := remote.NewHosts(set, hub, func(m int64) []int64 {
			var ids []int64
			for _, c := range store.ActiveConnections() {
				if c.Machine == m {
					ids = append(ids, c.ID)
				}
			}
			return ids
		}, nil, nil)
		svc := coordinator.NewForMachines(store, testHarnesses, set, nil)
		d.Sync, d.Channels, d.Machines = svc, hub, hosts
	})
	j := e.join("laptop")
	sw, err := e.store.SwitchInstance(t.Context(), j.MachineID, t3.Descriptor, "env-laptop", "laptop t3", 0)
	if err != nil {
		t.Fatal(err)
	}
	conn := sw.New
	ref := db.SessionRef{ConnectionID: conn, ExternalSessionID: "laptop-thread"}
	var out errOut
	path := fmt.Sprintf("/api/connections/%d/observations", conn)
	if rec := raw(t, e.h, "POST", path, nil, pushBody(pushed(conn, ref.ExternalSessionID, "proj", "running", time.Now().UTC())), &out); rec.Code != 409 || out.Code != "connection_unavailable" || out.Error != "Its machine is offline: the Stage worker there is not connected." {
		t.Fatalf("push on an offline machine: %d %+v", rec.Code, out)
	}
	if _, err := e.store.Observation(t.Context(), ref); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("offline push stored an observation: %v", err)
	}
}
