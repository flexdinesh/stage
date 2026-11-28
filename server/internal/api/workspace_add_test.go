package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/auth"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/engine"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/principal"
	"github.com/flexdinesh/stage/server/internal/remote/remotetest"
	"github.com/flexdinesh/stage/server/internal/t3"
)

type addedWorkspace struct {
	Workspace struct {
		db.Destination
		Path string `json:"path"`
	} `json:"workspace"`
	Group    db.MergeSource   `json:"group"`
	Existing bool             `json:"existing"`
	Message  string           `json:"message"`
	Facts    []execution.Fact `json:"facts"`
}

type addError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// workspaceDeclarationsOnly declares the kit's sources without AddWorkspace.
type workspaceDeclarationsOnly struct{ execution.Adapter }

func addBody(source, path, request string) map[string]any {
	return map[string]any{"sourceId": source, "input": map[string]string{"path": path}, "requestId": request}
}

func TestWorkspaceAddLocalAndWorker(t *testing.T) {
	for _, placement := range []string{"local", "worker"} {
		t.Run(placement, func(t *testing.T) {
			ctx := t.Context()
			kit := &workspaceKit{}
			withoutAdder := hostedKit
			withoutAdder.Kind, withoutAdder.Name = "declarations-only", "Declarations only"
			reg := harness.MustRegistry(t3.Descriptor, hostedKit, withoutAdder)
			var conn, noAdder int64
			e := newEnv(t, func(d *api.Deps) {
				machineID := db.LocalMachine
				if placement == "worker" {
					machineID = 2
					if _, err := testDB.Pool.ExecContext(ctx, `INSERT INTO machines(id,label) VALUES(2,'Worker')`); err != nil {
						t.Fatal(err)
					}
				}
				add := func(desc harness.Descriptor) int64 {
					t.Helper()
					sw, err := d.Store.SwitchInstance(ctx, machineID, desc, "new", desc.Name, 0)
					if err != nil {
						t.Fatal(err)
					}
					return sw.New
				}
				conn, noAdder = add(hostedKit), add(withoutAdder)
				adapters := map[int64]execution.Adapter{conn: kit, noAdder: workspaceDeclarationsOnly{kit}}
				connectors := map[string]harness.Connector{
					hostedKit.Kind:    streamStatusConnector{summary: harness.Summary{ConnectionID: conn}},
					withoutAdder.Kind: streamStatusConnector{summary: harness.Summary{ConnectionID: noAdder}},
				}
				var set *machine.Set
				if placement == "worker" {
					set = remotetest.NewWithConnectors(t, machineID, adapters, connectors).Set()
				} else {
					local := machine.NewLocal(machineID, harness.Profile{LocalProcesses: true}, nil)
					for id, adapter := range adapters {
						local.Install(id, id, adapter)
					}
					for kind, connector := range connectors {
						local.SetConnector(kind, connector)
					}
					set = machine.MustSet(local)
				}
				d.Engine, d.Harnesses = engine.NewForMachines(d.Store, reg, set, slog.Default()), reg
				d.LiveConnectors = func() harness.Connectors {
					out, err := set.Connectors(reg)
					if err != nil {
						t.Fatal(err)
					}
					return out
				}
			})
			route := fmt.Sprintf("/api/connections/%d/workspaces", conn)

			var caps execution.Capabilities
			e.must("GET", fmt.Sprintf("/api/connections/%d/capabilities", conn), nil, 200, &caps)
			if !caps.Operations[execution.OpWorkspaceAdd].Available || len(caps.WorkspaceSources) != 3 || caps.WorkspaceSources[1].Confirm != kitTrustConfirm {
				t.Fatalf("kit capabilities %+v %+v", caps.Operations, caps.WorkspaceSources)
			}
			e.must("GET", fmt.Sprintf("/api/connections/%d/capabilities", noAdder), nil, 200, &caps)
			wantReason := "This connection does not support adding workspaces."
			if placement == "worker" {
				wantReason = "This worker connection does not offer adding workspaces."
			}
			if op := caps.Operations[execution.OpWorkspaceAdd]; op.Available || op.Reason != wantReason {
				t.Fatalf("declarations-only operation %+v", op)
			}

			// A person adds an existing folder.
			var added addedWorkspace
			e.must("POST", route, addBody("folder", "/work/new", "req-new"), 201, &added)
			ws := added.Workspace
			if added.Existing || ws.Path != "/work/new" || ws.Title != "new" || ws.ConnectionID != conn ||
				ws.WorkspaceID != db.WorkspaceID(conn, "kit:/work/new", "/work/new") || !ws.Available ||
				added.Group.ID == 0 || ws.GroupProjectID == nil || *ws.GroupProjectID != added.Group.ID ||
				added.Message != "Hosted kit added /work/new" || len(added.Facts) != 1 {
				t.Fatalf("human add %+v", added)
			}
			if got := kit.addRequests(); len(got) != 1 || got[0].Source != "folder" || got[0].RequestID != "req-new" || got[0].Confirmed {
				t.Fatalf("relayed requests %+v", got)
			}
			// The same target again is idempotent in the harness.
			var again addedWorkspace
			e.must("POST", route, addBody("folder", "/work/new", "req-new"), 201, &again)
			if !again.Existing || again.Group.ID != added.Group.ID || again.Workspace.WorkspaceID != ws.WorkspaceID {
				t.Fatalf("repeat add %+v", again)
			}
			var events struct {
				Events []db.ProjectEvent `json:"events"`
			}
			e.must("GET", fmt.Sprintf("/api/projects/%d/events", added.Group.ID), nil, 200, &events)
			if len(events.Events) != 2 || events.Events[0].Type != "workspace_added" ||
				!strings.Contains(events.Events[0].Text, "already") || !strings.HasPrefix(events.Events[1].Text, "Added workspace new on Hosted kit (Existing folder)") ||
				events.Events[1].Actor.Kind != "personal" || events.Events[1].Cause == nil ||
				events.Events[1].Cause.Kind != "workspace_add" || events.Events[1].Cause.RequestID != "req-new" {
				t.Fatalf("events %+v", events.Events)
			}

			// An agent gets the destination projection without paths.
			caller := fmt.Sprintf("%d:agent-session", conn)
			rec := raw(t, e.h, "POST", route, map[string]string{principal.ToolTransportHeader: "mcp", agentctx.CallerHeader: caller},
				addBody("folder", "/work/agent", "req-agent"), nil)
			if rec.Code != 201 || strings.Contains(rec.Body.String(), "/work/agent") ||
				strings.Contains(rec.Body.String(), `"path"`) || strings.Contains(rec.Body.String(), `"message"`) || strings.Contains(rec.Body.String(), `"facts"`) {
				t.Fatalf("agent add %d %s", rec.Code, rec.Body)
			}
			var agentAdded addedWorkspace
			if err := json.Unmarshal(rec.Body.Bytes(), &agentAdded); err != nil || agentAdded.Workspace.Title != "agent" || agentAdded.Group.ID == 0 {
				t.Fatalf("agent result %+v %v", agentAdded, err)
			}
			e.must("GET", fmt.Sprintf("/api/projects/%d/events", agentAdded.Group.ID), nil, 200, &events)
			if len(events.Events) != 1 || events.Events[0].Cause == nil || events.Events[0].Cause.Session == nil ||
				events.Events[0].Cause.Session.ExternalSessionID != "agent-session" {
				t.Fatalf("agent event %+v", events.Events)
			}

			// Refusals before anything reaches the harness.
			before := len(kit.addRequests())
			for _, tc := range []struct {
				body   map[string]any
				status int
				code   string
				want   string
			}{
				{map[string]any{"sourceId": "folder", "input": map[string]string{"path": "/x"}}, 400, "", "requestId is required"},
				{map[string]any{"sourceId": "folder", "input": map[string]string{"path": "/x"}, "requestId": strings.Repeat("r", 257)}, 400, "", "at most 256"},
				{addBody("nope", "/x", "r1"), 404, "", `unknown workspace source "nope"`},
				{map[string]any{"sourceId": "clone", "input": map[string]string{"url": "https://x.test/r"}, "requestId": "r2"}, 409, "operation_unavailable", "Sign in to clone."},
				{addBody("folder", "", "r3"), 400, "", `Invalid value for workspace source field "path".`},
				{map[string]any{"sourceId": "folder", "input": map[string]string{"other": "x"}, "requestId": "r4"}, 400, "", "unknown field"},
				{addBody("trusted", "/work/trusted", "r5"), 400, "", kitTrustConfirm},
				{map[string]any{"sourceId": "folder", "input": map[string]string{"path": "/x"}, "requestId": "r6", "projectId": 987654}, 404, "", ""},
			} {
				var out addError
				if got := e.do("POST", route, tc.body, &out); got != tc.status || out.Code != tc.code || !strings.Contains(out.Error, tc.want) {
					t.Errorf("%v: %d %+v, want %d %q %q", tc.body, got, out, tc.status, tc.code, tc.want)
				}
			}
			if got := len(kit.addRequests()); got != before {
				t.Fatalf("refused requests reached the harness: %d -> %d", before, got)
			}
			var out addError
			if got := e.do("POST", fmt.Sprintf("/api/connections/%d/workspaces", noAdder), addBody("folder", "/x", "r7"), &out); got != 409 || out.Code != "operation_unavailable" {
				t.Fatalf("declarations-only add %d %+v", got, out)
			}

			// The harness refuses input it cannot use (nothing changed).
			if got := e.do("POST", route, addBody("folder", "relative", "r8"), &out); got != 400 || out.Error != "Hosted kit needs an absolute folder path." {
				t.Fatalf("harness refusal %d %+v", got, out)
			}
			// A confirmed source runs.
			trusted := addBody("trusted", "/work/trusted", "r9")
			trusted["confirmed"] = true
			e.must("POST", route, trusted, 201, &added)
			if got := kit.addRequests(); !got[len(got)-1].Confirmed || added.Workspace.Path != "/work/trusted" {
				t.Fatalf("confirmed add %+v %+v", got[len(got)-1], added)
			}

			// projectId joins the named group through the membership edit.
			var target db.Project
			e.must("POST", "/api/projects", map[string]string{"title": "Target"}, 201, &target)
			grouped := addBody("folder", "/work/grouped", "r10")
			grouped["projectId"] = target.ID
			e.must("POST", route, grouped, 201, &added)
			if added.Group.ID != target.ID || added.Group.Title != "Target" || *added.Workspace.GroupProjectID != target.ID {
				t.Fatalf("grouped add %+v", added)
			}
			group, err := e.store.GetProjectGroup(ctx, target.ID)
			if err != nil || !slices.ContainsFunc(group.Members, func(m db.GroupWorkspace) bool { return m.WorkspaceID == added.Workspace.WorkspaceID }) {
				t.Fatalf("target group %+v %v", group, err)
			}
			// Again with the same project: already a member, no edit.
			e.must("POST", route, grouped, 201, &added)
			if !added.Existing || added.Group.ID != target.ID {
				t.Fatalf("grouped repeat %+v", added)
			}

			// The harness answers but does not list the workspace.
			kit.unlisted.Store(true)
			if got := e.do("POST", route, addBody("folder", "/work/hidden", "r11"), &out); got != 502 || out.Code != "workspace_not_listed" || !strings.Contains(out.Error, "Hosted kit does not list") {
				t.Fatalf("not listed %d %+v", got, out)
			}
			kit.unlisted.Store(false)
			kit.uncertain.Store(true)
			if got := e.do("POST", route, addBody("folder", "/work/lost", "r12"), &out); got != 504 || out.Code != "outcome_uncertain" || !strings.Contains(out.Error, "same requestId") {
				t.Fatalf("uncertain %d %+v", got, out)
			}
			kit.uncertain.Store(false)
		})
	}
}

