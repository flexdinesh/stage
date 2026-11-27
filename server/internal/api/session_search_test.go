package api_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
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
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest/fakestate"
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

const (
	rosterID  = "12345678-1234-4234-8234-123456789012"
	wrappedID = "87654321-1234-4234-8234-123456789012"
)

type searchOutput struct {
	Sessions []struct {
		Session       execution.SessionRef
		Title         string
		Workspace     string
		Eligibility   execution.Availability
		TrackedTaskID *int64
	}
	NextCursor string
}

func seedRoster(f *claudetest.Fake, title string) {
	f.Update(func(s *fakestate.State) {
		s.Sessions = []fakestate.Session{
			{Entry: fakestate.Entry{SessionID: rosterID, Kind: "background", State: "done", StartedAt: 200, Name: title, Cwd: f.Workspace}},
			{Entry: fakestate.Entry{SessionID: wrappedID, Kind: "interactive", StartedAt: 100, Name: "Wrapped", Cwd: f.Workspace}},
		}
	})
}

func rosterPath(conn int64) string { return fmt.Sprintf("/api/connections/%d/sessions", conn) }

func TestPersonalOperatorCanBrowseLocalSessions(t *testing.T) {
	f := claudetest.New(t)
	seedRoster(f, "Local")
	var id int64
	e := newEnv(t, func(d *api.Deps) {
		sw, err := d.Store.SwitchInstance(t.Context(), db.LocalMachine, claudecode.Descriptor, f.ConfigDir(), "Local Claude", 0)
		id = mustValue(t, sw, err).New
		d.Harnesses = harness.MustRegistry(claudecode.Descriptor, t3.Descriptor)
		d.Engine = engine.NewWithAdapters(d.Store, d.Harnesses, map[int64]execution.Adapter{
			id: f.Adapter(id), d.Store.ActiveID(db.LocalMachine, t3.Kind): t3.NewAdapter(t3.NewFake()),
		}, d.Logger)
	})
	var page searchOutput
	e.must(http.MethodGet, rosterPath(id)+"?q=local", nil, http.StatusOK, &page)
	if len(page.Sessions) != 1 || page.Sessions[0].Title != "Local" || page.Sessions[0].Session.ConnectionID != id {
		t.Fatalf("local page: %+v", page)
	}
	var unsupported errOut
	e.must(http.MethodGet, rosterPath(e.store.ActiveID(db.LocalMachine, t3.Kind)), nil, http.StatusConflict, &unsupported)
	if unsupported.Code != "operation_unavailable" || !strings.Contains(unsupported.Error, "session link or ID") {
		t.Fatalf("unsupported fallback: %+v", unsupported)
	}
}

type countedSearch struct {
	*claudecode.Adapter
	searches, observations, attention atomic.Int64
}

func (a *countedSearch) SearchSessions(ctx context.Context, q execution.SessionSearchQuery) (execution.SessionSearchPage, error) {
	a.searches.Add(1)
	return a.Adapter.SearchSessions(ctx, q)
}

func (a *countedSearch) Observe(ctx context.Context, ref execution.SessionRef) (execution.Observation, error) {
	a.observations.Add(1)
	return a.Adapter.Observe(ctx, ref)
}

func (a *countedSearch) Attention(ctx context.Context) ([]execution.SessionRef, error) {
	a.attention.Add(1)
	return a.Adapter.Attention(ctx)
}

