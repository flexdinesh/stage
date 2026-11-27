package agenttools_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
)

func TestProjectReadTools(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/reads")
	cs := connect(t, &agenttools.Ops{Client: e.client})

	var got db.ProjectSummary
	mustCall(t, cs, "get_project", map[string]any{"id": p.ID}, &got)
	if got.ID != p.ID || got.Title != p.Title || got.ArchivedAt != nil {
		t.Fatalf("get_project %+v, want %d %q", got, p.ID, p.Title)
	}
	if msg := call(t, cs, "get_project", map[string]any{"id": 999}, nil); msg == "" {
		t.Fatal("get_project unknown id succeeded")
	}

	var groups agenttools.ProjectGroupsOut
	mustCall(t, cs, "list_project_groups", nil, &groups)
	if !slices.ContainsFunc(groups.Groups, func(g db.ProjectGroupSummary) bool { return g.ID == p.ID && g.Title == p.Title }) {
		t.Fatalf("list_project_groups %+v, want group %d", groups, p.ID)
	}
	mustCall(t, cs, "list_project_groups", map[string]any{"archived": "true"}, &groups)
	if len(groups.Groups) != 0 {
		t.Fatalf("archived groups %+v", groups)
	}
	if msg := call(t, cs, "list_project_groups", map[string]any{"archived": "maybe"}, nil); msg == "" {
		t.Fatal("list_project_groups bad archived succeeded")
	}

	var group db.ProjectGroupSummary
	mustCall(t, cs, "get_project_group", map[string]any{"id": p.ID}, &group)
	if group.ID != p.ID || group.Title != p.Title {
		t.Fatalf("get_project_group %+v", group)
	}
}

func TestMachineConnectionHarnessReadTools(t *testing.T) {
	e := newEnv(t)
	cs := connect(t, &agenttools.Ops{Client: e.client})

	var conns agenttools.ConnectionsOut
	mustCall(t, cs, "list_connections", nil, &conns)
	if !slices.ContainsFunc(conns.Connections, func(c db.HarnessConnection) bool { return c.ID == dbtest.T3Connection && c.Harness == "t3" }) {
		t.Fatalf("list_connections %+v", conns)
	}

	var machines agenttools.MachinesOut
	mustCall(t, cs, "list_machines", nil, &machines)
	if len(machines.Machines) == 0 || machines.Machines[0].Kind != "local" {
		t.Fatalf("list_machines %+v", machines)
	}
	local := machines.Machines[0]
	var m client.Machine
	mustCall(t, cs, "get_machine", map[string]any{"id": local.ID}, &m)
	if m.ID != local.ID || m.Label != local.Label || m.Kind != "local" {
		t.Fatalf("get_machine %+v, want %+v", m, local)
	}
	if msg := call(t, cs, "get_machine", map[string]any{"id": 999}, nil); msg == "" {
		t.Fatal("get_machine unknown id succeeded")
	}

	for _, args := range []map[string]any{nil, {"machineId": local.ID}} {
		var hs client.Harnesses
		mustCall(t, cs, "list_harnesses", args, &hs)
		if _, ok := hs.Lookup("t3"); !ok || hs.Machine.ID != local.ID {
			t.Fatalf("list_harnesses %v: %+v", args, hs)
		}
	}
	if msg := call(t, cs, "list_harnesses", map[string]any{"machineId": 999}, nil); msg == "" {
		t.Fatal("list_harnesses unknown machine succeeded")
	}

	// The test server has no connectors: both routes reach the handler and report it.
	for _, args := range []map[string]any{{"kind": "t3"}, {"kind": "t3", "machineId": local.ID}} {
		if msg := call(t, cs, "get_harness_status", args, nil); !strings.Contains(msg, "no connection setup") {
			t.Fatalf("get_harness_status %v: %q", args, msg)
		}
	}
	if msg := call(t, cs, "get_harness_status", map[string]any{"kind": ""}, nil); !strings.Contains(msg, "kind is required") {
		t.Fatalf("get_harness_status without kind: %q", msg)
	}
	if msg := call(t, cs, "get_harness_status", map[string]any{"kind": "nope"}, nil); msg == "" {
		t.Fatal("get_harness_status unknown kind succeeded")
	}
}

func TestGetPreferencesTool(t *testing.T) {
	e := newEnv(t)
	cs := connect(t, &agenttools.Ops{Client: e.client})
	var prefs client.Preferences
	mustCall(t, cs, "get_preferences", nil, &prefs)
	if prefs.DefaultConnectionID != nil {
		t.Fatalf("default connection before set: %+v", prefs)
	}
	if _, err := e.store.SetDefaultConnection(t.Context(), new(int64(dbtest.T3Connection))); err != nil {
		t.Fatal(err)
	}
	mustCall(t, cs, "get_preferences", nil, &prefs)
	if prefs.DefaultConnectionID == nil || *prefs.DefaultConnectionID != dbtest.T3Connection {
		t.Fatalf("get_preferences %+v", prefs)
	}
}

func TestDissolveTreeTool(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/dissolve")
	workspace := e.workspaceID(p.Bindings[0])
	cs := connect(t, &agenttools.Ops{Client: e.client, Caller: agentctx.Caller{Cwd: "/w/dissolve"}})
	newTask := func(title string) client.Task {
		t.Helper()
		var tk client.Task
		mustCall(t, cs, "create_task", map[string]any{"connectionId": dbtest.T3Connection, "workspaceId": workspace, "title": title, "prompt": "do " + title}, &tk)
		return tk
	}
	a, b := newTask("a"), newTask("b")
	var tree client.TreeDetail
	mustCall(t, cs, "create_tree", map[string]any{"title": "Split", "tasks": []map[string]any{{"id": a.ID}, {"id": b.ID, "needs": []int64{a.ID}}}}, &tree)

	var out agenttools.DissolvedOut
	mustCall(t, cs, "dissolve_tree", map[string]any{"id": tree.ID}, &out)
	if !out.Dissolved || out.ID != tree.ID {
		t.Fatalf("dissolve_tree %+v", out)
	}
	mustCall(t, cs, "get_tree", map[string]any{"id": tree.ID}, &tree)
	if tree.Status != db.TreeArchived || len(tree.Tasks) != 0 {
		t.Fatalf("dissolved tree %+v", tree)
	}
	var detail client.TaskDetail
	mustCall(t, cs, "get_task", map[string]any{"id": b.ID}, &detail)
	if detail.TreeID != nil || len(detail.Needs) != 0 {
		t.Fatalf("dissolved member %+v", detail.Task)
	}
	if msg := call(t, cs, "dissolve_tree", map[string]any{"id": 999}, nil); msg == "" {
		t.Fatal("dissolve_tree unknown id succeeded")
	}
}

func TestGetHarnessStatusRoutes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/harnesses/t3/status", "/api/machines/2/harnesses/t3/status":
			json.NewEncoder(w).Encode(client.HarnessStatus{Kind: "t3", State: r.URL.Path})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	cs := connect(t, &agenttools.Ops{Client: client.New(srv.URL)})
	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"kind": "t3"}, "/api/harnesses/t3/status"},
		{map[string]any{"kind": "t3", "machineId": 2}, "/api/machines/2/harnesses/t3/status"},
	} {
		var st client.HarnessStatus
		mustCall(t, cs, "get_harness_status", c.args, &st)
		if st.Kind != "t3" || st.State != c.want {
			t.Fatalf("get_harness_status %v: %+v", c.args, st)
		}
	}
}
