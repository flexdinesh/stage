package opencode

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/opencode/opencodetest"
)

// sessionUpdate follows the v2.3.2 OpenAPI session.update operation.
const sessionUpdate = `{
	"operationId": "session.update",
	"requestBody": {"required": true, "content": {"application/json": {"schema": {
		"type": "object", "additionalProperties": false,
		"properties": {"title": {"anyOf": [{"type": "string"}, {"type": "null"}]}, "archived": {"anyOf": [{"type": "boolean"}, {"type": "null"}]}}
	}}}},
	"responses": {"204": {"description": "<No Content>"}}
}`

// renameFixture serves the fake contract with patch as session.update ("": none).
func renameFixture(t *testing.T, patch, components string, handle http.HandlerFunc) *Adapter {
	t.Helper()
	fake := opencodetest.New(t, "")
	base := fake.Config.Handler
	rec := httptest.NewRecorder()
	base.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	var spec struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components json.RawMessage                       `json:"components,omitempty"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	if patch != "" {
		spec.Paths["/api/session/{sessionID}"]["patch"] = json.RawMessage(patch)
	}
	if components != "" {
		spec.Components = json.RawMessage(components)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/info":
			base.ServeHTTP(w, r)
		case "/openapi.json":
			if err := json.NewEncoder(w).Encode(spec); err != nil {
				t.Errorf("write OpenAPI: %v", err)
			}
		default:
			handle(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	a, err := New(17, Settings{Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestRenameSessionPatchesTheTitle(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	a := renameFixture(t, sessionUpdate, "", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/api/session/ses_live" {
			http.NotFound(w, r)
			return
		}
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	c, err := a.Features(t.Context())
	if err != nil || !c.Operations[execution.OpSessionRename].Available || c.DeclaresOperations() != nil {
		t.Fatalf("rename declaration: %+v, %v", c.Operations[execution.OpSessionRename], err)
	}
	ref := execution.SessionRef{ConnectionID: 17, ExternalSessionID: "ses_live"}
	if err := a.RenameSession(t.Context(), ref, "  Renamed task "); err != nil {
		t.Fatal(err)
	}
	if err := a.RenameSession(t.Context(), execution.SessionRef{ConnectionID: 17, ExternalSessionID: "ses_gone"}, "x"); err == nil {
		t.Fatal("a missing session renamed")
	}
	if err := a.RenameSession(t.Context(), execution.SessionRef{ConnectionID: 18, ExternalSessionID: "ses_live"}, "x"); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("another connection's session: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 || bodies[0] != `{"title":"Renamed task"}` {
		t.Fatalf("PATCH bodies %q", bodies)
	}
}

func TestRenameSessionFollowsTheContract(t *testing.T) {
	ref := execution.SessionRef{ConnectionID: 17, ExternalSessionID: "ses_live"}
	referenced := `{"operationId":"session.update","requestBody":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/SessionUpdate"}}}}}`
	for name, c := range map[string]struct {
		patch, components string
		available         bool
	}{
		"inline title":     {sessionUpdate, "", true},
		"referenced title": {referenced, `{"schemas":{"SessionUpdate":{"type":"object","properties":{"title":{"type":"string"}}}}}`, true},
		"no operation":     {"", "", false},
		"no title":         {`{"operationId":"session.update","requestBody":{"content":{"application/json":{"schema":{"properties":{"archived":{}}}}}}}`, "", false},
		"another id":       {`{"operationId":"session.patch","requestBody":{"content":{"application/json":{"schema":{"properties":{"title":{}}}}}}}`, "", false},
	} {
		t.Run(name, func(t *testing.T) {
			patched := false
			a := renameFixture(t, c.patch, c.components, func(w http.ResponseWriter, r *http.Request) {
				patched = r.Method == http.MethodPatch
				w.WriteHeader(http.StatusNoContent)
			})
			caps, err := a.Features(t.Context())
			op := caps.Operations[execution.OpSessionRename]
			if err != nil || op.Available != c.available || (!c.available && op.Reason != sessionRenameUnavailable) {
				t.Fatalf("rename %+v, %v", op, err)
			}
			err = a.RenameSession(t.Context(), ref, "x")
			if c.available != (err == nil) || patched != c.available {
				t.Fatalf("rename call: %v, patched %v", err, patched)
			}
		})
	}
}
