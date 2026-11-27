package api_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/auth"
	"github.com/flexdinesh/stage/server/internal/codex"
	"github.com/flexdinesh/stage/server/internal/codex/codextest"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/engine"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/opencode"
	"github.com/flexdinesh/stage/server/internal/opencode/opencodetest"
	"github.com/flexdinesh/stage/server/internal/principal"
	"github.com/flexdinesh/stage/server/internal/remote"
	"github.com/flexdinesh/stage/server/internal/remote/remotetest"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

func TestWorkerRosterSearchAcrossHarnesses(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	const openID = "ses_search_done"
	openCode, withdrawList := openCodeSearchService(t, dir, openID)

	t3Client := t3.NewFake()
	p := t3Client.AddProject("Stage T3 project", dir)
	thread, err := t3Client.LaunchThread(ctx, t3.LaunchInput{ProjectID: p.ID, Title: "Completed T3 thread"})
	t3ID := mustValue(t, thread, err).ThreadID
	if err := t3Client.SetStatus(t3ID, t3.StatusCompleted); err != nil {
		t.Fatal(err)
	}

	codexServer := codextest.New(t)
	codexServer.Trust(dir, "trusted")
	const codexID = "01900000-0000-7000-8000-000000000001"
	const wrapped = "01900000-0000-7000-8000-000000000002"
	codexServer.AddThread(codextest.Thread{ID: codexID, Cwd: dir, Name: "Completed Codex thread", Created: 20, Updated: 30,
		Turns: []*codextest.Turn{{ID: "codex-turn", Status: "completed", Started: 21, Completed: 30}}})
	codexServer.AddThread(codextest.Thread{ID: wrapped, Cwd: dir, Name: "Wrapped Codex thread", Created: 10, Updated: 10, Originator: t3.OriginOriginator})
	codexClient := codex.NewClient(&codextest.Daemon{Srv: codexServer, T: t}, "test")
	t.Cleanup(codexClient.Close)

	var (
		accounts   *auth.Service
		setupToken string
		grant      db.MachineGrant
		rig        *remotetest.Rig
	)
	cases := []struct {
		harness         harness.Descriptor
		nativeID, title string
		workspace       string
		conn            int64
	}{
		{harness: t3.Descriptor, nativeID: t3ID, title: "Completed T3 thread", workspace: p.Title},
		{harness: codex.Descriptor, nativeID: codexID, title: "Completed Codex thread", workspace: dir},
		{harness: opencode.Descriptor, nativeID: openID, title: "Completed OpenCode session", workspace: dir},
	}
	e := newEnv(t, withPublicURL, func(d *api.Deps) {
		pair, err := d.Store.CreatePairing(ctx, "Roster worker")
		pair = mustValue(t, pair, err)
		joined, err := d.Store.RedeemPairing(ctx, pair.Code, "Roster worker", "test")
		grant = mustValue(t, joined, err)
		for i := range cases {
			tc := &cases[i]
			sw, err := d.Store.SwitchInstance(ctx, grant.MachineID, tc.harness, tc.harness.Kind+"-fixture", tc.harness.Name, 0)
			tc.conn = mustValue(t, sw, err).New
		}
		oc, err := opencode.New(cases[2].conn, opencode.Settings{Endpoint: openCode})
		oc = mustValue(t, oc, err)
		rig = remotetest.NewWithConnectors(t, grant.MachineID, map[int64]execution.Adapter{
			cases[0].conn: t3.NewConnectionAdapter(t3Client, cases[0].conn),
			cases[1].conn: codex.New(codexClient, cases[1].conn, ""),
			cases[2].conn: oc,
		}, map[string]harness.Connector{
			t3.Kind: streamStatusConnector{}, codex.Kind: streamStatusConnector{}, opencode.Kind: streamStatusConnector{},
		})
		local := machine.NewLocal(db.LocalMachine, harness.Profile{Kind: harness.ProfileServer}, machine.LocalOps{})
		d.Harnesses = codexHarnesses
		d.Engine = engine.NewForMachines(d.Store, d.Harnesses, machine.MustSet(local, rig.Machine()), d.Logger)
		accounts = auth.New(d.Store, notificationOrigin, nil)
		on := true
		state, link, err := accounts.Start(ctx, &on)
		if err != nil {
			t.Fatal(err)
		}
		setupToken = notificationToken(t, link.URL())
		d.Access = api.NewAccess(accounts, notificationOrigin, state, nil)
	})
	notifyRequest(t, e, auth.SignInResult{}, http.MethodPost, "/api/auth/setup",
		fmt.Sprintf(`{"token":%q,"name":"Owner","email":"owner@example.test","password":%q}`, setupToken, ownerPassphrase), http.StatusOK, nil)
	member := addNotificationMember(t, accounts)
	credential, err := accounts.RotateAgent(ctx, member.Token.Reveal(), store.SessionBrowser)
	bearer := map[string]string{"Authorization": "Bearer " + principal.AgentTokenPrefix + mustValue(t, credential, err).Token.Reveal()}
	gateway := workerHeaders(grant.Token)
	gateway[workerchan.PrivateGatewayHeader] = workerchan.PrivateGatewayProof(grant.Token)
	shared := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host, r.RemoteAddr = "stage.test", "127.0.0.1:32101"
		e.h.ServeHTTP(w, r)
	})
	agentRequest := func(method, path string, headers map[string]string, body, out any, want int) {
		t.Helper()
		w := raw(t, shared, method, path, headers, body, out)
		if w.Code != want {
			t.Fatalf("%s %s: got %d, want %d: %s", method, path, w.Code, want, w.Body)
		}
	}
	for _, tc := range cases {
		path := rosterPath(tc.conn)
		var page searchOutput
		notifyRequest(t, e, member, http.MethodGet, path+"?limit=1", "", http.StatusOK, &page)
		want := execution.SessionRef{ConnectionID: tc.conn, ExternalSessionID: tc.nativeID}
		if len(page.Sessions) != 1 || page.Sessions[0].Session != want || page.Sessions[0].Title != tc.title ||
			page.Sessions[0].Workspace != tc.workspace || !page.Sessions[0].Eligibility.Available || page.Sessions[0].TrackedTaskID != nil {
			t.Fatalf("%s roster: %+v", tc.harness.Name, page)
		}
		if _, err := e.store.Observation(ctx, want); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("%s browse created observation: %v", tc.harness.Name, err)
		}
		agentRequest(http.MethodGet, path, bearer, nil, nil, http.StatusForbidden)
		agentRequest(http.MethodGet, api.GatewayPrefix+path, gateway, nil, nil, http.StatusForbidden)
	}

	codexPath := rosterPath(cases[1].conn)
	var first, second searchOutput
	notifyRequest(t, e, member, http.MethodGet, codexPath+"?limit=1", "", http.StatusOK, &first)
	if first.NextCursor == "" {
		t.Fatal("Codex cursor missing at page boundary")
	}
	notifyRequest(t, e, member, http.MethodGet, codexPath+"?limit=1&cursor="+url.QueryEscape(first.NextCursor), "", http.StatusOK, &second)
	if len(second.Sessions) != 1 || second.Sessions[0].Session.ExternalSessionID != wrapped || second.Sessions[0].Eligibility.Available ||
		!strings.Contains(second.Sessions[0].Eligibility.Reason, "Track it through T3") || second.NextCursor != "" {
		t.Fatalf("wrapped Codex thread: %+v", second)
	}
	agentRequest(http.MethodGet, codexPath+"/"+wrapped+"/context", bearer, nil, nil, http.StatusConflict)

	const stale = "01900000-0000-7000-8000-000000000003"
	codexServer.AddThread(codextest.Thread{ID: stale, Cwd: dir, Name: "Archived after search"})
	var candidate searchOutput
	notifyRequest(t, e, member, http.MethodGet, codexPath+"?q="+stale, "", http.StatusOK, &candidate)
	if len(candidate.Sessions) != 1 || !candidate.Sessions[0].Eligibility.Available {
		t.Fatalf("stale candidate before archive: %+v", candidate)
	}
	codexServer.Archive(stale)
	agentRequest(http.MethodPost, codexPath+"/"+stale+"/adopt", bearer, map[string]any{}, nil, http.StatusConflict)

	codexServer.Unsupported("thread/list")
	openPath := rosterPath(cases[2].conn)
	withdrawList()
	for _, tc := range []struct{ path, reason string }{{codexPath, "Codex"}, {openPath, "OpenCode"}} {
		var why errOut
		notifyRequest(t, e, member, http.MethodGet, tc.path, "", http.StatusConflict, &why)
		if why.Code != "operation_unavailable" || !strings.Contains(why.Error, tc.reason) || !strings.Contains(why.Error, "ID") {
			t.Fatalf("provider fallback: %+v", why)
		}
	}
	codexServer.Supported("thread/list")

	rig.StopWorker()
	rig.SetMissingRequestKinds(remote.KindSessionSearch)
	rig.StartWorker()
	for _, tc := range cases {
		path := rosterPath(tc.conn)
		var why errOut
		notifyRequest(t, e, member, http.MethodGet, path, "", http.StatusConflict, &why)
		if why.Code != "operation_unavailable" || !strings.Contains(why.Error, "Update Stage on the worker") {
			t.Fatalf("%s older worker: %+v", tc.harness.Name, why)
		}
		named := path + "/" + tc.nativeID
		agentRequest(http.MethodGet, named+"/context", bearer, nil, nil, http.StatusOK)
		agentRequest(http.MethodPost, api.GatewayPrefix+named+"/adopt", gateway, map[string]any{"newProjectTitle": tc.title}, nil, http.StatusCreated)
		agentRequest(http.MethodPost, named+"/adopt", bearer, map[string]any{}, nil, http.StatusOK)
	}
}