func TestSharedSessionSearchThroughWorker(t *testing.T) {
	ctx := t.Context()
	localClaude, workerClaude := claudetest.New(t), claudetest.New(t)
	seedRoster(localClaude, "Local stage")
	seedRoster(workerClaude, "Worker completed")
	// The transcript may identify the wrapper, but contributes neither
	// messages nor picker fields.
	logPath := fakestate.TranscriptPath(workerClaude.ProjectsDir(), workerClaude.Workspace, wrappedID)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	log := `{"type":"user","entrypoint":"t3-code","sessionId":"` + wrappedID + `","cwd":"/transcript-only","timestamp":"2026-11-27T00:00:00Z","message":{"content":"private prompt"}}` + "\n"
	if err := os.WriteFile(logPath, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}

	var (
		accounts          *auth.Service
		access            *api.Access
		setupToken        string
		offset            atomic.Int64
		localID, remoteID int64
		grant             db.MachineGrant
		rig               *remotetest.Rig
		worker            *countedSearch
	)
	now := time.Now()
	e := newEnv(t, withPublicURL, func(d *api.Deps) {
		pair, err := d.Store.CreatePairing(ctx, "Search worker")
		pair = mustValue(t, pair, err)
		joined, err := d.Store.RedeemPairing(ctx, pair.Code, "Search worker", "test")
		grant = mustValue(t, joined, err)
		sw, err := d.Store.SwitchInstance(ctx, db.LocalMachine, claudecode.Descriptor, localClaude.ConfigDir(), "Local", 0)
		localID = mustValue(t, sw, err).New
		sw, err = d.Store.SwitchInstance(ctx, grant.MachineID, claudecode.Descriptor, workerClaude.ConfigDir(), "Worker", 0)
		remoteID = mustValue(t, sw, err).New
		worker = &countedSearch{Adapter: workerClaude.Adapter(remoteID)}
		rig = remotetest.NewWithConnectors(t, grant.MachineID, map[int64]execution.Adapter{remoteID: worker},
			map[string]harness.Connector{claudecode.Kind: streamStatusConnector{}})
		local := machine.NewLocal(db.LocalMachine, harness.Profile{Kind: harness.ProfileServer, LocalProcesses: true}, machine.LocalOps{})
		local.Install(localID, localID, localClaude.Adapter(localID))
		d.Harnesses = harness.MustRegistry(claudecode.Descriptor, t3.Descriptor)
		d.Engine = engine.NewForMachines(d.Store, d.Harnesses, machine.MustSet(local, rig.Machine()), d.Logger)
		accounts = auth.New(d.Store, notificationOrigin, func() time.Time { return now.Add(time.Duration(offset.Load())) })
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
	remotePath := rosterPath(remoteID)
	deny := func(h http.Handler, path, credential, code string, status int, extra map[string]string) {
		t.Helper()
		headers := map[string]string{"Authorization": credential}
		for k, v := range extra {
			headers[k] = v
		}
		var out errOut
		w := raw(t, h, http.MethodGet, path, headers, nil, &out)
		if w.Code != status || out.Code != code {
			t.Fatalf("GET %s: got %d %+v, want %d %s", path, w.Code, out, status, code)
		}
	}
	deny(shared, remotePath, "", "setup_pending", http.StatusForbidden, nil)
	if worker.searches.Load() != 0 {
		t.Fatal("pending setup reached the adapter")
	}
	notifyRequest(t, e, auth.SignInResult{}, http.MethodPost, "/api/auth/setup",
		fmt.Sprintf(`{"token":%q,"name":"Owner","email":"owner@example.test","password":%q}`, setupToken, ownerPassphrase), http.StatusOK, nil)
	member := addNotificationMember(t, accounts)
	if member.Account.Role != store.RoleMember {
		t.Fatalf("expected ordinary member, got %s", member.Account.Role)
	}
	login, err := accounts.SignIn(ctx, "owner@example.test", ownerPassphrase, netip.MustParseAddr("127.0.0.1"), store.SessionBrowser)
	owner := mustValue(t, login, err)
	cred, err := accounts.RotateAgent(ctx, member.Token.Reveal(), store.SessionBrowser)
	agent := "Bearer " + principal.AgentTokenPrefix + mustValue(t, cred, err).Token.Reveal()

	project, err := e.store.InsertProject(ctx, db.Project{Title: "Local", Bindings: []db.ProjectBinding{
		{ConnectionID: localID, ExternalProjectID: localClaude.Workspace, Workspace: localClaude.Workspace},
	}})
	project = mustValue(t, project, err)
	tk, err := e.store.CreateTask(ctx, db.NewTask{ConnectionID: localID, ProjectID: project.ID, Title: "Prior tracked work", Prompt: "Work"})
	tk = mustValue(t, tk, err)
	ref := db.SessionRef{ConnectionID: localID, ExternalSessionID: rosterID}
	if _, err := e.store.TransitionTask(ctx, db.Transition{TaskID: tk.ID, To: db.StateRunning, Session: &ref}); err != nil {
		t.Fatal(err)
	}

	var page searchOutput
	notifyRequest(t, e, member, http.MethodGet, remotePath+"?limit=1", "", 200, &page)
	if len(page.Sessions) != 1 || page.Sessions[0].Session != (execution.SessionRef{ConnectionID: remoteID, ExternalSessionID: rosterID}) ||
		page.Sessions[0].Title != "Worker completed" || !page.Sessions[0].Eligibility.Available ||
		page.Sessions[0].TrackedTaskID != nil || page.NextCursor == "" {
		t.Fatalf("worker page: %+v", page)
	}
	token := url.QueryEscape(page.NextCursor)
	next := remotePath + "?limit=1&cursor=" + token
	if worker.searches.Load() != 1 {
		t.Fatalf("remote search calls: %d", worker.searches.Load())
	}

	deny(shared, next, "", "authentication_required", http.StatusUnauthorized, nil)
	deny(shared, next, agent, "principal_forbidden", http.StatusForbidden, nil)
	headers := workerHeaders(grant.Token)
	headers[workerchan.PrivateGatewayHeader] = workerchan.PrivateGatewayProof(grant.Token)
	deny(shared, api.GatewayPrefix+next, "Bearer "+grant.Token, "principal_forbidden", http.StatusForbidden, headers)
	localAgent := access.LocalAgent(e.h)
	deny(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = "stage"
		ctx := context.WithValue(r.Context(), http.LocalAddrContextKey, &net.UnixAddr{Name: "stage.sock", Net: "unix"})
		localAgent.ServeHTTP(w, r.WithContext(ctx))
	}), next, "", "principal_forbidden", http.StatusForbidden, nil)
	deny(shared, next, "", "invalid_session_search_cursor", http.StatusBadRequest, map[string]string{"Cookie": "stage_session=" + owner.Token.Reveal()})
	for _, path := range []string{rosterPath(localID) + "?limit=1&cursor=" + token, next + "&q=switch", remotePath + "?cursor=" + token} {
		notifyRequest(t, e, member, http.MethodGet, path, "", http.StatusBadRequest, nil)
	}
	offset.Store(int64(member.Session.ExpiresAt.Sub(now) + time.Second))
	deny(shared, next, "", "authentication_required", http.StatusUnauthorized, map[string]string{"Cookie": "stage_session=" + member.Token.Reveal()})
	offset.Store(0)
	if worker.searches.Load() != 1 {
		t.Fatalf("denied page reached the worker: %d", worker.searches.Load())
	}

	page = searchOutput{}
	notifyRequest(t, e, member, http.MethodGet, next, "", 200, &page)
	if len(page.Sessions) != 1 || page.Sessions[0].Session.ExternalSessionID != wrappedID ||
		page.Sessions[0].Eligibility.Available || !strings.Contains(page.Sessions[0].Eligibility.Reason, "Track it through T3") ||
		page.Sessions[0].Workspace != workerClaude.Workspace || page.NextCursor != "" {
		t.Fatalf("wrapped page: %+v", page)
	}
	w := raw(t, shared, http.MethodGet, remotePath+"?q=Wrapped", map[string]string{"Cookie": "stage_session=" + member.Token.Reveal()}, nil, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "private prompt") || strings.Contains(w.Body.String(), "\"origin\"") ||
		strings.Contains(w.Body.String(), "/transcript-only") {
		t.Fatalf("non-roster data in search: %d %s", w.Code, w.Body)
	}
	page = searchOutput{}
	notifyRequest(t, e, member, http.MethodGet, rosterPath(localID)+"?q=stage", "", 200, &page)
	if len(page.Sessions) != 1 || page.Sessions[0].Session.ConnectionID != localID ||
		page.Sessions[0].TrackedTaskID == nil || *page.Sessions[0].TrackedTaskID != tk.ID {
		t.Fatalf("local page with duplicate native ID: %+v", page)
	}

	if worker.observations.Load() != 0 || worker.attention.Load() != 0 {
		t.Fatal("search queried Observe or Attention")
	}
	for _, id := range []string{rosterID, wrappedID} {
		ref := db.SessionRef{ConnectionID: remoteID, ExternalSessionID: id}
		if _, err := e.store.TaskBySession(ctx, ref); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("search tracked %s: %v", id, err)
		}
		if _, err := e.store.Observation(ctx, ref); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("search observed %s: %v", id, err)
		}
	}
	for _, check := range []struct{ method, suffix string }{{http.MethodGet, "/context"}, {http.MethodPost, "/adopt"}} {
		w := raw(t, shared, check.method, rosterPath(localID)+"/"+rosterID+check.suffix,
			map[string]string{"Authorization": agent}, map[string]any{}, nil)
		if w.Code != 200 {
			t.Fatalf("named agent request %s: %d %s", check.suffix, w.Code, w.Body)
		}
	}

	rig.StopWorker()
	var unavailable errOut
	notifyRequest(t, e, member, http.MethodGet, remotePath, "", http.StatusServiceUnavailable, &unavailable)
	if unavailable.Code != "session_search_unavailable" || !strings.Contains(unavailable.Error, "session link or ID") {
		t.Fatalf("offline worker: %+v", unavailable)
	}
	before := worker.searches.Load()
	rig.SetMissingRequestKinds(remote.KindSessionSearch)
	rig.StartWorker()
	// An older worker has neither the RPC nor an operation declaration.
	rig.ReplaceResult(remote.KindFeatures, []byte(`{"operations":{"adopt":{"available":true}}}`))
	unavailable = errOut{}
	notifyRequest(t, e, member, http.MethodGet, remotePath, "", http.StatusConflict, &unavailable)
	if unavailable.Code != "operation_unavailable" || !strings.Contains(unavailable.Error, "Update Stage on the worker") || worker.searches.Load() != before {
		t.Fatalf("older worker: %+v, search calls: %d", unavailable, worker.searches.Load())
	}
}
