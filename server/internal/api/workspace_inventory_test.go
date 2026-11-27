package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/auth"
	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/engine"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/principal"
	"github.com/flexdinesh/stage/server/internal/remote"
	"github.com/flexdinesh/stage/server/internal/remote/remotetest"
	"github.com/flexdinesh/stage/server/internal/t3"
)

type workspaceInventory struct {
	Workspaces []db.Workspace
	Inventory  struct {
		db.WorkspaceRefresh
		State string
	}
}

type inventoryAdapter struct {
	execution.Adapter
	rows  []execution.WorkspaceInfo
	calls atomic.Int32
}

func (a *inventoryAdapter) Workspaces(context.Context) ([]execution.WorkspaceInfo, error) {
	a.calls.Add(1)
	return a.rows, nil
}

func TestConnectionInventoryAccessAndProjection(t *testing.T) {
	ctx := t.Context()
	archived := time.Now()
	const privateRoot = "/private/builds/"
	a := &inventoryAdapter{Adapter: t3.NewAdapter(t3.NewFake()), rows: []execution.WorkspaceInfo{
		{ID: "native-private-id", Title: "Build", WorkspaceRoot: privateRoot + "one"},
		{ID: "native-private-id", Title: "Build", WorkspaceRoot: privateRoot + "two"},
		{ID: "archived", Title: "Old", WorkspaceRoot: privateRoot + "three", ArchivedAt: &archived},
		{ID: "pathless", Title: "Pathless"},
	}}
	var accounts *auth.Service
	var owner auth.SignInResult
	var pending, claimed http.Handler
	e := newEnv(t, func(d *api.Deps) {
		d.Engine = engine.NewWithAdapters(d.Store, testHarnesses, map[int64]execution.Adapter{dbtest.T3Connection: a}, d.Logger)
		accounts = auth.New(d.Store, notificationOrigin, nil)
		on := true
		state, link, err := accounts.Start(ctx, &on)
		if err != nil {
			t.Fatal(err)
		}
		blocked := *d
		blocked.Access = api.NewAccess(accounts, notificationOrigin, state, nil)
		pending = api.NewRouter(blocked)
		owner, err = accounts.ClaimSetup(ctx, notificationToken(t, link.URL()), "owner@example.test", "Owner", ownerPassphrase)
		if err != nil || owner.Account.ID != 1 {
			t.Fatalf("claim setup: %v", err)
		}
		state, _, err = accounts.Start(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		admitted := *d
		admitted.Access = api.NewAccess(accounts, notificationOrigin, state, nil)
		claimed = api.NewRouter(admitted)
	})
	const inventory = "/api/connections/1/workspaces"
	var personal workspaceInventory
	e.must("GET", inventory, nil, 200, &personal)
	if personal.Inventory.State != "ready" || personal.Inventory.RefreshedAt == nil || len(personal.Workspaces) != 3 {
		t.Fatalf("personal inventory: %+v", personal)
	}
	byRoot := make(map[string]string)
	for _, row := range personal.Workspaces {
		if !row.Available || row.Importable || row.ProjectID != nil || row.WorkspaceID == "" {
			t.Fatalf("unexpected unbound row %+v", row)
		}
		byRoot[row.Workspace] = row.WorkspaceID
	}
	if byRoot[privateRoot+"one"] == byRoot[privateRoot+"two"] {
		t.Fatal("different roots share an exact selector")
	}
	p, err := e.store.InsertProject(ctx, db.Project{Title: "Builds", Bindings: []db.ProjectBinding{{
		ConnectionID: dbtest.T3Connection, ExternalProjectID: "native-private-id", Workspace: privateRoot + "one",
	}}})
	p = mustValue(t, p, err)

	member := addNotificationMember(t, accounts)
	agent, err := accounts.RotateAgent(ctx, member.Token.Reveal(), store.SessionBrowser)
	agent = mustValue(t, agent, err)
	e.h = claimed
	var visible workspaceInventory
	notifyRequest(t, e, owner, "GET", inventory+"?refresh=false", "", 200, &visible)
	if visible.Inventory.State != "ready" || len(visible.Workspaces) != len(personal.Workspaces) {
		t.Fatalf("admin inventory: %+v", visible)
	}
	notifyRequest(t, e, member, "GET", inventory, "", 200, &visible)
	if visible.Inventory.State != "ready" || len(visible.Workspaces) != len(personal.Workspaces) {
		t.Fatalf("member inventory: %+v", visible)
	}
	for _, row := range visible.Workspaces {
		if byRoot[row.Workspace] != row.WorkspaceID {
			t.Fatalf("workspace selector changed after binding: %+v", row)
		}
	}
	notifyRequest(t, e, auth.SignInResult{}, "GET", inventory, "", 401, nil)
	notifyRequest(t, e, auth.SignInResult{}, "GET", "/api/projects", "", 401, nil)
	for _, route := range []string{inventory, "/api/projects"} {
		rec := raw(t, pending, "GET", route, nil, nil, nil)
		if rec.Code != 403 || !strings.Contains(rec.Body.String(), "setup_pending") {
			t.Fatalf("pending GET %s: %d %s", route, rec.Code, rec.Body)
		}
	}
	calls := a.calls.Load()
	request := func(method, path string, want int) string {
		t.Helper()
		req := httptest.NewRequest(method, notificationOrigin+path, nil)
		req.RemoteAddr = "127.0.0.1:32101"
		req.Header.Set("Authorization", "Bearer "+principal.AgentTokenPrefix+agent.Token.Reveal())
		rec := httptest.NewRecorder()
		claimed.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body)
		}
		body := rec.Body.String()
		for _, hidden := range []string{privateRoot, "native-private-id", "canonicalPath", "lastError", "resolution", "evidenceAt", "imports"} {
			if strings.Contains(body, hidden) {
				t.Fatalf("%s exposes %q: %s", path, hidden, body)
			}
		}
		return body
	}
	request("GET", inventory, 403)
	request("POST", "/api/sync", 403)
	request("POST", "/api/connections/1/resolve", 403)
	body := request("GET", "/api/projects", 200)
	var projection client.ProjectSummaries
	if err := json.Unmarshal([]byte(body), &projection); err != nil {
		t.Fatal(err)
	}
	if len(projection.Projects) != 1 || len(projection.Destinations) != 3 {
		t.Fatalf("agent selection: %+v", projection)
	}
	for _, dest := range projection.Destinations {
		if !dest.Available || dest.MachineLabel == "" || dest.ConnectionID != 1 {
			t.Fatalf("invalid destination %+v", dest)
		}
		if dest.Title == "Build" && dest.Discriminator == "" {
			t.Fatalf("same-name destination needs a discriminator: %+v", dest)
		}
		if dest.WorkspaceID == byRoot[privateRoot+"two"] && dest.ProjectID != nil {
			t.Fatalf("unbound workspace became a binding: %+v", dest)
		}
	}
	var detail db.ProjectSummary
	if err := json.Unmarshal([]byte(request("GET", fmt.Sprintf("/api/projects/%d", p.ID), 200)), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Bindings) != 1 || detail.Bindings[0].WorkspaceID != byRoot[privateRoot+"one"] {
		t.Fatalf("bound selector %+v", detail)
	}
	if a.calls.Load() != calls {
		t.Fatal("agent or denied request refreshed the harness")
	}

	// Caller-session metadata cannot admit an unauthenticated request, and
	// a bearer can pick an unbound row using only the scoped summary.
	selection := map[string]any{"workspaceId": byRoot[privateRoot+"two"], "connectionId": 1,
		"title": "Agent planning task", "prompt": "Inspect the checkout"}
	payload, err := json.Marshal(selection)
	payload = mustValue(t, payload, err)
	create := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", notificationOrigin+"/api/tasks", bytes.NewReader(payload))
		req.RemoteAddr = "127.0.0.1:32101"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Stage-Caller-Session", "1:unverified-session")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+principal.AgentTokenPrefix+token)
		}
		rec := httptest.NewRecorder()
		claimed.ServeHTTP(rec, req)
		return rec
	}
	if rec := create(""); rec.Code != 401 {
		t.Fatalf("caller hint admitted task creation: %d %s", rec.Code, rec.Body)
	}
	rec := create(agent.Token.Reveal())
	if rec.Code != 201 {
		t.Fatalf("scoped create: %d %s", rec.Code, rec.Body)
	}
	var created db.Task
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ConnectionID != 1 || created.WorkspaceID != byRoot[privateRoot+"two"] || created.ProjectID == p.ID || created.CreatedBy != member.Account.ID {
		t.Fatalf("scoped selection lost identity or attribution: %+v", created)
	}
	group, err := e.store.GetProject(ctx, created.ProjectID)
	if err != nil || len(group.Bindings) != 0 {
		t.Fatalf("selecting an unbound workspace created a binding: %+v, %v", group, err)
	}
}

