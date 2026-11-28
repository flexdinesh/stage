package codex_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// launched starts a thread through the adapter, as a Stage launch does
// (Stage unsubscribes after the first turn starts).
func launched(t *testing.T, r rig) execution.SessionRef {
	t.Helper()
	var cps checkpoints
	o, err := r.adapter.Launch(t.Context(), intent("/w/app"), execution.Checkpoint{}, cps.save)
	if err != nil {
		t.Fatal(err)
	}
	return o.Session
}

// Recorded request shapes (codex-cli 0.172.0, gates g9 and g10).
func commandApproval() map[string]any {
	return map[string]any{
		"kind": "command", "turnId": "t1", "itemId": "exec-1", "startedAtMs": 1795860205938, "environmentId": "local",
		"reason": "Allow creating x with the requested touch x command?", "command": "/usr/bin/bash -lc 'touch x'", "cwd": "/w/app",
		"availableDecisions": []any{"accept", map[string]any{"acceptWithExecpolicyAmendment": map[string]any{"execpolicy_amendment": []string{"touch", "x"}}}, "cancel"},
	}
}

func fileApproval() map[string]any {
	return map[string]any{"turnId": "t1", "itemId": "exec-2", "startedAtMs": 1795860210055, "reason": nil, "grantRoot": nil}
}

func userInput(secret bool) map[string]any {
	return map[string]any{"turnId": "t1", "itemId": "call_3a5f", "isBlocking": false, "autoResolutionMs": nil, "questions": []any{
		map[string]any{"id": "color", "header": "Color", "question": "Which color?", "isOther": true, "isSecret": false,
			"options": []any{map[string]any{"label": "Red", "description": "Choose Red."}, map[string]any{"label": "Blue", "description": "Choose Blue."}}},
		map[string]any{"id": "token", "header": "Token", "question": "Paste the token", "isOther": true, "isSecret": secret, "options": nil},
	}}
}

func TestRequestsListAnswerableKindsWithCodexWords(t *testing.T) {
	r := newRig(t)
	ref := launched(t, r)
	r.srv.Request(ref.ExternalSessionID, "item/commandExecution/requestApproval", commandApproval())
	r.srv.Request(ref.ExternalSessionID, "item/fileChange/requestApproval", fileApproval())
	r.srv.Request(ref.ExternalSessionID, "item/tool/requestUserInput", userInput(false))
	// Answered in Codex: permissions, MCP elicitations, secret questions.
	r.srv.Request(ref.ExternalSessionID, "item/permissions/requestApproval", map[string]any{"itemId": "perm-1", "permissions": map[string]any{}})
	r.srv.Request(ref.ExternalSessionID, "mcpServer/elicitation/request", map[string]any{"serverName": "x", "message": "?"})
	r.srv.Request(ref.ExternalSessionID, "item/tool/requestUserInput", map[string]any{"itemId": "call_secret", "questions": userInput(true)["questions"]})

	rs, err := r.adapter.Requests(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 3 {
		t.Fatalf("requests %+v", rs)
	}
	cmd := rs[0]
	if cmd.ID != "exec-1" || cmd.Kind != execution.RequestApproval || cmd.Title != "Run a command" || cmd.Preview != "/usr/bin/bash -lc 'touch x'" ||
		cmd.Detail != "Allow creating x with the requested touch x command?" || cmd.CreatedAt.IsZero() || cmd.TextOption != "" {
		t.Fatalf("command %+v", cmd)
	}
	// Offered plain decisions only, in Codex's order (the amendment stays in Codex).
	if ids := choiceIDs(cmd.Options); !slices.Equal(ids, []string{"accept", "cancel"}) || cmd.Options[0].Label != "Run command" || cmd.Options[1].Label != "Stop the turn" {
		t.Fatalf("command options %+v", cmd.Options)
	}
	file := rs[1]
	if file.ID != "exec-2" || file.Title != "Edit files" || file.Preview != "" || file.Detail == "" {
		t.Fatalf("file change %+v", file)
	}
	if ids := choiceIDs(file.Options); !slices.Equal(ids, []string{"accept", "acceptForSession", "decline", "cancel"}) || file.Options[1].Label != "Apply, and don't ask again for these files" {
		t.Fatalf("file change options %+v", file.Options)
	}
	q := rs[2]
	if q.ID != "call_3a5f" || q.Kind != execution.RequestQuestion || q.Title != "Codex asks 2 questions" || len(q.Questions) != 2 {
		t.Fatalf("question %+v", q)
	}
	if c := q.Questions[0]; c.ID != "color" || c.Header != "Color" || c.Prompt != "Which color?" || !c.Custom || len(c.Options) != 2 || c.Options[1].Description != "Choose Blue." {
		t.Fatalf("color %+v", c)
	}
	if tok := q.Questions[1]; !tok.Custom || len(tok.Options) != 0 {
		t.Fatalf("free text %+v", tok)
	}
	if n := r.srv.Subscribed(ref.ExternalSessionID); n != 0 {
		t.Fatalf("still subscribed: %d", n)
	}
}

func choiceIDs(cs []execution.Choice) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}