// The roster contract is optional on an otherwise working OpenCode service.
func openCodeSearchService(t *testing.T, dir, id string) (string, func()) {
	t.Helper()
	base := opencodetest.New(t, "")
	var supported atomic.Bool
	supported.Store(true)
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		session := fmt.Sprintf(`{"id":%q,"projectID":"atlas","title":"Completed OpenCode session","location":{"directory":%q},"time":{"created":10,"updated":30,"busy":20,"idle":30},"outcome":"succeeded"}`, id, dir)
		switch r.URL.Path {
		case "/openapi.json":
			rec := httptest.NewRecorder()
			base.Config.Handler.ServeHTTP(rec, r)
			var spec struct {
				Paths map[string]map[string]json.RawMessage
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if supported.Load() {
				spec.Paths["/api/session"]["get"] = json.RawMessage(`{
					"operationId":"session.list",
					"parameters":[{"in":"query","name":"limit","schema":{"type":"string"}},{"in":"query","name":"cursor","schema":{"type":"string"}}],
					"responses":{"200":{"content":{"application/json":{"schema":{
						"type":"object",
						"properties":{
							"data":{"type":"array"},
							"cursor":{"type":"object","properties":{"next":{"anyOf":[{"type":"string"},{"type":"null"}]}}}
						}
					}}}}}
				}`)
			}
			_ = json.NewEncoder(w).Encode(spec)
		case "/api/session":
			if !supported.Load() {
				http.NotFound(w, r)
				return
			}
			fmt.Fprintf(w, `{"data":[%s],"cursor":{"previous":null,"next":null}}`, session)
		case "/api/session/" + id:
			fmt.Fprintf(w, `{"data":%s}`, session)
		case "/api/session/active":
			fmt.Fprint(w, `{"data":{}}`)
		case "/api/session/" + id + "/permission", "/api/session/" + id + "/form", "/api/session/" + id + "/message":
			fmt.Fprint(w, `{"data":[]}`)
		case "/api/project":
			fmt.Fprintf(w, `[{"id":"atlas","name":"Atlas","canonical":%q,"time":{"created":10,"updated":30}}]`, dir)
		default:
			base.Config.Handler.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(fixture.Close)
	return fixture.URL, func() {
		supported.Store(false)
		base.SetVersion("legacy")
	}
}
