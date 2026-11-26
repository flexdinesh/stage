package claudecode_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// hookFixture is a captured PermissionRequest (testdata/hooks).
type hookFixture struct {
	Tool        string            `json:"tool_name"`
	Input       json.RawMessage   `json:"tool_input"`
	Suggestions []json.RawMessage `json:"permission_suggestions"`
}

func loadHook(t *testing.T, name string) hookFixture {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "hooks", name))
	if err != nil {
		t.Fatal(err)
	}
	var h hookFixture
	if err := json.Unmarshal(b, &h); err != nil {
		t.Fatal(err)
	}
	return h
}

func ask(f *claudetest.Fake, id string, h hookFixture) *claudetest.HookCall {
	sugg := make([]any, 0, len(h.Suggestions))
	for _, s := range h.Suggestions {
		sugg = append(sugg, s)
	}
	return f.Ask(id, h.Tool, h.Input, sugg)
}

// launchHooked launches one session through an adapter with a receiver.
func launchHooked(t *testing.T) (*claudetest.Fake, *claudecode.Adapter, *claudecode.Hooks, execution.SessionRef) {
	t.Helper()
	f := claudetest.New(t)
	h := f.Hooks()
	a := f.HookedAdapter(conn, h)
	o, err := a.Launch(t.Context(), intent(f, "req-h"), execution.Checkpoint{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return f, a, h, o.Session
}

func requests(t *testing.T, a *claudecode.Adapter, ref execution.SessionRef) []execution.Request {
	t.Helper()
	rs, err := a.Requests(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

// decisionOf is the hook response's decision object.
func decisionOf(t *testing.T, c *claudetest.HookCall) map[string]json.RawMessage {
	t.Helper()
	if c.Err != nil || c.Status != http.StatusOK {
		t.Fatalf("hook response %d %v", c.Status, c.Err)
	}
	var out struct {
		HookSpecificOutput struct {
			HookEventName string                     `json:"hookEventName"`
			Decision      map[string]json.RawMessage `json:"decision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(c.Body), &out); err != nil || out.HookSpecificOutput.HookEventName != "PermissionRequest" {
		t.Fatalf("hook body %q: %v", c.Body, err)
	}
	return out.HookSpecificOutput.Decision
}

func jsonEqual(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	gb, _ := json.Marshal(g)
	wb, _ := json.Marshal(w)
	if string(gb) != string(wb) {
		t.Fatalf("got %s, want %s", gb, wb)
	}
}

// A launch passes the receiver's settings file; Claude's prompt lists as an approval with Claude's own choices, and Allow
// answers the hook with an allow decision.
func TestApprovalAllow(t *testing.T) {
	f, a, h, ref := launchHooked(t)
	args := f.Launches()[0].Args
	if i := slices.Index(args, "--settings"); i < 0 || args[i+1] != h.SettingsPath() || slices.Index(args, "--") < i {
		t.Fatalf("args %q", args)
	}
	if caps, _ := a.Features(t.Context()); !caps.Operations[execution.OpRespond].Available || !caps.Operations[execution.OpSend].Available {
		t.Fatalf("operations %+v", caps.Operations)
	}
	call := ask(f, ref.ExternalSessionID, loadHook(t, "permission-request.json"))
	var rs []execution.Request
	for range 100 {
		if rs = requests(t, a, ref); len(rs) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(rs) != 1 {
		t.Fatalf("requests %+v", rs)
	}
	r := rs[0]
	ids := []string{}
	labels := []string{}
	for _, o := range r.Options {
		ids, labels = append(ids, o.ID), append(labels, o.Label)
	}
	if r.Kind != "approval" || r.Title != "Run a command" || r.Preview != "touch blocked.txt && echo made" || r.Detail != "Create blocked.txt and echo made" || r.TextOption != "deny" ||
		!slices.Equal(ids, []string{"allow", "always:0", "always:1", "deny"}) {
		t.Fatalf("request %+v", r)
	}
	if labels[1] != "Always allow access to /home/user/src/spike for this session" || labels[2] != "Allow and accept edits for this session" {
		t.Fatalf("labels %q", labels)
	}
	if call.Answered() {
		t.Fatal("hook answered before the user")
	}
	if err := a.Respond(t.Context(), ref, r.ID, execution.Response{Option: "allow"}); err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, mustJSON(t, decisionOf(t, call.Wait(t, 2*time.Second))), `{"behavior":"allow"}`)
	if rs := requests(t, a, ref); len(rs) != 0 {
		t.Fatalf("answered request still listed %+v", rs)
	}
	if err := a.Respond(t.Context(), ref, r.ID, execution.Response{Option: "allow"}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("second answer: %v", err)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// waitRequest is the session's one listed request.
func waitRequest(t *testing.T, a *claudecode.Adapter, ref execution.SessionRef) execution.Request {
	t.Helper()
	for range 200 {
		if rs := requests(t, a, ref); len(rs) == 1 {
			return rs[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no request")
	return execution.Request{}
}

// Deny sends the reason (or a default) as Claude's tool result; Always
// allow passes Claude's suggestion back verbatim; answers that don't fit
// are refused and send nothing.
func TestApprovalDenyAlwaysAndInvalid(t *testing.T) {
	f, a, _, ref := launchHooked(t)
	id := ref.ExternalSessionID

	call := ask(f, id, loadHook(t, "permission-rule.json"))
	r := waitRequest(t, a, ref)
	if r.Options[1].Label != `Always allow Bash(python3 -c "print(41+1)") in this project (.claude/settings.local.json)` {
		t.Fatalf("label %q", r.Options[1].Label)
	}
	var invalid *execution.Invalid
	for _, bad := range []execution.Response{
		{Option: "maybe"}, {Option: "always:7"}, {Option: "always:x"}, {Option: "allow", Text: "because"},
		{Answers: map[string][]string{"0": {"Red"}}},
	} {
		if err := a.Respond(t.Context(), ref, r.ID, bad); !errors.As(err, &invalid) {
			t.Fatalf("%+v accepted: %v", bad, err)
		}
	}
	if call.Answered() {
		t.Fatal("an invalid answer reached Claude")
	}
	if err := a.Respond(t.Context(), ref, r.ID, execution.Response{Option: "always:0"}); err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, mustJSON(t, decisionOf(t, call.Wait(t, 2*time.Second))),
		`{"behavior":"allow","updatedPermissions":[{"type":"addRules","rules":[{"toolName":"Bash","ruleContent":"python3 -c \"print(41+1)\""}],"behavior":"allow","destination":"localSettings"}]}`)

	call = ask(f, id, loadHook(t, "permission-request.json"))
	r = waitRequest(t, a, ref)
	if err := a.Respond(t.Context(), ref, r.ID, execution.Response{Option: "deny", Text: "  use another name "}); err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, mustJSON(t, decisionOf(t, call.Wait(t, 2*time.Second))), `{"behavior":"deny","message":"Denied in Stage: use another name"}`)

	call = ask(f, id, loadHook(t, "permission-request.json"))
	r = waitRequest(t, a, ref)
	if err := a.Respond(t.Context(), ref, r.ID, execution.Response{Option: "deny"}); err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, mustJSON(t, decisionOf(t, call.Wait(t, 2*time.Second))), `{"behavior":"deny","message":"Denied in Stage."}`)
}

// AskUserQuestion lists as a question request; the answers go back as its
// updated input, keyed by question text.
func TestQuestionAnswers(t *testing.T) {
	f, a, _, ref := launchHooked(t)
	h := loadHook(t, "ask-user-question.json")
	call := ask(f, ref.ExternalSessionID, h)
	r := waitRequest(t, a, ref)
	if r.Kind != "question" || len(r.Questions) != 1 || len(r.Options) != 0 {
		t.Fatalf("request %+v", r)
	}
	q := r.Questions[0]
	if q.ID != "0" || q.Header != "Color" || q.Prompt != "Which color do you prefer?" || q.Multi || !q.Custom || len(q.Options) != 2 || q.Options[1].Label != "Blue" || q.Options[1].Description != "Choose blue" {
		t.Fatalf("question %+v", q)
	}
	var invalid *execution.Invalid
	for _, bad := range []execution.Response{
		{Option: "allow"}, {Answers: map[string][]string{}}, {Answers: map[string][]string{"0": {" "}}},
		{Answers: map[string][]string{"0": {"Red", "Blue"}}}, {Answers: map[string][]string{"0": {"Red"}, "1": {"x"}}},
	} {
		if err := a.Respond(t.Context(), ref, r.ID, bad); !errors.As(err, &invalid) {
			t.Fatalf("%+v accepted: %v", bad, err)
		}
	}
	if err := a.Respond(t.Context(), ref, r.ID, execution.Response{Answers: map[string][]string{"0": {"Blue"}}}); err != nil {
		t.Fatal(err)
	}
	d := decisionOf(t, call.Wait(t, 2*time.Second))
	jsonEqual(t, d["behavior"], `"allow"`)
	jsonEqual(t, d["updatedInput"], `{"questions":[{"question":"Which color do you prefer?","header":"Color","options":[{"label":"Red","description":"Choose red"},{"label":"Blue","description":"Choose blue"}],"multiSelect":false}],"answers":{"Which color do you prefer?":"Blue"}}`)

	// Several picks join; free text is an answer too.
	multi := json.RawMessage(`{"questions":[{"question":"Which?","header":"Pick","options":[{"label":"A","description":""},{"label":"B","description":""}],"multiSelect":true},{"question":"Name?","header":"Name","options":[],"multiSelect":false}]}`)
	call = f.Ask(ref.ExternalSessionID, "AskUserQuestion", multi, nil)
	r = waitRequest(t, a, ref)
	if r.Title != "Claude Code asks 2 questions" || !r.Questions[0].Multi {
		t.Fatalf("request %+v", r)
	}
	if err := a.Respond(t.Context(), ref, r.ID, execution.Response{Answers: map[string][]string{"0": {"A", "B"}, "1": {"Ada"}}}); err != nil {
		t.Fatal(err)
	}
	var in struct {
		Answers map[string]string `json:"answers"`
	}
	if err := json.Unmarshal(decisionOf(t, call.Wait(t, 2*time.Second))["updatedInput"], &in); err != nil || in.Answers["Which?"] != "A, B" || in.Answers["Name?"] != "Ada" {
		t.Fatalf("answers %+v %v", in.Answers, err)
	}
}

// Claude's prompt answered in Claude Code (the session stops waiting), a
// newer prompt of the session, or Claude giving up all end a request:
// it leaves the list and the hook gets no decision.
func TestRequestsEndWithoutStage(t *testing.T) {
	f, a, _, ref := launchHooked(t)
	id := ref.ExternalSessionID
	h := loadHook(t, "permission-request.json")

	call := ask(f, id, h)
	r := waitRequest(t, a, ref)
	f.SetState(id, "working", "busy", "")
	if rs := requests(t, a, ref); len(rs) != 0 {
		t.Fatalf("answered in Claude, still listed %+v", rs)
	}
	if c := call.Wait(t, 2*time.Second); c.Status != http.StatusNoContent || c.Body != "" {
		t.Fatalf("released hook %d %q", c.Status, c.Body)
	}
	if err := a.Respond(t.Context(), ref, r.ID, execution.Response{Option: "allow"}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("answer after release: %v", err)
	}

	// Answered in Claude between listing and answering: the fresh list wins.
	call = ask(f, id, h)
	r = waitRequest(t, a, ref)
	f.SetState(id, "done", "idle", "")
	if err := a.Respond(t.Context(), ref, r.ID, execution.Response{Option: "allow"}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("stale answer: %v", err)
	}
	if c := call.Wait(t, 2*time.Second); c.Body != "" {
		t.Fatalf("stale answer reached Claude: %q", c.Body)
	}

	first := ask(f, id, h)
	old := waitRequest(t, a, ref)
	second := ask(f, id, h)
	if c := first.Wait(t, 2*time.Second); c.Body != "" {
		t.Fatalf("superseded hook got %q", c.Body)
	}
	if r := waitRequest(t, a, ref); r.ID == old.ID {
		t.Fatal("older request still listed")
	}
	second.Abandon()
	for range 100 {
		if len(requests(t, a, ref)) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("abandoned hook still listed")
}