func TestRequestsOfAThreadWaitingOnNothingDoNotResume(t *testing.T) {
	r := newRig(t)
	ref := launched(t, r)
	rs, err := r.adapter.Requests(t.Context(), ref)
	if err != nil || len(rs) != 0 {
		t.Fatalf("%+v %v", rs, err)
	}
	if n := r.srv.Calls("thread/resume"); n != 0 {
		t.Fatalf("resumed %d times", n)
	}
	if _, err := r.adapter.Requests(t.Context(), execution.SessionRef{ConnectionID: conn, ExternalSessionID: "bad"}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatal("bad ref", err)
	}
}

func TestRespondAnswersApprovalAndClearsIt(t *testing.T) {
	r := newRig(t)
	ref := launched(t, r)
	id := r.srv.Request(ref.ExternalSessionID, "item/commandExecution/requestApproval", commandApproval())
	if err := r.adapter.Respond(t.Context(), ref, "exec-1", execution.Response{Option: "accept"}); err != nil {
		t.Fatal(err)
	}
	as := r.srv.Answers()
	if len(as) != 1 || as[0].ID != id || as[0].Method != "item/commandExecution/requestApproval" || string(as[0].Result) != `{"decision":"accept"}` {
		t.Fatalf("answers %+v", as)
	}
	if th := r.srv.Thread(ref.ExternalSessionID); th.Status != "active" || len(th.Flags) != 0 {
		t.Fatalf("thread still waits %+v", th)
	}
	// Answered: no longer pending.
	if err := r.adapter.Respond(t.Context(), ref, "exec-1", execution.Response{Option: "accept"}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatal("second answer", err)
	}
	if n := r.srv.Subscribed(ref.ExternalSessionID); n != 0 {
		t.Fatalf("still subscribed: %d", n)
	}
}

