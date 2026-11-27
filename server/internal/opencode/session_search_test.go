package opencode

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/opencode/opencodetest"
)

// The optional operation and its component references follow the v2.3.2
// OpenAPI. Keep the fake service's required contract independent of it.
const rosterOperation = `{
	"operationId": "session.list",
	"parameters": [
		{"name":"limit", "in":"query", "schema":{"anyOf":[{"type":"string"}]}},
		{"name":"order", "in":"query", "schema":{"anyOf":[{"type":"string","enum":["asc","desc"]}]}},
		{"name":"parentID", "in":"query", "schema":{"anyOf":[{"type":"string","enum":["null"]},{"type":"string"}]}},
		{"name":"archived", "in":"query", "schema":{"type":"string","enum":["true","false"]}},
		{"name":"cursor", "in":"query", "schema":{"$ref":"#/components/schemas/SessionsCursor"}}
	],
	"responses":{
		"200":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/SessionsResponse"}}}}
	}
}`

const rosterComponents = `{
	"schemas":{
		"SessionsCursor":{"type":"string"},
		"SessionsResponse":{
			"type":"object",
			"properties":{
				"data":{"type":"array","items":{"type":"object"}},
				"cursor":{"type":"object","properties":{
					"previous":{"anyOf":[{"$ref":"#/components/schemas/SessionsCursor"},{"type":"null"}]},
					"next":{"anyOf":[{"$ref":"#/components/schemas/SessionsCursor"},{"type":"null"}]}
				}}
			}
		}
	}
}`

