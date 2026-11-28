package codex

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
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

// The registry keeps one thread's requests in arrival order until
// serverRequest/resolved names them; other threads are ignored.
func TestPendingRegistryLifecycle(t *testing.T) {
	p := newPending("t1")
	req := func(id int, thread string) serverRequest {
		params, _ := json.Marshal(map[string]any{"threadId": thread, "itemId": "i"})
		raw, _ := json.Marshal(id)
		return serverRequest{ID: raw, Method: methodCommandApproval, Params: params}
	}
	resolved := func(id int, thread string) notification {
		params, _ := json.Marshal(map[string]any{"threadId": thread, "requestId": id})
		return notification{Method: notifyResolved, Params: params}
	}
	p.add(req(1, "t1"))
	p.add(req(0, "t1"))
	p.add(req(5, "t2"))
	p.add(req(1, "t1")) // a replay of a request already held
	if got := p.list(); len(got) != 2 || idKey(got[0].ID) != "1" || idKey(got[1].ID) != "0" {
		t.Fatalf("list %+v", got)
	}
	p.notify(resolved(0, "t2"))
	p.notify(notification{Method: "thread/status/changed", Params: json.RawMessage(`{"threadId":"t1","requestId":1}`)})
	if p.size() != 2 {
		t.Fatalf("cleared by another thread or method: %d", p.size())
	}
	p.notify(resolved(1, "t1"))
	if got := p.list(); len(got) != 1 || idKey(got[0].ID) != "0" {
		t.Fatalf("after resolve %+v", got)
	}
}

// settle returns after the quiet spell once a request arrived, and at the
// cap when none does.
func TestPendingSettleWaitsForReplay(t *testing.T) {
	quiet, wait := replayQuiet, replayWait
	replayQuiet, replayWait = 20*time.Millisecond, 200*time.Millisecond
	t.Cleanup(func() { replayQuiet, replayWait = quiet, wait })

	p := newPending("t1")
	go func() {
		time.Sleep(10 * time.Millisecond)
		p.add(serverRequest{ID: json.RawMessage(`0`), Params: json.RawMessage(`{"threadId":"t1"}`)})
	}()
	start := time.Now()
	if err := p.settle(t.Context()); err != nil || p.size() != 1 {
		t.Fatalf("settle %v size %d", err, p.size())
	}
	if d := time.Since(start); d >= replayWait {
		t.Fatalf("waited the cap (%s) despite a request", d)
	}
	start = time.Now()
	if err := newPending("t1").settle(t.Context()); err != nil || time.Since(start) < replayWait {
		t.Fatalf("empty settle %v after %s", err, time.Since(start))
	}
}

// The requests the live gates recorded (codex-cli 0.172.0) map to Stage's
// requests, and the answers the gates sent are the ones answer builds.
func TestRecordedServerRequestsMap(t *testing.T) {
	type entry struct {
		Client string
		Dir    string
		Msg    gateLike
	}
	load := func(name string) []entry {
		var es []entry
		fixture(t, name, &es)
		return es
	}
	var got []execution.Request
	sent := map[string]string{}
	for _, name := range []string{"g9-transcript.json", "g10-transcript.json"} {
		var reqs []serverRequest
		for _, e := range load(name) {
			switch {
			case e.Dir == "in" && e.Msg.Method != "" && len(e.Msg.ID) > 0 && (e.Client == "stage-respond" || e.Client == "stage-respond-2"):
				reqs = append(reqs, serverRequest{ID: e.Msg.ID, Method: e.Msg.Method, Params: e.Msg.Params})
			case e.Dir == "out" && e.Msg.Method == "" && len(e.Msg.ID) > 0 && len(e.Msg.Result) > 0 && (e.Client == "stage-respond" || e.Client == "stage-respond-2"):
				sent[name+"/"+idKey(e.Msg.ID)] = string(e.Msg.Result)
			}
		}
		for _, r := range reqs {
			req, ok := listed(r)
			if !ok {
				t.Fatalf("%s: %s not listed", name, r.Method)
			}
			got = append(got, req)
			var resp execution.Response
			switch {
			case r.Method == methodCommandApproval:
				resp.Option = "accept"
			case r.Method == methodFileApproval:
				resp.Option = "decline"
			default:
				resp.Answers = map[string][]string{"color": {"Blue"}}
			}
			result, err := answer(r, req, resp)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(result)
			var want any
			_ = json.Unmarshal([]byte(sent[name+"/"+idKey(r.ID)]), &want)
			if wb, _ := json.Marshal(want); string(wb) != string(b) {
				t.Fatalf("%s %s: answer %s, gate sent %s", name, r.Method, b, wb)
			}
		}
	}
	if len(got) != 3 || got[0].Title != "Run a command" || len(got[0].Options) != 2 || got[1].Title != "Edit files" || got[2].Kind != execution.RequestQuestion || got[2].Questions[0].Options[1].Label != "Blue" {
		t.Fatalf("mapped %+v", got)
	}
}

type gateLike struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
}
