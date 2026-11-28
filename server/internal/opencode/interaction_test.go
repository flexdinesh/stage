package opencode

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/opencode/opencodetest"
)

// The reply operations follow the v2.3.2 OpenAPI: the permission body is
// inline, the form body a $ref to Form.Reply.
const (
	permissionReply = `{"operationId": "session.permission.reply", "requestBody": {"required": true, "content": {"application/json": {"schema": {
		"type": "object", "required": ["decision"], "additionalProperties": false,
		"properties": {"decision": {"$ref": "#/components/schemas/Permission.Reply"}, "message": {"anyOf": [{"type": "string"}, {"type": "null"}]}}
	}}}}}`
	formReply       = `{"operationId": "session.form.reply", "requestBody": {"required": true, "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Form.Reply"}}}}}`
	replyComponents = `{"schemas": {"Form.Reply": {"type": "object", "required": ["answer"], "properties": {"answer": {"$ref": "#/components/schemas/Form.Answer"}}}}}`
)

const liveSession = "ses_live"

// interactionFake is a stateful OpenCode session: pending permissions and
// forms, recorded replies and prompts.
type interactionFake struct {
	mu          sync.Mutex
	permissions []map[string]any
	forms       []map[string]any
	// replyStatus answers replies (0: 204); replyBody is its JSON body.
	replyStatus int
	replyBody   string
	replies     []recorded
	prompts     []string
	// admitID overrides the admitted message ID (else the prompt's id).
	admitID string
	gone    bool
}

type recorded struct{ path, body string }