// noWorkspaceAdd is T3 declaring workspaceAdd unsupported.
type noWorkspaceAdd struct{ *t3.Adapter }

func (a noWorkspaceAdd) Features(ctx context.Context) (execution.Capabilities, error) {
	c, err := a.Adapter.Features(ctx)
	if err == nil {
		c.Operations[execution.OpWorkspaceAdd], c.WorkspaceSources = execution.WorkspaceAddNotSupported(t3.Descriptor.Name), nil
	}
	return c, err
}

func TestWorkspaceAddUnsupportedHarness(t *testing.T) {
	e := newEnvWrapped(t, func(a *t3.Adapter) execution.Adapter { return noWorkspaceAdd{a} })
	var caps execution.Capabilities
	e.must("GET", "/api/connections/1/capabilities", nil, 200, &caps)
	if op := caps.Operations[execution.OpWorkspaceAdd]; op.Available || op.Reason != "Adding workspaces from Stage is not supported for T3 yet." {
		t.Fatalf("T3 workspace add %+v", op)
	}
	var out addError
	if got := e.do("POST", "/api/connections/1/workspaces", addBody("folder", "/x", "r"), &out); got != 409 || out.Code != "operation_unavailable" || out.Error != caps.Operations[execution.OpWorkspaceAdd].Reason {
		t.Fatalf("T3 add %d %+v", got, out)
	}
}

