package t3

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func ptr[T any](v T) *T { return &v }

func sampleQuestions() []Question {
	return []Question{
		{ID: "scope", Header: "Scope", Question: "Which scope?", Options: []QuestionOption{
			{Label: "Server", Description: "Go only"},
			{Label: "Web", Description: "UI only", Value: ptr("web-value")},
		}},
		{ID: "extras", Header: "Extras", Question: "Which extras?", MultiSelect: true, Options: []QuestionOption{
			{Label: "Docs", Description: ""}, {Label: "Tests", Description: ""},
		}},
		{ID: "strict", Header: "Strict", Question: "Pick one", AllowCustomAnswer: ptr(false), Options: []QuestionOption{
			{Label: "Yes", Description: ""}, {Label: "No", Description: ""},
		}},
	}
}

func TestAnswerJSON(t *testing.T) {
	b, err := json.Marshal(map[string]Answer{"a": TextAnswer("x"), "b": ValuesAnswer([]string{"p", "q"}), "c": ValuesAnswer(nil)})
	if err != nil || string(b) != `{"a":"x","b":["p","q"],"c":[]}` {
		t.Fatalf("marshal = %s, %v", b, err)
	}
	var got map[string]Answer
	if err := json.Unmarshal([]byte(`{"a":"x","b":["p"]}`), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, map[string]Answer{"a": TextAnswer("x"), "b": ValuesAnswer([]string{"p"})}) {
		t.Fatalf("unmarshal = %+v", got)
	}
	if err := json.Unmarshal([]byte(`{"a":3}`), &got); err == nil {
		t.Fatal("number accepted")
	}
}

func TestValidateAnswers(t *testing.T) {
	qs := sampleQuestions()
	ok := map[string]Answer{"scope": TextAnswer(" Server "), "extras": ValuesAnswer([]string{"Tests", "Docs"}), "strict": TextAnswer("Yes")}
	if err := ValidateAnswers(qs, ok); err != nil {
		t.Fatal(err)
	}
	norm := NormalizeAnswers(qs, ok)
	if !reflect.DeepEqual(norm["scope"], TextAnswer("Server")) || !reflect.DeepEqual(norm["extras"], ValuesAnswer([]string{"Tests", "Docs"})) {
		t.Fatalf("normalized = %+v", norm)
	}
	with := func(id string, a Answer) map[string]Answer {
		m := map[string]Answer{}
		for k, v := range ok {
			m[k] = v
		}
		m[id] = a
		return m
	}
	valid := []map[string]Answer{
		with("scope", TextAnswer("web-value")),         // option value
		with("scope", TextAnswer("something else")),    // custom text
		with("extras", TextAnswer("custom for multi")), // custom on multi-select
	}
	for i, a := range valid {
		if err := ValidateAnswers(qs, a); err != nil {
			t.Errorf("valid %d: %v", i, err)
		}
	}
	missing := with("scope", TextAnswer("x"))
	delete(missing, "strict")
	invalid := map[string]map[string]Answer{
		"missing":         missing,
		"unknown id":      with("nope", TextAnswer("x")),
		"blank":           with("scope", TextAnswer("  ")),
		"array on single": with("scope", ValuesAnswer([]string{"Server"})),
		"empty array":     with("extras", ValuesAnswer(nil)),
		"unknown option":  with("extras", ValuesAnswer([]string{"Nope"})),
		"duplicate":       with("extras", ValuesAnswer([]string{"Docs", " Docs"})),
		"custom disabled": with("strict", TextAnswer("Maybe")),
		"label of valued": with("scope", ValuesAnswer([]string{"Web"})),
	}
	for name, a := range invalid {
		if err := ValidateAnswers(qs, a); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// initialAnswer allows an exact (even empty) custom answer.
	pre := []Question{{ID: "q", InitialAnswer: ptr("draft")}}
	if err := ValidateAnswers(pre, map[string]Answer{"q": TextAnswer("")}); err != nil {
		t.Fatal(err)
	}
}

func TestPendingRequestErrorsMapExactT3Messages(t *testing.T) {
	cases := []struct {
		msg             string
		answered, stale bool
	}{
		{"The user-input request was already answered.", true, false},
		{"The user-input request is no longer pending (expired).", false, true},
		{"The pending user-input request was not found.", false, true},
		{"requestId is required.", false, false},
	}
	for _, c := range cases {
		err := error(&ToolError{Tool: "t3_pending_request", Code: "invalid_request", Message: c.msg})
		if errors.Is(err, ErrRequestAnswered) != c.answered || errors.Is(err, ErrRequestStale) != c.stale || errors.Is(err, ErrNotFound) {
			t.Errorf("%q: answered=%v stale=%v", c.msg, errors.Is(err, ErrRequestAnswered), errors.Is(err, ErrRequestStale))
		}
	}
	other := &ToolError{Code: "thread_not_found", Message: "already answered"}
	if errors.Is(other, ErrRequestAnswered) || !errors.Is(other, ErrNotFound) {
		t.Fatal("code is not checked")
	}
}

func TestPendingRequestToolArgs(t *testing.T) {
	f := newFakeT3(t)
	var calls []map[string]any
	f.tool("t3_pending_request", func(args map[string]any) (*mcp.CallToolResult, error) {
		calls = append(calls, args)
		switch args["action"] {
		case "list":
			return structured(`{"requestIds":["r1"]}`), nil
		case "read":
			return structured(`{"requestId":"r1","questions":[{"id":"q","header":"H","question":"Q?","options":[{"label":"A","description":"d","value":"a"},{"label":"B","description":""}],"multiSelect":true}]}`), nil
		}
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{
			Text: `{"_tag":"OrchestratorMcpFailure","code":"invalid_request","message":"The user-input request was already answered."}`,
		}}}, nil
	})
	c := newTestClient(t, f, f.adminToken, NewMemoryTokenStore())
	ctx := ctxT(t)
	ids, err := c.ListPendingQuestions(ctx, "t1")
	if err != nil || !reflect.DeepEqual(ids, []string{"r1"}) {
		t.Fatalf("list = %v, %v", ids, err)
	}
	pq, err := c.ReadPendingQuestion(ctx, "t1", "r1")
	if err != nil || len(pq.Questions) != 1 || !pq.Questions[0].MultiSelect || pq.Questions[0].Options[0].Value == nil ||
		*pq.Questions[0].Options[0].Value != "a" || pq.Questions[0].Options[1].Value != nil || pq.Questions[0].AllowCustomAnswer != nil {
		t.Fatalf("read = %+v, %v", pq, err)
	}
	err = c.RespondPendingQuestion(ctx, "t1", "r1", map[string]Answer{"q": ValuesAnswer([]string{"a"})})
	if !errors.Is(err, ErrRequestAnswered) {
		t.Fatalf("respond err = %v", err)
	}
	if len(calls) != 3 || calls[0]["threadId"] != "t1" || calls[1]["requestId"] != "r1" || calls[2]["action"] != "respond" {
		t.Fatalf("calls = %v", calls)
	}
	ans, _ := calls[2]["answers"].(map[string]any)
	if vals, _ := ans["q"].([]any); len(vals) != 1 || vals[0] != "a" {
		t.Fatalf("answers = %v", calls[2]["answers"])
	}
	if retrySafe("t3_pending_request", map[string]any{"action": "respond"}) || !retrySafe("t3_pending_request", map[string]any{"action": "read"}) {
		t.Fatal("retry policy")
	}
}