func (f *interactionFake) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	prefix := "/api/session/" + liveSession
	if f.gone || !strings.HasPrefix(r.URL.Path, prefix+"/") {
		http.Error(w, `{"_tag":"SessionNotFoundError","sessionID":"x","message":"Session not found"}`, http.StatusNotFound)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, prefix)
	body, _ := io.ReadAll(r.Body)
	switch {
	case r.Method == http.MethodGet && rest == "/permission":
		writeJSON(w, map[string]any{"data": append([]map[string]any{}, f.permissions...)})
	case r.Method == http.MethodGet && rest == "/form":
		writeJSON(w, map[string]any{"data": append([]map[string]any{}, f.forms...)})
	case r.Method == http.MethodPost && strings.HasSuffix(rest, "/reply"):
		f.replies = append(f.replies, recorded{r.URL.Path, string(body)})
		if f.replyStatus != 0 {
			http.Error(w, f.replyBody, f.replyStatus)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && rest == "/prompt":
		f.prompts = append(f.prompts, string(body))
		var in struct{ ID, Text, Delivery string }
		_ = json.Unmarshal(body, &in)
		id := in.ID
		if f.admitID != "" {
			id = f.admitID
		}
		writeJSON(w, map[string]any{"data": map[string]any{"id": id, "sessionID": liveSession, "type": "user", "time": map[string]any{"created": 1}, "payload": map[string]any{"text": in.Text}, "delivery": in.Delivery}})
	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// interactionFixture serves the fake contract, plus the reply operations
// when replies, and routes session calls to a fresh interactionFake.
func interactionFixture(t *testing.T, replies bool) (*Adapter, *interactionFake) {
	t.Helper()
	fake := opencodetest.New(t, "")
	if !replies {
		fake.WithoutInteraction()
	}
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
	if replies {
		spec.Paths["/api/session/{sessionID}/permission/{requestID}/reply"] = map[string]json.RawMessage{"post": json.RawMessage(permissionReply)}
		spec.Paths["/api/session/{sessionID}/form/{formID}/reply"] = map[string]json.RawMessage{"post": json.RawMessage(formReply)}
		spec.Components = json.RawMessage(replyComponents)
	}
	session := &interactionFake{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/info":
			base.ServeHTTP(w, r)
		case "/openapi.json":
			writeJSON(w, spec)
		default:
			session.handle(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	a, err := New(17, Settings{Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return a, session
}

var liveRef = execution.SessionRef{ConnectionID: 17, ExternalSessionID: liveSession}

func bashPermission(id string) map[string]any {
	return map[string]any{
		"id": id, "sessionID": liveSession, "action": "bash", "resources": []string{"npm test"},
		"message": "Runs the suite", "metadata": map[string]any{"input": map[string]any{"description": "Run tests", "command": "npm test"}}, "save": []string{"npm *"},
		"source": map[string]any{"type": "tool", "messageID": "msg_1", "id": "call_1"},
	}
}

// questionForm is what OpenCode's question tool creates.
func questionForm(id string) map[string]any {
	return map[string]any{
		"id": id, "sessionID": liveSession, "title": "Questions",
		"metadata": map[string]any{"kind": "question", "tool": map[string]any{"messageID": "msg_1", "id": "call_2"}},
		"fields": []map[string]any{
			{"key": "q0", "title": "Package", "description": "Which manager?", "type": "string", "custom": true,
				"options": []map[string]any{{"value": "pnpm", "label": "pnpm", "description": "Fast"}, {"value": "npm", "label": "npm"}}},
			{"key": "q1", "title": "Checks", "description": "Run which?", "type": "multiselect", "custom": true,
				"options": []map[string]any{{"value": "lint", "label": "Lint"}, {"value": "test", "label": "Test"}}},
		},
	}
}

// elicitation mixes free text, boolean and number fields.
func elicitation(id string) map[string]any {
	return map[string]any{
		"id": id, "sessionID": liveSession, "title": "Deploy settings", "metadata": map[string]any{"kind": "mcp-elicitation"},
		"fields": []map[string]any{
			{"key": "name", "title": "Name", "type": "string"},
			{"key": "confirm", "title": "Ship it?", "type": "boolean", "default": false},
			{"key": "replicas", "title": "Replicas", "type": "integer", "minimum": 1, "maximum": "Infinity"},
		},
	}
}

func TestFeaturesGatesRespondOnReplyOperations(t *testing.T) {
	for _, replies := range []bool{true, false} {
		a, _ := interactionFixture(t, replies)
		c, err := a.Features(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		send := c.Operations[execution.OpSend]
		if send.Available != replies || (!replies && send.Reason != sendUnavailable) {
			t.Fatalf("replies=%v send %+v", replies, send)
		}
		got := c.Operations[execution.OpRespond]
		if got.Available != replies || (!replies && got.Reason != respondUnavailable) {
			t.Fatalf("replies=%v respond %+v", replies, got)
		}
	}
}

func TestRequestsMapPermissionsAndShowableForms(t *testing.T) {
	a, fake := interactionFixture(t, true)
	conditional := elicitation("frm_cond")
	conditional["fields"] = []map[string]any{
		{"key": "mode", "type": "boolean"},
		{"key": "why", "type": "string", "when": []map[string]any{{"key": "mode", "op": "eq", "value": true}}},
	}
	external := map[string]any{"id": "frm_ext", "sessionID": liveSession, "title": "Sign in",
		"fields": []map[string]any{{"key": "auth", "type": "external", "url": "https://example.test"}}}
	fake.permissions = []map[string]any{bashPermission("per_1"), {"id": "per_2", "sessionID": liveSession, "action": "mcp_thing", "resources": []string{}}}
	fake.forms = []map[string]any{questionForm("frm_q"), conditional, external, elicitation("frm_e")}

	rs, err := a.Requests(t.Context(), liveRef)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, r := range rs {
		ids = append(ids, r.ID)
	}
	if !reflect.DeepEqual(ids, []string{"per_1", "per_2", "frm_q", "frm_e"}) {
		t.Fatalf("listed %v", ids)
	}
	p := rs[0]
	wantOptions := []execution.Choice{choice("once", "Allow once"), choice("always", "Allow always"), choice("reject", "Deny")}
	if p.Kind != execution.RequestApproval || p.Title != "Run shell commands" || p.Detail != "Runs the suite" ||
		p.Preview != "command: npm test\ndescription: Run tests\nnpm test" || p.TextOption != "reject" || !reflect.DeepEqual(p.Options, wantOptions) {
		t.Fatalf("permission %+v", p)
	}
	// No save patterns: OpenCode would save nothing, so Allow always is not offered.
	if rs[1].Title != "Permission required: mcp_thing" || rs[1].Preview != "" ||
		!reflect.DeepEqual(rs[1].Options, []execution.Choice{choice("once", "Allow once"), choice("reject", "Deny")}) {
		t.Fatalf("unknown action %+v", rs[1])
	}
	q := rs[2]
	if q.Kind != execution.RequestQuestion || q.Title != "OpenCode asks 2 questions" || len(q.Questions) != 2 {
		t.Fatalf("question form %+v", q)
	}
	want0 := execution.Question{ID: "q0", Header: "Package", Prompt: "Which manager?", Custom: true,
		Options: []execution.QuestionOption{{Label: "pnpm", Description: "Fast"}, {Label: "npm"}}}
	if !reflect.DeepEqual(q.Questions[0], want0) || !q.Questions[1].Multi || !q.Questions[1].Custom {
		t.Fatalf("questions %+v", q.Questions)
	}
	e := rs[3]
	if e.Title != "Deploy settings" || len(e.Questions) != 3 {
		t.Fatalf("elicitation %+v", e)
	}
	name, confirm, replicas := e.Questions[0], e.Questions[1], e.Questions[2]
	if name.Prompt != "Name" || !name.Custom || len(name.Options) != 0 {
		t.Fatalf("free text %+v", name)
	}
	if confirm.Custom || !reflect.DeepEqual(confirm.Options, []execution.QuestionOption{{Label: "Yes"}, {Label: "No"}}) {
		t.Fatalf("boolean %+v", confirm)
	}
	if !replicas.Custom || len(replicas.Options) != 0 {
		t.Fatalf("number %+v", replicas)
	}
}

func TestRequestsRefuseForeignAndMissingSessions(t *testing.T) {
	a, fake := interactionFixture(t, true)
	if _, err := a.Requests(t.Context(), execution.SessionRef{ConnectionID: 99, ExternalSessionID: liveSession}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("other connection: %v", err)
	}
	other := bashPermission("per_x")
	other["sessionID"] = "ses_other"
	fake.permissions = []map[string]any{other}
	if _, err := a.Requests(t.Context(), liveRef); err == nil || errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("foreign permission accepted: %v", err)
	}
	fake.gone = true
	if _, err := a.Requests(t.Context(), liveRef); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("missing session: %v", err)
	}
}

func TestRespondPermission(t *testing.T) {
	a, fake := interactionFixture(t, true)
	fake.permissions = []map[string]any{bashPermission("per_1")}
	ctx := t.Context()
	for _, tc := range []struct {
		name string
		r    execution.Response
	}{
		{"unknown option", execution.Response{Option: "allow"}},
		{"text on allow", execution.Response{Option: "once", Text: "why"}},
		{"answers", execution.Response{Answers: map[string][]string{"q0": {"x"}}}},
	} {
		var invalid *execution.Invalid
		if err := a.Respond(ctx, liveRef, "per_1", tc.r); !errors.As(err, &invalid) {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
	if len(fake.replies) != 0 {
		t.Fatalf("misfit answers sent: %+v", fake.replies)
	}
	if err := a.Respond(ctx, liveRef, "per_1", execution.Response{Option: "reject", Text: " use pnpm "}); err != nil {
		t.Fatal(err)
	}
	if err := a.Respond(ctx, liveRef, "per_1", execution.Response{Option: "always"}); err != nil {
		t.Fatal(err)
	}
	want := []recorded{
		{"/api/session/ses_live/permission/per_1/reply", `{"decision":"reject","message":"use pnpm"}`},
		{"/api/session/ses_live/permission/per_1/reply", `{"decision":"always"}`},
	}
	for i := range fake.replies {
		fake.replies[i].body = strings.TrimSpace(fake.replies[i].body)
	}
	if !reflect.DeepEqual(fake.replies, want) {
		t.Fatalf("replies %+v", fake.replies)
	}
	if err := a.Respond(ctx, liveRef, "per_gone", execution.Response{Option: "once"}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("stale request: %v", err)
	}
	unsaved := bashPermission("per_2")
	delete(unsaved, "save")
	fake.permissions = append(fake.permissions, unsaved)
	var invalid *execution.Invalid
	if err := a.Respond(ctx, liveRef, "per_2", execution.Response{Option: "always"}); !errors.As(err, &invalid) || len(fake.replies) != 2 {
		t.Fatalf("always without save: %v %+v", err, fake.replies)
	}
	fake.replyStatus, fake.replyBody = http.StatusNotFound, `{"_tag":"PermissionNotFoundError","requestID":"per_1","message":"gone"}`
	if err := a.Respond(ctx, liveRef, "per_1", execution.Response{Option: "once"}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("answered elsewhere: %v", err)
	}
}

func TestRespondForm(t *testing.T) {
	a, fake := interactionFixture(t, true)
	fake.forms = []map[string]any{questionForm("frm_q"), elicitation("frm_e")}
	ctx := t.Context()
	if err := a.Respond(ctx, liveRef, "frm_q", execution.Response{Answers: map[string][]string{"q0": {"pnpm"}, "q1": {"Lint", "also docs"}}}); err != nil {
		t.Fatal(err)
	}
	if err := a.Respond(ctx, liveRef, "frm_e", execution.Response{Answers: map[string][]string{"name": {"api"}, "confirm": {"Yes"}, "replicas": {" 3 "}}}); err != nil {
		t.Fatal(err)
	}
	if len(fake.replies) != 2 {
		t.Fatalf("replies %+v", fake.replies)
	}
	for i, want := range []map[string]any{
		{"answer": map[string]any{"q0": "pnpm", "q1": []any{"lint", "also docs"}}},
		{"answer": map[string]any{"name": "api", "confirm": true, "replicas": float64(3)}},
	} {
		var got map[string]any
		if err := json.Unmarshal([]byte(fake.replies[i].body), &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("reply %d: %v, want %v", i, got, want)
		}
	}

	for _, tc := range []struct {
		name string
		id   string
		r    execution.Response
	}{
		{"option alone", "frm_q", execution.Response{Option: "pnpm"}},
		{"unknown key", "frm_q", execution.Response{Answers: map[string][]string{"q9": {"x"}}}},
		{"two answers", "frm_q", execution.Response{Answers: map[string][]string{"q0": {"pnpm", "npm"}}}},
		{"boolean text", "frm_e", execution.Response{Answers: map[string][]string{"confirm": {"sure"}}}},
		{"not a number", "frm_e", execution.Response{Answers: map[string][]string{"replicas": {"three"}}}},
		{"fraction", "frm_e", execution.Response{Answers: map[string][]string{"replicas": {"1.5"}}}},
	} {
		var invalid *execution.Invalid
		if err := a.Respond(ctx, liveRef, tc.id, tc.r); !errors.As(err, &invalid) {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
	if len(fake.replies) != 2 {
		t.Fatalf("misfit answers sent: %+v", fake.replies[2:])
	}

	fake.replyStatus, fake.replyBody = http.StatusBadRequest, `{"_tag":"FormInvalidAnswerError","id":"frm_e","message":"Form field is too small: replicas"}`
	var invalid *execution.Invalid
	if err := a.Respond(ctx, liveRef, "frm_e", execution.Response{Answers: map[string][]string{"replicas": {"0"}}}); !errors.As(err, &invalid) || invalid.Reason != "Form field is too small: replicas" {
		t.Fatalf("refused answer: %v", err)
	}
	fake.replyStatus, fake.replyBody = http.StatusConflict, `{"_tag":"FormAlreadySettledError","id":"frm_q","message":"settled"}`
	if err := a.Respond(ctx, liveRef, "frm_q", execution.Response{Answers: map[string][]string{"q0": {"npm"}}}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("settled form: %v", err)
	}
	if err := a.Respond(ctx, liveRef, "frm_gone", execution.Response{Answers: map[string][]string{"q0": {"npm"}}}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("stale form: %v", err)
	}
	if err := a.Respond(ctx, liveRef, "bogus", execution.Response{Option: "once"}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestRespondUnshowableFormIsInvalid(t *testing.T) {
	a, fake := interactionFixture(t, true)
	fake.forms = []map[string]any{{"id": "frm_ext", "sessionID": liveSession, "title": "Sign in",
		"fields": []map[string]any{{"key": "auth", "type": "external", "url": "https://example.test"}}}}
	var invalid *execution.Invalid
	if err := a.Respond(t.Context(), liveRef, "frm_ext", execution.Response{Answers: map[string][]string{"auth": {"Yes"}}}); !errors.As(err, &invalid) {
		t.Fatalf("unshowable form: %v", err)
	}
	if len(fake.replies) != 0 {
		t.Fatalf("sent %+v", fake.replies)
	}
}

func TestRespondRequiresReplyOperations(t *testing.T) {
	a, fake := interactionFixture(t, false)
	fake.permissions = []map[string]any{bashPermission("per_1")}
	err := a.Respond(t.Context(), liveRef, "per_1", execution.Response{Option: "once"})
	if err == nil || err.Error() != respondUnavailable || len(fake.replies) != 0 {
		t.Fatalf("respond without contract: %v %+v", err, fake.replies)
	}
}

// promptBody is a recorded session.prompt body.
type promptBody struct {
	ID       string            `json:"id"`
	Text     string            `json:"text"`
	Metadata map[string]string `json:"metadata"`
	Delivery string            `json:"delivery"`
}

func decodePrompt(t *testing.T, raw string) promptBody {
	t.Helper()
	var p promptBody
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSendQueuesWithAStageMessageID(t *testing.T) {
	a, fake := interactionFixture(t, true)
	ctx := t.Context()
	if err := a.Send(ctx, liveRef, execution.Message{Text: "also run lint"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Send(ctx, liveRef, execution.Message{Text: "and docs"}); err != nil {
		t.Fatal(err)
	}
	first, second := decodePrompt(t, fake.prompts[0]), decodePrompt(t, fake.prompts[1])
	if first.Text != "also run lint" || first.Delivery != "queue" || !strings.HasPrefix(first.ID, "msg_") || len(first.ID) != 36 ||
		!reflect.DeepEqual(first.Metadata, map[string]string{"stageMessage": first.ID}) {
		t.Fatalf("prompt %+v", first)
	}
	if second.ID == first.ID {
		t.Fatalf("fresh sends share message ID %s", first.ID)
	}

	// A RequestID derives the ID, so a retry is the same message to OpenCode.
	steer := execution.Message{Text: "digest", RequestID: "steering-3-41"}
	for range 2 {
		if err := a.Send(ctx, liveRef, steer); err != nil {
			t.Fatal(err)
		}
	}
	r1, r2 := decodePrompt(t, fake.prompts[2]), decodePrompt(t, fake.prompts[3])
	want, _ := messageID(liveSession, "steering-3-41")
	if r1.ID != want || r2.ID != want || r1.Metadata["stageMessage"] != "steering-3-41" || r1.Delivery != "queue" {
		t.Fatalf("retried prompts %+v %+v", r1, r2)
	}
	if other, _ := messageID("ses_other", "steering-3-41"); other == want {
		t.Fatal("request IDs collide across sessions")
	}

	fake.admitID = "msg_someone_else"
	if err := a.Send(ctx, liveRef, execution.Message{Text: "hi"}); err == nil {
		t.Fatal("mismatched admission accepted")
	}
}

func TestSendRequiresPromptDelivery(t *testing.T) {
	a, fake := interactionFixture(t, false)
	err := a.Send(t.Context(), liveRef, execution.Message{Text: "hi"})
	if err == nil || err.Error() != sendUnavailable || len(fake.prompts) != 0 {
		t.Fatalf("send without contract: %v %q", err, fake.prompts)
	}
}

func TestSendPromptsUnlessARequestIsPending(t *testing.T) {
	a, fake := interactionFixture(t, true)
	ctx := t.Context()
	if err := a.Send(ctx, liveRef, execution.Message{Text: "also run lint"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.prompts) != 1 {
		t.Fatalf("prompts %q", fake.prompts)
	}
	for _, tc := range []struct {
		name        string
		permissions []map[string]any
		forms       []map[string]any
		reason      string
	}{
		{"permission", []map[string]any{bashPermission("per_1")}, nil, busyPermission},
		{"questions", nil, []map[string]any{questionForm("frm_q")}, busyQuestions},
		{"unshowable form", nil, []map[string]any{questionForm("frm_q"), {"id": "frm_ext", "sessionID": liveSession, "title": "Sign in",
			"fields": []map[string]any{{"key": "auth", "type": "external", "url": "https://example.test"}}}}, busyForm},
	} {
		fake.permissions, fake.forms = tc.permissions, tc.forms
		var busy *execution.Busy
		if err := a.Send(ctx, liveRef, execution.Message{Text: "hi"}); !errors.As(err, &busy) || busy.Reason != tc.reason {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
	if len(fake.prompts) != 1 {
		t.Fatalf("busy session prompted: %q", fake.prompts)
	}
	fake.gone = true
	if err := a.Send(ctx, liveRef, execution.Message{Text: "hi"}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("missing session: %v", err)
	}
	if err := a.Send(ctx, execution.SessionRef{ConnectionID: 17, ExternalSessionID: "bad id"}, execution.Message{Text: "hi"}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("invalid ref: %v", err)
	}
}
