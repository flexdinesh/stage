package api_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/auth"
	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/engine"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/principal"
	"github.com/flexdinesh/stage/server/internal/remote"
	"github.com/flexdinesh/stage/server/internal/remote/remotetest"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// browsingAdapter declares directoryBrowse and answers Browse from a fixed
// host tree (or fail), counting calls.
type browsingAdapter struct {
	execution.Adapter
	mu    sync.Mutex
	fail  error
	calls atomic.Int64
	last  execution.BrowseRequest
	big   bool
}

func (a *browsingAdapter) Features(ctx context.Context) (execution.Capabilities, error) {
	c, err := a.Adapter.Features(ctx)
	if err != nil {
		return c, err
	}
	c.Operations[execution.OpDirectoryBrowse] = execution.Availability{Available: true}
	return c, nil
}

func (a *browsingAdapter) Browse(_ context.Context, r execution.BrowseRequest) (execution.BrowseResult, error) {
	a.calls.Add(1)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.last = r
	if a.fail != nil {
		return execution.BrowseResult{}, a.fail
	}
	dir := r.Path
	if dir == "" {
		dir = "/home/me"
	}
	entries := []execution.DirEntry{{Name: "src", Path: dir + "/src"}, {Name: ".git", Path: dir + "/.git"}}
	if a.big {
		// A misbehaving adapter: Stage still answers at most the bound.
		out := execution.BrowseResult{Path: dir, Parent: "/"}
		for i := range execution.MaxDirectoryEntries + 5 {
			out.Entries = append(out.Entries, execution.DirEntry{Name: fmt.Sprint(i), Path: fmt.Sprintf("%s/%d", dir, i)})
		}
		return out, nil
	}
	return execution.Listing(dir, entries, r.ShowHidden), nil
}

func (a *browsingAdapter) set(fail error, big bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.fail, a.big = fail, big
}

func directoriesPath(conn int64, query string) string {
	return fmt.Sprintf("/api/connections/%d/directories%s", conn, query)
}