func TestRespondRefusesAnswersThatDoNotFit(t *testing.T) {
	r := newRig(t)
	ref := launched(t, r)
	r.srv.Request(ref.ExternalSessionID, "item/commandExecution/requestApproval", commandApproval())
	r.srv.Request(ref.ExternalSessionID, "item/tool/requestUserInput", userInput(false))
	r.srv.Request(ref.ExternalSessionID, "item/permissions/requestApproval", map[string]any{"itemId": "perm-1"})
	var invalid *execution.Invalid
	for name, tc := range map[string]struct {
		id string
		r  execution.Response
	}{
		"not offered":       {"exec-1", execution.Response{Option: "decline"}},
		"approval text":     {"exec-1", execution.Response{Option: "accept", Text: "because"}},
		"question option":   {"call_3a5f", execution.Response{Option: "accept"}},
		"missing answer":    {"call_3a5f", execution.Response{Answers: map[string][]string{"color": {"Red"}}}},
		"unknown question":  {"call_3a5f", execution.Response{Answers: map[string][]string{"color": {"Red"}, "token": {"t"}, "size": {"L"}}}},
		"two single answer": {"call_3a5f", execution.Response{Answers: map[string][]string{"color": {"Red", "Blue"}, "token": {"t"}}}},
	} {
		if err := r.adapter.Respond(t.Context(), ref, tc.id, tc.r); !errors.As(err, &invalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if err := r.adapter.Respond(t.Context(), ref, "perm-1", execution.Response{Option: "accept"}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatal("unlisted kind answered", err)
	}
	if as := r.srv.Answers(); len(as) != 0 {
		t.Fatalf("sent %+v", as)
	}
}

func TestRespondAnswersQuestionsInCodexShape(t *testing.T) {
	r := newRig(t)
	ref := launched(t, r)
	r.srv.Request(ref.ExternalSessionID, "item/tool/requestUserInput", userInput(false))
	err := r.adapter.Respond(t.Context(), ref, "call_3a5f", execution.Response{Answers: map[string][]string{"color": {"Blue"}, "token": {"abc"}}})
	if err != nil {
		t.Fatal(err)
	}
	as := r.srv.Answers()
	if len(as) != 1 {
		t.Fatalf("answers %+v", as)
	}
	var got map[string]map[string]map[string][]string
	if err := json.Unmarshal(as[0].Result, &got); err != nil {
		t.Fatal(err)
	}
	if got["answers"]["color"]["answers"][0] != "Blue" || got["answers"]["token"]["answers"][0] != "abc" {
		t.Fatalf("result %s", as[0].Result)
	}
	if th := r.srv.Thread(ref.ExternalSessionID); len(th.Flags) != 0 {
		t.Fatalf("thread still waits %+v", th.Flags)
	}
}

func TestSendIsBusyWhileATurnRuns(t *testing.T) {
	r := newRig(t)
	ref := launched(t, r)
	var busy *execution.Busy
	if err := r.adapter.Send(t.Context(), ref, execution.Message{Text: "more"}); !errors.As(err, &busy) || busy.Reason != "Codex is working. Send once it finishes." {
		t.Fatal("running", err)
	}
	r.srv.Request(ref.ExternalSessionID, "item/commandExecution/requestApproval", commandApproval())
	if err := r.adapter.Send(t.Context(), ref, execution.Message{Text: "more"}); !errors.As(err, &busy) || busy.Reason != "Codex is waiting on an approval or question. Answer that first." {
		t.Fatal("waiting", err)
	}
	if n := len(r.srv.Thread(ref.ExternalSessionID).Turns); n != 1 || r.srv.Calls("thread/resume") != 0 {
		t.Fatalf("turns %d resumes %d", n, r.srv.Calls("thread/resume"))
	}
}

func TestSendStartsATurnOnAnIdleThread(t *testing.T) {
	r := newRig(t)
	ref := launched(t, r)
	r.srv.Finish(ref.ExternalSessionID, "completed", "Done.")
	if err := r.adapter.Send(t.Context(), ref, execution.Message{Text: "also run lint", RequestID: "steering-7-12"}); err != nil {
		t.Fatal(err)
	}
	th := r.srv.Thread(ref.ExternalSessionID)
	if len(th.Turns) != 2 || th.Turns[1].Prompt != "also run lint" || th.Turns[1].ClientID != "steering-7-12" || th.Status != "active" {
		t.Fatalf("thread %+v turn %+v", th, th.Turns[len(th.Turns)-1])
	}
	if r.srv.Calls("thread/resume") != 1 || r.srv.Subscribed(ref.ExternalSessionID) != 0 {
		t.Fatalf("resumes %d subscribed %d", r.srv.Calls("thread/resume"), r.srv.Subscribed(ref.ExternalSessionID))
	}
	// A retry of the same send finds it delivered.
	r.srv.Finish(ref.ExternalSessionID, "completed", "Linted.")
	if err := r.adapter.Send(t.Context(), ref, execution.Message{Text: "also run lint", RequestID: "steering-7-12"}); err != nil {
		t.Fatal(err)
	}
	if n := len(r.srv.Thread(ref.ExternalSessionID).Turns); n != 2 {
		t.Fatalf("retry started turn %d", n)
	}
	// Without a request id, Stage still names the message.
	if err := r.adapter.Send(t.Context(), ref, execution.Message{Text: "thanks"}); err != nil {
		t.Fatal(err)
	}
	if tu := r.srv.Thread(ref.ExternalSessionID).Turns[2]; tu.ClientID == "" || tu.Prompt != "thanks" {
		t.Fatalf("turn %+v", tu)
	}
}

func TestSendResumesAThreadTheDaemonUnloaded(t *testing.T) {
	r := newRig(t)
	ref := launched(t, r)
	r.srv.Finish(ref.ExternalSessionID, "completed", "Done.")
	r.srv.Restart(t)
	if th := r.srv.Thread(ref.ExternalSessionID); th.Status != "notLoaded" {
		t.Fatalf("status %s", th.Status)
	}
	if err := r.adapter.Send(t.Context(), ref, execution.Message{Text: "continue"}); err != nil {
		t.Fatal(err)
	}
	if th := r.srv.Thread(ref.ExternalSessionID); len(th.Turns) != 2 || th.Status != "active" {
		t.Fatalf("thread %+v", th)
	}
}

func TestSendToAnArchivedThreadIsRefusedWithCodexsFix(t *testing.T) {
	r := newRig(t)
	ref := launched(t, r)
	r.srv.Finish(ref.ExternalSessionID, "completed", "Done.")
	r.srv.Archive(ref.ExternalSessionID)
	var refused *execution.Refused
	if err := r.adapter.Send(t.Context(), ref, execution.Message{Text: "more"}); !errors.As(err, &refused) || !strings.Contains(refused.Reason, "codex unarchive") {
		t.Fatal("archived", err)
	}
	if err := r.adapter.Send(t.Context(), execution.SessionRef{ConnectionID: conn, ExternalSessionID: "019a0000-0000-7000-8000-00000000ffff"}, execution.Message{Text: "hi"}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatal("unknown thread", err)
	}
}