func TestWorkspaceAddSharedAccess(t *testing.T) {
	ctx := t.Context()
	kit := &workspaceKit{}
	var accounts *auth.Service
	var pending, claimed http.Handler
	var conn int64
	newEnv(t, func(d *api.Deps) {
		sw, err := d.Store.SwitchInstance(ctx, db.LocalMachine, hostedKit, "new", hostedKit.Name, 0)
		if err != nil {
			t.Fatal(err)
		}
		conn = sw.New
		d.Engine = engine.NewWithAdapters(d.Store, hostedHarnesses, map[int64]execution.Adapter{conn: kit}, d.Logger)
		d.Harnesses = hostedHarnesses
		accounts = auth.New(d.Store, notificationOrigin, nil)
		on := true
		state, link, err := accounts.Start(ctx, &on)
		if err != nil {
			t.Fatal(err)
		}
		blocked := *d
		blocked.Access = api.NewAccess(accounts, notificationOrigin, state, nil)
		pending = api.NewRouter(blocked)
		if _, err := accounts.ClaimSetup(ctx, notificationToken(t, link.URL()), "owner@example.test", "Owner", ownerPassphrase); err != nil {
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
	route := fmt.Sprintf("/api/connections/%d/workspaces", conn)
	if rec := raw(t, pending, "POST", route, nil, addBody("folder", "/work/pending", "p1"), nil); rec.Code != 403 || !strings.Contains(rec.Body.String(), "setup_pending") {
		t.Fatalf("pending add %d %s", rec.Code, rec.Body)
	}
	if len(kit.addRequests()) != 0 {
		t.Fatal("pending add reached the harness")
	}
	member := addNotificationMember(t, accounts)
	agent, err := accounts.RotateAgent(ctx, member.Token.Reveal(), store.SessionBrowser)
	agent = mustValue(t, agent, err)
	post := func(token string, want int) string {
		t.Helper()
		body, err := json.Marshal(addBody("folder", "/work/shared", "s1"))
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("POST", notificationOrigin+route, strings.NewReader(string(body)))
		req.RemoteAddr = "127.0.0.1:32101"
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		claimed.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("POST %s: %d %s", route, rec.Code, rec.Body)
		}
		return rec.Body.String()
	}
	post("", 401)
	body := post(principal.AgentTokenPrefix+agent.Token.Reveal(), 201)
	if strings.Contains(body, "/work/shared") || !strings.Contains(body, `"title":"shared"`) {
		t.Fatalf("agent projection %s", body)
	}
}