func sessionSearchFixture(t *testing.T, op, components string, handle http.HandlerFunc) *Adapter {
	t.Helper()
	fake := opencodetest.New(t, "")
	base := fake.Config.Handler
	rec := httptest.NewRecorder()
	base.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	var spec struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components json.RawMessage                       `json:"components"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	if op != "" {
		spec.Paths["/api/session"]["get"] = json.RawMessage(op)
	}
	spec.Components = json.RawMessage(components)
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

func TestSessionSearchUsesOneBoundedRosterPage(t *testing.T) {
	const cursor = "opaque+token/= with?&punctuation"
	var calls atomic.Int32
	a := sessionSearchFixture(t, rosterOperation, rosterComponents, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/session" {
			t.Errorf("unexpected roster dependency %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		want := url.Values{"limit": {"5"}, "archived": {"false"}, "parentID": {"null"}}
		switch r.URL.Query().Get("cursor") {
		case "":
			want.Set("order", "desc")
			fmt.Fprint(w, `{"data":[
				{"id":"ses_completed","title":"Completed Work","location":{"directory":"/Workspace/Release"},"time":{"created":10,"updated":20},"outcome":"succeeded"},
				{"id":"ses_other","title":"Routine","location":{"directory":"/Workspace/Main"},"time":{"created":50}},
				{"id":"ses_archive","title":"Archived Work","location":{"directory":"/Workspace/Main"},"time":{"created":30,"archived":200}},
				{"id":"ses_child","title":"Child Work","location":{"directory":"/Workspace/Main"},"time":{"created":20},"parentID":"ses_other"}
			],"cursor":{"previous":null,"next":"`+cursor+`"}}`)
		case cursor:
			want.Set("cursor", cursor)
			fmt.Fprint(w, `{"data":[{"id":"ses_later","title":"Found","location":{"directory":"/repo"}}],"cursor":{"previous":"ignored","next":null}}`)
		default:
			t.Errorf("unexpected cursor query %v", r.URL.Query())
		}
		if !reflect.DeepEqual(r.URL.Query(), want) {
			t.Errorf("roster parameters %v, want %v", r.URL.Query(), want)
		}
	})
	search := func(query, next string) execution.SessionSearchPage {
		t.Helper()
		before := calls.Load()
		page, err := a.SearchSessions(t.Context(), execution.SessionSearchQuery{Query: query, Limit: 5, Cursor: next})
		if err != nil {
			t.Fatal(err)
		}
		if count := calls.Load() - before; count != 1 {
			t.Fatalf("read %d roster pages", count)
		}
		return page
	}
	features, err := a.Features(t.Context())
	if err != nil || !features.Operations[execution.OpSessionSearch].Available || calls.Load() != 0 {
		t.Fatalf("search capability %+v, roster calls %d, error %v", features, calls.Load(), err)
	}
	page := search("", "")
	ids := func(page execution.SessionSearchPage) []string {
		out := make([]string, 0, len(page.Sessions))
		for _, item := range page.Sessions {
			if item.Session.ConnectionID != 17 {
				t.Errorf("unscoped session %+v", item.Session)
			}
			out = append(out, item.Session.ExternalSessionID)
		}
		return out
	}
	if got := ids(page); !slices.Equal(got, []string{"ses_completed", "ses_other", "ses_archive", "ses_child"}) || page.NextCursor != cursor {
		t.Fatalf("page order or cursor %+v", page)
	}
	completed := page.Sessions[0]
	if !completed.Eligibility.Available || completed.Title != "Completed Work" || completed.Workspace != "/Workspace/Release" ||
		completed.StartedAt == nil || !completed.StartedAt.Equal(time.UnixMilli(10)) {
		t.Fatalf("completed session metadata %+v", completed)
	}
	for _, item := range page.Sessions[2:] {
		if item.Eligibility.Available || item.Eligibility.Reason == "" {
			t.Errorf("eligible archived or child session %+v", item)
		}
	}
	for _, query := range []string{"  cOmPlEtEd wOrK ", "/workspace/RELEASE", "SES_COMPLETED"} {
		got := search(query, "")
		if !slices.Equal(ids(got), []string{"ses_completed"}) || got.NextCursor != cursor {
			t.Errorf("%q: %+v", query, got)
		}
	}
	empty := search("found", "")
	if empty.Sessions == nil || len(empty.Sessions) != 0 || empty.NextCursor != cursor {
		t.Fatalf("empty filtered page %+v", empty)
	}
	last := search("found", empty.NextCursor)
	if !slices.Equal(ids(last), []string{"ses_later"}) || last.NextCursor != "" {
		t.Fatalf("continuation %+v", last)
	}
}

func TestSessionSearchValidatesBoundsAndNativeFailures(t *testing.T) {
	const one = `{"id":"ses_one","location":{"directory":"/repo"}}`
	for _, tc := range []struct {
		name, body, cursor string
		status, limit      int
		err                bool
	}{
		{name: "empty with null cursor", body: `{"data":[],"cursor":{"next":null,"previous":null}}`},
		{name: "empty with continuation", body: `{"data":[],"cursor":{"next":"continue"}}`},
		{name: "missing cursor", body: `{"data":[]}`, err: true},
		{name: "null data", body: `{"data":null,"cursor":{}}`, err: true},
		{name: "long native cursor", body: `{"data":[],"cursor":{"next":"` + strings.Repeat("c", execution.SessionSearchMaxCursor+1) + `"}}`, err: true},
		{name: "non-advancing cursor", body: `{"data":[],"cursor":{"next":"same"}}`, cursor: "same", err: true},
		{name: "oversized roster page", body: `{"data":[` + one + `,` + one + `],"cursor":{}}`, err: true},
		{name: "malformed roster ID", body: `{"data":[{"id":"wrong"}],"cursor":{}}`, err: true},
		{name: "malformed cursor shape", body: `{"data":[],"cursor":{"next":17}}`, err: true},
		{name: "maximum bound", body: `{"data":[],"cursor":{"next":null}}`, limit: execution.SessionSearchMaxLimit},
		{name: "stale", status: 400, body: `{"_tag":"InvalidCursorError"}`, cursor: "expired", err: true},
		{name: "malformed", status: 400, body: `{"_tag":"InvalidCursorError"}`, cursor: "bad", err: true},
		{name: "unrelated bad request", status: 400, body: `{"_tag":"InvalidRequestError"}`, cursor: "valid", err: true},
		{name: "bad request on first page", status: 400, body: `{"_tag":"InvalidCursorError"}`, err: true},
		{name: "native outage", status: 500, body: `{"_tag":"InvalidCursorError"}`, cursor: "valid", err: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limit := max(tc.limit, 1)
			a := sessionSearchFixture(t, rosterOperation, rosterComponents, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/session" || r.URL.Query().Get("limit") != strconv.Itoa(limit) {
					t.Errorf("unexpected roster request %s", r.URL)
				}
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				fmt.Fprint(w, tc.body)
			})
			page, err := a.SearchSessions(t.Context(), execution.SessionSearchQuery{Limit: limit, Cursor: tc.cursor})
			if (err != nil) != tc.err {
				t.Fatalf("page %+v, error %v", page, err)
			}
			wantCursorError := tc.name == "stale" || tc.name == "malformed"
			if errors.Is(err, execution.ErrSessionSearchCursor) != wantCursorError {
				t.Errorf("cursor error %v, want %v", err, wantCursorError)
			}
			if tc.name == "empty with continuation" && (len(page.Sessions) != 0 || page.NextCursor != "continue") {
				t.Errorf("empty native page %+v", page)
			}
		})
	}
}

func TestSessionSearchRejectsOutOfBoundsQueries(t *testing.T) {
	a, err := New(17, Settings{Endpoint: "http://unreachable.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []execution.SessionSearchQuery{
		{Limit: 0},
		{Limit: execution.SessionSearchMaxLimit + 1},
		{Limit: 1, Query: strings.Repeat("q", execution.SessionSearchMaxQuery+1)},
	} {
		var invalid *execution.Invalid
		if _, err := a.SearchSessions(t.Context(), q); !errors.As(err, &invalid) {
			t.Errorf("%+v: %v", q, err)
		}
	}
	over := execution.SessionSearchQuery{Limit: 1, Cursor: strings.Repeat("c", execution.SessionSearchMaxCursor+1)}
	if _, err := a.SearchSessions(t.Context(), over); !errors.Is(err, execution.ErrSessionSearchCursor) {
		t.Errorf("oversized cursor: %v", err)
	}
}

func TestSessionSearchNegotiatesTheOptionalContract(t *testing.T) {
	for _, tc := range []struct{ name, op, components string }{
		{"operation missing", "", rosterComponents},
		{"wrong operation", strings.Replace(rosterOperation, "session.list", "session.active", 1), rosterComponents},
		{"limit missing", strings.Replace(rosterOperation, `"limit"`, `"unbounded"`, 1), rosterComponents},
		{"cursor missing", strings.Replace(rosterOperation, `"cursor", "in"`, `"other", "in"`, 1), rosterComponents},
		{"bare array", rosterOperation, strings.Replace(rosterComponents, `"type":"object"`, `"type":"array"`, 1)},
		{"missing next cursor", rosterOperation, strings.Replace(rosterComponents, `"next":`, `"earlier":`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := sessionSearchFixture(t, tc.op, tc.components, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/session/ses_direct":
					fmt.Fprint(w, `{"data":{"id":"ses_direct","projectID":"project","location":{"directory":"/repo"},"time":{"created":1,"updated":2}}}`)
				case "/api/session/active":
					fmt.Fprint(w, `{"data":{"ses_direct":{"type":"running"}}}`)
				case "/api/session/ses_direct/permission", "/api/session/ses_direct/form":
					fmt.Fprint(w, `{"data":[]}`)
				case "/api/debug/location":
					fmt.Fprint(w, `[]`)
				default:
					t.Errorf("unexpected request %s", r.URL)
					http.NotFound(w, r)
				}
			})
			ctx := t.Context()
			if h := a.Health(ctx); !h.Available {
				t.Fatal(h.Reason)
			}
			f, err := a.Features(ctx)
			if err != nil || f.Operations[execution.OpSessionSearch] != (execution.Availability{Reason: sessionSearchUnavailable}) ||
				!f.Operations[execution.OpAdopt].Available || !f.Operations[execution.OpAttention].Available {
				t.Fatalf("optional search affected features %+v: %v", f, err)
			}
			var unsupported *execution.SessionSearchUnsupported
			if _, err := a.SearchSessions(ctx, execution.SessionSearchQuery{Limit: 1}); !errors.As(err, &unsupported) || unsupported.Reason != sessionSearchUnavailable {
				t.Fatalf("unavailable search: %v", err)
			}
			ref, err := a.ResolveLink(ctx, "ses_direct")
			if err != nil || ref != (execution.SessionRef{ConnectionID: 17, ExternalSessionID: "ses_direct"}) {
				t.Fatalf("direct reference %+v: %v", ref, err)
			}
			observation, err := a.Observe(ctx, ref)
			if err != nil || observation.Session != ref || observation.Directory != "/repo" {
				t.Fatalf("adoption observation %+v: %v", observation, err)
			}
			attention, err := a.Attention(ctx)
			if err != nil || !slices.Equal(attention, []execution.SessionRef{ref}) {
				t.Fatalf("attention %+v: %v", attention, err)
			}
		})
	}
}