func TestWorkerInventoryKeepsLastGoodOnNegotiationFailureAndOffline(t *testing.T) {
	ctx := t.Context()
	fake := claudetest.New(t)
	dir := fake.Workspace
	fake.WriteClaudeJSON(dir)
	var rig *remotetest.Rig
	var conn int64
	e := newEnv(t, func(d *api.Deps) {
		if _, err := testDB.Pool.ExecContext(ctx, `INSERT INTO machines(id,label) VALUES(2,'Worker')`); err != nil {
			t.Fatal(err)
		}
		sw, err := d.Store.SwitchInstance(ctx, 2, claudecode.Descriptor, fake.ConfigDir(), "Worker harness", 0)
		conn = mustValue(t, sw, err).New
		rig = remotetest.NewWithConnectors(t, 2, map[int64]execution.Adapter{conn: fake.Adapter(conn)}, map[string]harness.Connector{
			claudecode.Kind: streamStatusConnector{summary: harness.Summary{ConnectionID: conn, Configured: true, Connected: true}},
		})
		registry := harness.MustRegistry(t3.Descriptor, claudecode.Descriptor)
		d.Harnesses = registry
		d.Engine = engine.NewForMachines(d.Store, registry, rig.Set(), d.Logger)
	})
	path := fmt.Sprintf("/api/connections/%d/workspaces", conn)
	var first workspaceInventory
	e.must("GET", path, nil, 200, &first)
	if len(first.Workspaces) != 1 || first.Inventory.State != "ready" || first.Inventory.RefreshedAt == nil {
		t.Fatalf("worker inventory %+v", first)
	}
	row := first.Workspaces[0]
	if row.FileHost == nil || *row.FileHost != 2 || row.CanonicalPath != dir || !row.Available {
		t.Fatalf("workspace evidence did not run on the worker: %+v", row)
	}
	if row.WorkspaceID == db.WorkspaceID(dbtest.T3Connection, row.ExternalID, row.Workspace) {
		t.Fatal("same native ID and root share a selector across machines")
	}
	checkCached := func(state string) {
		t.Helper()
		var found workspaceInventory
		e.must("GET", path, nil, 200, &found)
		if len(found.Workspaces) != 1 || found.Workspaces[0].Available || found.Workspaces[0].WorkspaceID != row.WorkspaceID ||
			found.Inventory.State != state || found.Inventory.LastError == nil || found.Inventory.RefreshedAt == nil ||
			!found.Inventory.RefreshedAt.Equal(*first.Inventory.RefreshedAt) {
			t.Fatalf("last-good inventory %+v", found)
		}
	}
	rig.StopWorker()
	checkCached("unavailable")
	rig.SetMissingRequestKinds(remote.KindInventory)
	rig.StartWorker()
	checkCached("stale")
	rig.StopWorker()
	rig.SetMissingRequestKinds()
	rig.StartWorker()
	stateFile := filepath.Join(fake.Home, ".claude.json")
	if err := os.Remove(stateFile); err != nil {
		t.Fatal(err)
	}
	checkCached("stale")
	if err := os.WriteFile(stateFile, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	checkCached("stale")
	fake.WriteClaudeJSON(dir)
	var recovered workspaceInventory
	e.must("GET", path, nil, 200, &recovered)
	if recovered.Inventory.State != "ready" || recovered.Inventory.LastError != nil || len(recovered.Workspaces) != 1 ||
		!recovered.Workspaces[0].Available || recovered.Workspaces[0].WorkspaceID != row.WorkspaceID {
		t.Fatalf("recovered inventory %+v", recovered)
	}
}
