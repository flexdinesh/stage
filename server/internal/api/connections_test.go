package api_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/t3setup"
)

type connection struct {
	ID          int64   `json:"id"`
	Harness     string  `json:"harness"`
	Active      bool    `json:"active"`
	Label       string  `json:"label"`
	Configured  bool    `json:"configured"`
	RetiredAt   *string `json:"retiredAt"`
	ForgottenAt *string `json:"forgottenAt"`
	Tasks       int     `json:"tasks"`
	Bindings    int     `json:"bindings"`
}

func TestPairingAnotherT3RetiresTheConnectionAndItsLinks(t *testing.T) {
	e := newEnv(t, func(d *api.Deps) {
		a, err := d.Sync.Adapter(d.Store.LegacyT3ID())
		if err != nil {
			t.Fatal(err)
		}
		client := a.(*t3.Adapter).Client
		d.Setup = t3setup.New(t3setup.Options{Config: d.Config, Store: d.Store.OnMachine(db.LocalMachine), Client: client, Fake: true, Logger: d.Logger,
			OnSwitched: func(sw db.Switch) { d.Sync.SwapAdapter(sw.Old, sw.New, t3.NewConnectionAdapter(client, sw.New)) }})
	})
	first := e.store.LegacyT3ID()
	if _, err := e.store.SwitchInstance(t.Context(), db.LocalMachine, t3.Descriptor, t3.FakeEnvironmentID, "box a", 0); err != nil {
		t.Fatal(err)
	}
	p := e.project("/w/app")
	tk := e.task(p.ID, "Run", nil)
	var started task
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", tk.ID), nil, 200, &started)
	if started.Links.Web == "" || started.Links.Native == "" {
		t.Fatalf("no link before the switch %+v", started)
	}

	e.must("DELETE", "/api/harnesses/t3/connection", nil, 200, nil)
	e.fake.SetProbe(t3.Probe{Reachable: true, CheckedAt: time.Now(), Identity: &t3.Identity{EnvironmentID: "env-b", Label: "box b"}})
	var paired struct {
		ConnectionID int64       `json:"connectionId"`
		Replaced     *connection `json:"replaced"`
	}
	e.must("POST", "/api/harnesses/t3/actions/pair", map[string]any{"code": "abc"}, 200, &paired)
	if paired.Replaced == nil || paired.Replaced.ID != first || paired.Replaced.Active || paired.Replaced.Label != "box a" {
		t.Fatalf("replaced %+v", paired.Replaced)
	}
	second := e.store.LegacyT3ID()
	if second == first || paired.ConnectionID != second {
		t.Fatalf("connection %d -> %d (status %d)", first, second, paired.ConnectionID)
	}

	var got struct {
		task
		Harness          string `json:"harness"`
		ConnectionID     int64  `json:"connectionId"`
		ConnectionActive bool   `json:"connectionActive"`
		ConnectionLabel  string `json:"connectionLabel"`
	}
	e.must("GET", fmt.Sprintf("/api/tasks/%d", tk.ID), nil, 200, &got)
	if got.ConnectionID != first || got.ConnectionActive || got.ConnectionLabel != "box a" || got.Harness != "t3" || got.State != "running" {
		t.Fatalf("replaced task %+v", got)
	}
	// A retired connection is never reached again: its links are the stored
	// observation's, naming the old environment, never the new T3's.
	if got.Links != started.Links || strings.Contains(got.Links.Web, "env-b") || strings.Contains(got.Links.Native, "env-b") ||
		got.Links.Native != t3.AppThreadURL(t3.FakeEnvironmentID, *started.T3ThreadID) {
		t.Fatalf("retired links %+v, want the old environment's %+v", got.Links, started.Links)
	}

	var list struct {
		Connections []connection `json:"connections"`
	}
	e.must("GET", "/api/connections", nil, 200, &list)
	byID := map[int64]connection{}
	for _, c := range list.Connections {
		byID[c.ID] = c
	}
	if c := byID[first]; c.Active || c.Configured || c.RetiredAt == nil || c.Tasks != 1 || c.Bindings != 1 {
		t.Fatalf("replaced connection %+v", c)
	}
	if c := byID[second]; !c.Active || c.Label != "box b" || c.Bindings != 0 {
		t.Fatalf("new connection %+v", c)
	}

	e.must("DELETE", fmt.Sprintf("/api/connections/%d", second), nil, 409, nil)
	e.must("DELETE", fmt.Sprintf("/api/connections/%d", first), nil, 204, nil)
	e.must("DELETE", fmt.Sprintf("/api/connections/%d", first), nil, 404, nil)
	e.must("GET", "/api/connections", nil, 200, &list)
	for _, c := range list.Connections {
		if c.ID == first && (c.ForgottenAt == nil || c.Bindings != 0) {
			t.Fatalf("forgotten %+v", c)
		}
	}
}