func TestBrowseDirectoriesLocal(t *testing.T) {
	f := claudetest.New(t)
	var id, t3ID int64
	browser := &browsingAdapter{}
	e := newEnv(t, func(d *api.Deps) {
		sw, err := d.Store.SwitchInstance(t.Context(), db.LocalMachine, claudecode.Descriptor, f.ConfigDir(), "Local Claude", 0)
		id = mustValue(t, sw, err).New
		browser.Adapter = f.Adapter(id)
		t3ID = d.Store.ActiveID(db.LocalMachine, t3.Kind)
		d.Harnesses = harness.MustRegistry(claudecode.Descriptor, t3.Descriptor)
		d.Engine = engine.NewWithAdapters(d.Store, d.Harnesses, map[int64]execution.Adapter{
			id: browser, t3ID: t3.NewAdapter(t3.NewFake()),
		}, d.Logger)
	})
	var out execution.BrowseResult
	e.must(http.MethodGet, directoriesPath(id, ""), nil, http.StatusOK, &out)
	if out.Path != "/home/me" || out.Parent != "/home" || len(out.Entries) != 1 || out.Entries[0].Name != "src" || out.Truncated {
		t.Fatalf("default start %+v", out)
	}
	out = execution.BrowseResult{}
	e.must(http.MethodGet, directoriesPath(id, "?path="+url.QueryEscape("/srv/my repo")+"&hidden=true"), nil, http.StatusOK, &out)
	if out.Path != "/srv/my repo" || len(out.Entries) != 2 || browser.last != (execution.BrowseRequest{Path: "/srv/my repo", ShowHidden: true}) {
		t.Fatalf("hidden listing %+v, request %+v", out, browser.last)
	}

	// Bad input never reaches the adapter.
	calls := browser.calls.Load()
	for _, q := range []string{"?path=relative/dir", "?hidden=maybe", "?path=/" + strings.Repeat("a", execution.MaxBrowsePath)} {
		e.must(http.MethodGet, directoriesPath(id, q), nil, http.StatusBadRequest, nil)
	}
	// MCP marks its tool calls: agents type paths.
	var denied errOut
	if w := raw(t, e.h, http.MethodGet, directoriesPath(id, ""), map[string]string{principal.ToolTransportHeader: "mcp"}, nil, &denied); w.Code != http.StatusForbidden || denied.Code != "principal_forbidden" {
		t.Fatalf("MCP: %d %+v", w.Code, denied)
	}
	if browser.calls.Load() != calls {
		t.Fatal("refused requests reached the adapter")
	}

	// T3 declares the operation unavailable with its reason.
	var unavailable errOut
	e.must(http.MethodGet, directoriesPath(t3ID, ""), nil, http.StatusConflict, &unavailable)
	if unavailable.Code != "operation_unavailable" || unavailable.Error != "T3 does not expose folder browsing to Stage; type the path." {
		t.Fatalf("T3: %+v", unavailable)
	}

	for _, c := range []struct {
		fail   error
		status int
		code   string
		msg    string
	}{
		{&execution.Invalid{Reason: "Folder not found on the host: /nope."}, http.StatusBadRequest, "", "Folder not found on the host: /nope."},
		{&execution.BrowseUnavailable{Reason: "No projects yet. Type the path."}, http.StatusConflict, "operation_unavailable", "No projects yet. Type the path."},
		{errors.New("secret upstream detail"), http.StatusBadGateway, "directory_browse_failed", "The harness could not list this folder. Retry or type the path."},
	} {
		browser.set(c.fail, false)
		var got errOut
		e.must(http.MethodGet, directoriesPath(id, "?path=/nope"), nil, c.status, &got)
		if (c.code != "" && got.Code != c.code) || got.Error != c.msg {
			t.Fatalf("%v: %+v", c.fail, got)
		}
	}
	browser.set(nil, true)
	out = execution.BrowseResult{}
	e.must(http.MethodGet, directoriesPath(id, ""), nil, http.StatusOK, &out)
	if len(out.Entries) != execution.MaxDirectoryEntries || !out.Truncated {
		t.Fatalf("bound: %d %v", len(out.Entries), out.Truncated)
	}
}