func TestFakePendingRequests(t *testing.T) {
	f := NewFake()
	ctx := context.Background()
	p := f.AddProject("P", "/p")
	th, err := f.LaunchThread(ctx, LaunchInput{ProjectID: p.ID, Title: "T"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.AddApproval(th.ThreadID); err != nil {
		t.Fatal(err)
	}
	r1, _ := f.AddUserInput(th.ThreadID, sampleQuestions())
	r2, _ := f.AddUserInput(th.ThreadID, sampleQuestions()[:1])
	d, _ := f.ReadThread(ctx, th.ThreadID)
	ids, _ := f.ListPendingQuestions(ctx, th.ThreadID)
	if d.PendingRequestCount != 3 || !reflect.DeepEqual(ids, []string{r1, r2}) {
		t.Fatalf("count=%d ids=%v", d.PendingRequestCount, ids)
	}
	if err := f.RespondPendingQuestion(ctx, th.ThreadID, r1, map[string]Answer{"scope": TextAnswer("Server"), "extras": ValuesAnswer([]string{"Docs"}), "strict": TextAnswer("Yes")}); err != nil {
		t.Fatal(err)
	}
	if err := f.RespondPendingQuestion(ctx, th.ThreadID, r1, map[string]Answer{"scope": TextAnswer("Server"), "extras": ValuesAnswer([]string{"Docs"}), "strict": TextAnswer("Yes")}); !errors.Is(err, ErrRequestAnswered) {
		t.Fatalf("second respond = %v", err)
	}
	_ = f.ResolveRequest(th.ThreadID, r2, "expired")
	if _, err := f.ReadPendingQuestion(ctx, th.ThreadID, r2); !errors.Is(err, ErrRequestStale) {
		t.Fatalf("expired read = %v", err)
	}
	if _, err := f.ReadPendingQuestion(ctx, th.ThreadID, "nope"); !errors.Is(err, ErrRequestStale) {
		t.Fatalf("unknown read = %v", err)
	}
	if _, err := f.ListPendingQuestions(ctx, "gone"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown thread = %v", err)
	}
	d, _ = f.ReadThread(ctx, th.ThreadID)
	if d.PendingRequestCount != 1 || len(f.Responses) != 1 {
		t.Fatalf("count=%d responses=%d", d.PendingRequestCount, len(f.Responses))
	}
}
