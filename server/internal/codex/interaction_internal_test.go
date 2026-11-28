package codex

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// A server request (method and id) reaches the request handler, not the
// notification handler, and respond answers it by its id.
func TestConnRoutesServerRequestsAndResponds(t *testing.T) {
	dir, err := os.MkdirTemp("", "cdx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	replies := make(chan []byte, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := context.Background()
		_ = ws.Write(ctx, websocket.MessageText, []byte(`{"method":"thread/status/changed","params":{"threadId":"t"}}`))
		_ = ws.Write(ctx, websocket.MessageText, []byte(`{"id":7,"method":"item/commandExecution/requestApproval","params":{"threadId":"t","itemId":"exec-1"}}`))
		_, b, err := ws.Read(ctx)
		if err == nil {
			replies <- b
		}
		_ = ws.Close(websocket.StatusNormalClosure, "")
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	notes := make(chan notification, 4)
	reqs := make(chan serverRequest, 4)
	c, err := dial(t.Context(), sock, func(n notification) { notes <- n }, func(r serverRequest) { reqs <- r })
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	var r serverRequest
	select {
	case r = <-reqs:
	case <-time.After(5 * time.Second):
		t.Fatal("no server request")
	}
	if idKey(r.ID) != "7" || r.Method != methodCommandApproval {
		t.Fatalf("request %+v", r)
	}
	if n := <-notes; n.Method != "thread/status/changed" || len(notes) != 0 {
		t.Fatalf("notification %+v (+%d)", n, len(notes))
	}
	if err := c.respond(t.Context(), r.ID, map[string]any{"decision": "accept"}); err != nil {
		t.Fatal(err)
	}
	select {
	case b := <-replies:
		var got struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(b, &got); err != nil || got.ID != 7 || got.Method != "" || string(got.Result) != `{"decision":"accept"}` {
			t.Fatalf("reply %s %v", b, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no reply")
	}
}

// These sanitized schema fixtures exercise every wire decision, including
// two amendments of the same kind and the empty profile used to deny.
func TestApprovalDecisionPayloads(t *testing.T) {
	var frames []envelope
	fixture(t, "approval-decisions.json", &frames)
	approvals := make([]*approval, 0, len(frames))
	for _, f := range frames {
		_, a := decodeApproval(serverRequest{ID: f.ID, Method: f.Method, Params: f.Params})
		if a == nil {
			t.Fatalf("request %s was not listed", f.Method)
		}
		approvals = append(approvals, a)
	}
	cmd, permissions := approvals[0], approvals[1]
	var choices []string
	for _, c := range cmd.request.Options {
		choices = append(choices, c.ID)
	}
	if !slices.Equal(choices, []string{"accept", "acceptForSession", "acceptWithExecpolicyAmendment",
		"applyNetworkPolicyAmendment:allow:example.test", "applyNetworkPolicyAmendment:deny:example.test", "decline", "cancel"}) {
		t.Fatal(choices)
	}
	assertResult := func(a *approval, option, want string) {
		t.Helper()
		result, err := a.answer(execution.Response{Option: option})
		got, marshalErr := json.Marshal(result)
		if err != nil || marshalErr != nil || string(got) != want {
			t.Fatalf("%s = %s, want %s (%v, %v)", option, got, want, err, marshalErr)
		}
	}
	assertResult(cmd, "acceptWithExecpolicyAmendment", `{"decision":{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["touch","x"]}}}`)
	assertResult(cmd, "applyNetworkPolicyAmendment:deny:example.test", `{"decision":{"applyNetworkPolicyAmendment":{"network_policy_amendment":{"host":"example.test","action":"deny"}}}}`)
	assertResult(permissions, "turn", `{"permissions":{"fileSystem":{"read":["/home/u/work"],"write":["/home/u/work"]}},"scope":"turn"}`)
	assertResult(permissions, "decline", `{"permissions":{},"scope":"turn"}`)
}
