package engine_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/opencode"
	"github.com/flexdinesh/stage/server/internal/opencode/opencodetest"
)

func TestContractOpenCodeSessionSurface(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		ctx := t.Context()
		fake := opencodetest.New(t, "")
		ref := execution.SessionRef{ConnectionID: 3, ExternalSessionID: "ses_fixture"}
		snapshot := opencodetest.Session{
			Info: map[string]any{
				"id": ref.ExternalSessionID, "projectID": "proj", "location": map[string]string{"directory": "/workspace"},
				"time":  map[string]int64{"created": 1000, "updated": 3000, "busy": 2000},
				"agent": "build", "model": map[string]string{"providerID": "fixture", "id": "test", "variant": "high"},
				"tokens": map[string]int{"input": 25, "output": 12}, "cost": 0.5,
			},
			Running: true,
			Permissions: []map[string]any{{
				"id": "per_bash", "sessionID": ref.ExternalSessionID, "action": "bash",
				"resources": []string{"go test"}, "save": []string{"go test *"},
			}},
			Forms: []map[string]any{{
				"id": "frm_confirm", "sessionID": ref.ExternalSessionID, "title": "Confirm form",
				"fields": []map[string]any{{"key": "confirm", "type": "boolean", "title": "Continue?"}},
			}},
			Messages: []map[string]any{{"id": "msg_fixture", "type": "user", "text": "Verify", "time": map[string]int64{"created": 2000}}},
			Diff:     []map[string]any{{"file": "work.go", "additions": 1, "deletions": 0, "status": "modified"}},
		}
		fake.SetSession(ref.ExternalSessionID, snapshot)
		a, err := opencode.New(ref.ConnectionID, opencode.Settings{Endpoint: fake.URL})
		if err != nil {
			t.Fatal(err)
		}
		pool := testDB.Reset(t)
		store := db.NewStore(pool)
		if _, err := pool.ExecContext(ctx, "INSERT INTO harness_connections(id,harness,machine_id) VALUES($1,$2,$3)", ref.ConnectionID, opencode.Kind, p.machine(t, pool)); err != nil {
			t.Fatal(err)
		}
		if _, err := store.InsertProject(ctx, db.Project{Title: "Work", Bindings: []db.ProjectBinding{{
			ConnectionID: ref.ConnectionID, ExternalProjectID: "proj", Workspace: "/workspace",
		}}}); err != nil {
			t.Fatal(err)
		}
		obs, err := a.Observe(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutObservation(ctx, obs); err != nil {
			t.Fatal(err)
		}
		task, created, err := store.AdoptSession(ctx, db.AdoptInput{Session: ref})
		if err != nil || !created || task.State != db.StateNeedsInput {
			t.Fatalf("adopted task %+v: %v", task, err)
		}
		reg := harness.MustRegistry(opencode.Descriptor)
		svc := p.engine(t, store, reg, map[int64]execution.Adapter{ref.ConnectionID: a}, nil)
		router := api.NewRouter(api.Deps{Store: store, Engine: svc, Harnesses: reg})
		check := func(method, route, body string, self bool, status int, want string) {
			t.Helper()
			req := httptest.NewRequest(method, fmt.Sprintf("/api/tasks/%d/%s", task.ID, route), strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			if self {
				req.Header.Set(agentctx.CallerHeader, fmt.Sprintf("%d:%s", ref.ConnectionID, ref.ExternalSessionID))
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != status || !strings.Contains(rec.Body.String(), want) {
				t.Fatalf("%s %s: HTTP %d %s", method, route, rec.Code, rec.Body)
			}
		}
		check(http.MethodGet, "requests", "", true, 200, "Always allow go test *")
		check(http.MethodPost, "requests/per_bash", `{"option":"always"}`, true, 403, "self_action_forbidden")
		check(http.MethodPost, "requests/per_bash", `{"option":"always"}`, false, 200, "")
		check(http.MethodPost, "requests/frm_confirm", `{"answers":{"confirm":["Yes"]}}`, false, 200, "")
		check(http.MethodPost, "requests/per_bash", `{"option":"once"}`, false, 404, "")
		check(http.MethodPost, "messages", `{"text":"work","delivery":"send"}`, false, 409, "Steer or queue instead.")
		check(http.MethodPost, "messages", `{"text":"work","delivery":"steer"}`, true, 403, "self_action_forbidden")
		check(http.MethodPost, "messages", `{"text":"work","delivery":"steer"}`, false, 200, "")
		check(http.MethodPost, "messages", `{"text":"work","delivery":"queue"}`, false, 200, "")
		check(http.MethodGet, "activity?limit=1", "", true, 200, `"path":"work.go"`)
		check(http.MethodGet, "activity", "", false, 200, `"variant":"high"`)

		snapshot.Permissions, snapshot.Forms, snapshot.Running = nil, nil, false
		snapshot.Info["time"] = map[string]int64{"created": 1000, "updated": 4000, "busy": 2000, "idle": 4000}
		snapshot.Info["outcome"] = "succeeded"
		fake.SetSession(ref.ExternalSessionID, snapshot)
		check(http.MethodPost, "messages", `{"text":"work","delivery":"queue"}`, false, 409, "send instead.")
		check(http.MethodPost, "messages", `{"text":"work"}`, false, 200, "")
		calls := fake.Interactions()
		if len(calls) != 5 || calls[0].Body["decision"] != "always" || fmt.Sprint(calls[1].Body["answer"]) != "map[confirm:true]" ||
			calls[2].Body["delivery"] != "steer" || calls[3].Body["delivery"] != "queue" || calls[4].Body["delivery"] != nil {
			t.Fatalf("OpenCode interactions %+v", calls)
		}

		fake.WithoutDiff()
		fake.WithoutInteraction()
		fake.SetVersion("older")
		check(http.MethodGet, "activity", "", false, 409, "Update OpenCode")
		check(http.MethodGet, "requests", "", false, 409, "Update OpenCode")
		for _, mode := range []string{"send", "steer", "queue"} {
			check(http.MethodPost, "messages", fmt.Sprintf(`{"text":"work","delivery":%q}`, mode), false, 409, "Update OpenCode")
		}
	})
}