func TestSharedBrowseDirectoriesThroughWorker(t *testing.T) {
	ctx := t.Context()
	workerClaude := claudetest.New(t)
	var (
		accounts   *auth.Service
		access     *api.Access
		setupToken string
		remoteID   int64
		grant      db.MachineGrant
		rig        *remotetest.Rig
		worker     *browsingAdapter
	)
	now := time.Now()
	e := newEnv(t, withPublicURL, func(d *api.Deps) {
		pair, err := d.Store.CreatePairing(ctx, "Browse worker")
		pair = mustValue(t, pair, err)
		joined, err := d.Store.RedeemPairing(ctx, pair.Code, "Browse worker", "test")
		grant = mustValue(t, joined, err)
		sw, err := d.Store.SwitchInstance(ctx, grant.MachineID, claudecode.Descriptor, workerClaude.ConfigDir(), "Worker", 0)
		remoteID = mustValue(t, sw, err).New
		worker = &browsingAdapter{Adapter: workerClaude.Adapter(remoteID)}
		rig = remotetest.NewWithConnectors(t, grant.MachineID, map[int64]execution.Adapter{remoteID: worker},
			map[string]harness.Connector{claudecode.Kind: streamStatusConnector{}})
		local := machine.NewLocal(db.LocalMachine, harness.Profile{Kind: harness.ProfileServer, LocalProcesses: true}, machine.LocalOps{})
		d.Harnesses = harness.MustRegistry(claudecode.Descriptor, t3.Descriptor)
		d.Engine = engine.NewForMachines(d.Store, d.Harnesses, machine.MustSet(local, rig.Machine()), d.Logger)
		accounts = auth.New(d.Store, notificationOrigin, func() time.Time { return now })
		on := true
		state, link, err := accounts.Start(ctx, &on)
		if err != nil {
			t.Fatal(err)
		}
		setupToken = notificationToken(t, link.URL())
		access = api.NewAccess(accounts, notificationOrigin, state, nil)
		d.Access = access
	})
	shared := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host, r.RemoteAddr = "stage.test", "127.0.0.1:32101"
		e.h.ServeHTTP(w, r)
	})
	notifyRequest(t, e, auth.SignInResult{}, http.MethodPost, "/api/auth/setup",
		fmt.Sprintf(`{"token":%q,"name":"Owner","email":"owner@example.test","password":%q}`, setupToken, ownerPassphrase), http.StatusOK, nil)
	member := addNotificationMember(t, accounts)
	cred, err := accounts.RotateAgent(ctx, member.Token.Reveal(), store.SessionBrowser)
	agent := "Bearer " + principal.AgentTokenPrefix + mustValue(t, cred, err).Token.Reveal()

	p := directoriesPath(remoteID, "?path=/srv/app")
	var out execution.BrowseResult
	notifyRequest(t, e, member, http.MethodGet, p, "", http.StatusOK, &out)
	if out.Path != "/srv/app" || len(out.Entries) != 1 || out.Entries[0] != (execution.DirEntry{Name: "src", Path: "/srv/app/src"}) {
		t.Fatalf("worker listing %+v", out)
	}
	calls := worker.calls.Load()

	deny := func(h http.Handler, path string, headers map[string]string) {
		t.Helper()
		var got errOut
		if w := raw(t, h, http.MethodGet, path, headers, nil, &got); w.Code != http.StatusForbidden || got.Code != "principal_forbidden" {
			t.Fatalf("GET %s %v: %d %+v", path, headers, w.Code, got)
		}
	}
	deny(shared, p, map[string]string{"Authorization": agent})
	headers := workerHeaders(grant.Token)
	headers[workerchan.PrivateGatewayHeader] = workerchan.PrivateGatewayProof(grant.Token)
	deny(shared, api.GatewayPrefix+p, headers)
	localAgent := access.LocalAgent(e.h)
	deny(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = "stage"
		ctx := context.WithValue(r.Context(), http.LocalAddrContextKey, &net.UnixAddr{Name: "stage.sock", Net: "unix"})
		localAgent.ServeHTTP(w, r.WithContext(ctx))
	}), p, nil)
	if worker.calls.Load() != calls {
		t.Fatal("a denied request reached the worker")
	}

	// The adapter's refusal crosses the channel.
	worker.set(&execution.Invalid{Reason: "Folder not found on the host: /srv/app."}, false)
	var refused errOut
	notifyRequest(t, e, member, http.MethodGet, p, "", http.StatusBadRequest, &refused)
	if refused.Error != "Folder not found on the host: /srv/app." {
		t.Fatalf("worker refusal %+v", refused)
	}
	worker.set(&execution.BrowseUnavailable{Reason: "No projects yet. Type the path."}, false)
	notifyRequest(t, e, member, http.MethodGet, p, "", http.StatusConflict, &refused)
	if refused.Code != "operation_unavailable" || refused.Error != "No projects yet. Type the path." {
		t.Fatalf("worker unavailable %+v", refused)
	}

	// An older worker has no handler: the operation says to update it.
	worker.set(nil, false)
	rig.StopWorker()
	rig.SetMissingRequestKinds(remote.KindDirectoryBrowse)
	rig.StartWorker()
	calls = worker.calls.Load()
	var older errOut
	notifyRequest(t, e, member, http.MethodGet, p, "", http.StatusConflict, &older)
	if older.Code != "operation_unavailable" || !strings.Contains(older.Error, "Update Stage on the worker machine") || worker.calls.Load() != calls {
		t.Fatalf("older worker: %+v, calls %d", older, worker.calls.Load()-calls)
	}
}
