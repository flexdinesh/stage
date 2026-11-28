//go:build codexlive

// Live gates for send and respond (ADR 0006 gate style) against the real
// Codex daemon on this machine. They run model turns on the signed-in
// account in temp directories and delete their threads afterwards. Each
// writes a sanitized transcript to testdata/:
//
//	STAGE_CODEX_GATES=1 go test -tags codexlive -run TestLiveGate -v -timeout 15m ./internal/codex/
package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/flexdinesh/stage/server/internal/execution"
)

const gateTurn = 3 * time.Minute

type gateEntry struct {
	Client string          `json:"client"`
	Dir    string          `json:"dir"`
	Msg    json.RawMessage `json:"msg"`
}

type gateRecorder struct {
	mu      sync.Mutex
	entries []gateEntry
}

// unrecorded are notifications about the machine or account, not the
// protocol under test (they name the host and plan).
var unrecorded = []string{`"remoteControl/status/changed"`, `"account/updated"`, `"configWarning"`}

func (r *gateRecorder) add(client, dir string, b []byte) {
	for _, m := range unrecorded {
		if strings.Contains(string(b), `"method":`+m) {
			return
		}
	}
	r.mu.Lock()
	r.entries = append(r.entries, gateEntry{client, dir, append(json.RawMessage(nil), b...)})
	r.mu.Unlock()
}

// write saves the transcript with the home and work directory replaced.
func (r *gateRecorder) write(t *testing.T, name, work, as string) {
	t.Helper()
	r.mu.Lock()
	b, err := json.MarshalIndent(r.entries, "", "  ")
	r.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	s := strings.ReplaceAll(string(b), work, as)
	if home := os.Getenv("HOME"); home != "" {
		s = strings.ReplaceAll(s, home, "/home/u")
	}
	if err := os.WriteFile("testdata/"+name, []byte(s+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

type gateMsg struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

// gateClient is a raw recording client: every frame in and out is kept.
type gateClient struct {
	t    *testing.T
	name string
	rec  *gateRecorder
	ws   *websocket.Conn

	mu      sync.Mutex
	next    int64
	pending map[string]chan gateMsg
	inbox   []gateMsg
	arrived chan struct{}
}

func gateDial(t *testing.T, sock, name string, rec *gateRecorder) *gateClient {
	t.Helper()
	hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws://localhost/", &websocket.DialOptions{HTTPClient: hc})
	if err != nil {
		t.Fatal(err)
	}
	ws.SetReadLimit(readLimit)
	c := &gateClient{t: t, name: name, rec: rec, ws: ws, pending: map[string]chan gateMsg{}, arrived: make(chan struct{}, 1)}
	go c.read()
	c.call("initialize", initializeParams{ClientInfo: clientInfo{Name: name, Title: "Stage gate", Version: "0.0.1"}, Capabilities: map[string]any{"optOutNotificationMethods": quiet}})
	c.write(map[string]any{"method": "initialized"})
	return c
}

func (c *gateClient) read() {
	for {
		_, b, err := c.ws.Read(context.Background())
		if err != nil {
			return
		}
		c.rec.add(c.name, "in", b)
		var m gateMsg
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		c.mu.Lock()
		if m.Method == "" && len(m.ID) > 0 {
			ch := c.pending[idKey(m.ID)]
			delete(c.pending, idKey(m.ID))
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		c.inbox = append(c.inbox, m)
		c.mu.Unlock()
		select {
		case c.arrived <- struct{}{}:
		default:
		}
	}
}

func (c *gateClient) write(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		c.t.Fatal(err)
	}
	c.rec.add(c.name, "out", b)
	if err := c.ws.Write(context.Background(), websocket.MessageText, b); err != nil {
		c.t.Fatal(err)
	}
}

// call returns the result, or the RPC error (never fatal on one).
func (c *gateClient) call(method string, params any) (json.RawMessage, *RPCError) {
	c.mu.Lock()
	c.next++
	id := c.next
	ch := make(chan gateMsg, 1)
	c.pending[fmt.Sprint(id)] = ch
	c.mu.Unlock()
	c.write(map[string]any{"id": id, "method": method, "params": params})
	select {
	case m := <-ch:
		return m.Result, m.Error
	case <-time.After(time.Minute):
		c.t.Fatalf("%s %s: no reply", c.name, method)
		return nil, nil
	}
}

func (c *gateClient) must(method string, params any) json.RawMessage {
	res, err := c.call(method, params)
	if err != nil {
		c.t.Fatalf("%s %s: %v", c.name, method, err)
	}
	return res
}

// await returns the first inbox message (server request or notification)
// matching pred, consuming everything before it.
func (c *gateClient) await(what string, timeout time.Duration, pred func(gateMsg) bool) gateMsg {
	deadline := time.After(timeout)
	for {
		c.mu.Lock()
		for i, m := range c.inbox {
			if pred(m) {
				c.inbox = c.inbox[i+1:]
				c.mu.Unlock()
				return m
			}
		}
		c.inbox = c.inbox[:0]
		c.mu.Unlock()
		select {
		case <-c.arrived:
		case <-deadline:
			c.t.Fatalf("%s: no %s within %s", c.name, what, timeout)
		}
	}
}

func (c *gateClient) close() { _ = c.ws.Close(websocket.StatusNormalClosure, "") }

func forThread(id string, methods ...string) func(gateMsg) bool {
	return func(m gateMsg) bool {
		if !slices.Contains(methods, m.Method) {
			return false
		}
		var p struct {
			ThreadID string `json:"threadId"`
		}
		_ = json.Unmarshal(m.Params, &p)
		return p.ThreadID == id
	}
}

func statusFlag(id, flag string) func(gateMsg) bool {
	return func(m gateMsg) bool {
		if !forThread(id, "thread/status/changed")(m) {
			return false
		}
		var p struct {
			Status threadStatus `json:"status"`
		}
		_ = json.Unmarshal(m.Params, &p)
		if flag == "" {
			return p.Status.Type == statusIdle
		}
		return slices.Contains(p.Status.ActiveFlags, flag)
	}
}

type gate struct {
	t    *testing.T
	sock string
	rec  *gateRecorder
	work string
}

func newGate(t *testing.T) *gate {
	if os.Getenv("STAGE_CODEX_GATES") != "1" {
		t.Skip("set STAGE_CODEX_GATES=1 to run live Codex gates")
	}
	info, err := CLI{}.Version(t.Context())
	if err == nil && !info.Up() {
		info, err = CLI{}.Start(t.Context())
	}
	if err != nil || !info.Up() {
		t.Fatalf("codex daemon: %+v %v", info, err)
	}
	t.Logf("codex %s app-server %s", info.CLIVersion, info.AppServerVersion)
	return &gate{t: t, sock: info.SocketPath, rec: &gateRecorder{}, work: t.TempDir()}
}

func (g *gate) dial(name string) *gateClient { return gateDial(g.t, g.sock, name, g.rec) }

// start creates a thread and starts its first turn from a launcher that
// unsubscribes and disconnects at once, as Stage's launch does.
func (g *gate) start(threadParams map[string]any, prompt, clientID string) string {
	a := g.dial("stage-launch")
	defer a.close()
	threadParams["cwd"] = g.work
	var th threadResponse
	if err := json.Unmarshal(a.must("thread/start", threadParams), &th); err != nil {
		g.t.Fatal(err)
	}
	g.t.Cleanup(func() {
		c := gateDial(g.t, g.sock, "cleanup", &gateRecorder{})
		defer c.close()
		if _, err := c.call("thread/delete", map[string]any{"threadId": th.Thread.ID}); err != nil {
			g.t.Logf("delete %s: %v", th.Thread.ID, err)
		}
	})
	a.must("turn/start", map[string]any{"threadId": th.Thread.ID, "effort": "low", "clientUserMessageId": clientID,
		"input": []map[string]any{{"type": "text", "text": prompt, "text_elements": []any{}}}})
	a.must("thread/unsubscribe", map[string]any{"threadId": th.Thread.ID})
	g.t.Logf("thread %s", th.Thread.ID)
	return th.Thread.ID
}

// g8: a message to an idle thread no client holds: turn/start alone, then
// thread/resume and turn/start with a client message id, then unsubscribe.
func TestLiveGateSendAfterResume(t *testing.T) {
	g := newGate(t)
	watch := g.dial("bystander")
	defer watch.close()
	id := g.start(map[string]any{"approvalPolicy": "never", "sandbox": "read-only"}, "Reply with exactly OK.", "g8-launch")
	watch.await("idle", gateTurn, statusFlag(id, ""))

	b := g.dial("stage-send")
	defer b.close()
	read := b.must("thread/read", map[string]any{"threadId": id})
	t.Logf("read after launch: %s", read)
	if _, err := b.call("turn/start", map[string]any{"threadId": id, "clientUserMessageId": "g8-direct",
		"input": []map[string]any{{"type": "text", "text": "Reply with exactly TWO.", "text_elements": []any{}}}}); err != nil {
		t.Logf("turn/start without resume: %v", err)
	} else {
		t.Log("turn/start without resume: accepted")
		watch.await("idle", gateTurn, statusFlag(id, ""))
	}
	res := b.must("thread/resume", map[string]any{"threadId": id, "excludeTurns": true})
	var rt threadResponse
	_ = json.Unmarshal(res, &rt)
	t.Logf("resume status %+v", rt.Thread.Status)
	b.must("turn/start", map[string]any{"threadId": id, "clientUserMessageId": "g8-send",
		"input": []map[string]any{{"type": "text", "text": "Reply with exactly THREE.", "text_elements": []any{}}}})
	b.must("thread/unsubscribe", map[string]any{"threadId": id})
	watch.await("idle", gateTurn, statusFlag(id, ""))
	var page turnsPage
	if err := json.Unmarshal(b.must("thread/turns/list", map[string]any{"threadId": id, "limit": 1, "sortDirection": "desc", "itemsView": "full"}), &page); err != nil || len(page.Data) == 0 {
		t.Fatalf("turns %v", err)
	}
	found := false
	for _, it := range page.Data[0].Items {
		if it.Type == itemUserMessage && it.ClientID != nil && *it.ClientID == "g8-send" {
			found = true
		}
	}
	g.rec.write(t, "g8-transcript.json", g.work, "/home/u/work/g8")
	if !found || page.Data[0].Status != turnCompleted {
		t.Fatalf("sent turn %+v", page.Data[0])
	}
}

// g9: approvals no client held, answered from fresh clients after
// thread/resume: a command (accept) and a file change (decline, with a
// second fresh client seeing the same request).
func TestLiveGateApprovalsFromSecondClient(t *testing.T) {
	g := newGate(t)
	watch := g.dial("bystander")
	defer watch.close()
	id := g.start(map[string]any{"approvalPolicy": "untrusted", "sandbox": "read-only"},
		"Run exactly `touch x` in the shell (request escalation if needed), then reply OK.", "g9-launch")
	watch.await("waitingOnApproval", gateTurn, statusFlag(id, "waitingOnApproval"))

	b := g.dial("stage-respond")
	b.must("thread/resume", map[string]any{"threadId": id, "excludeTurns": true})
	req := b.await("command approval replay", 10*time.Second, func(m gateMsg) bool { return m.Method == "item/commandExecution/requestApproval" })
	b.write(map[string]any{"id": req.ID, "result": map[string]any{"decision": "accept"}})
	b.await("serverRequest/resolved", 10*time.Second, forThread(id, "serverRequest/resolved"))
	b.must("thread/unsubscribe", map[string]any{"threadId": id})
	b.close()
	watch.await("idle", gateTurn, statusFlag(id, ""))

	d := g.dial("stage-send")
	d.must("thread/resume", map[string]any{"threadId": id, "excludeTurns": true})
	d.must("turn/start", map[string]any{"threadId": id, "effort": "low", "clientUserMessageId": "g9-patch",
		"input": []map[string]any{{"type": "text", "text": "Use the apply_patch tool (not the shell) to create the file y.txt containing the line hi, then reply OK.", "text_elements": []any{}}}})
	d.must("thread/unsubscribe", map[string]any{"threadId": id})
	d.close()
	watch.await("waitingOnApproval", gateTurn, statusFlag(id, "waitingOnApproval"))

	e := g.dial("stage-respond-2")
	defer e.close()
	f := g.dial("stage-list")
	defer f.close()
	f.must("thread/resume", map[string]any{"threadId": id, "excludeTurns": true})
	fr := f.await("approval replay (list)", 10*time.Second, func(m gateMsg) bool { return strings.HasSuffix(m.Method, "/requestApproval") })
	e.must("thread/resume", map[string]any{"threadId": id, "excludeTurns": true})
	er := e.await("approval replay (respond)", 10*time.Second, func(m gateMsg) bool { return strings.HasSuffix(m.Method, "/requestApproval") })
	t.Logf("replayed %s id %s / %s id %s", fr.Method, fr.ID, er.Method, er.ID)
	e.must("thread/turns/list", map[string]any{"threadId": id, "limit": 1, "sortDirection": "desc", "itemsView": "full"})
	e.write(map[string]any{"id": er.ID, "result": map[string]any{"decision": "decline"}})
	e.await("serverRequest/resolved", 10*time.Second, forThread(id, "serverRequest/resolved"))
	f.await("serverRequest/resolved (list)", 10*time.Second, forThread(id, "serverRequest/resolved"))
	e.must("thread/unsubscribe", map[string]any{"threadId": id})
	f.must("thread/unsubscribe", map[string]any{"threadId": id})
	watch.await("idle", gateTurn, statusFlag(id, ""))
	g.rec.write(t, "g9-transcript.json", g.work, "/home/u/work/g9")
	if er.Method != "item/fileChange/requestApproval" {
		t.Fatalf("second approval was %s, not a file change", er.Method)
	}
}

// g10: a requestUserInput no client held, replayed to a fresh client after
// thread/resume and answered.
func TestLiveGateUserInputReplay(t *testing.T) {
	g := newGate(t)
	watch := g.dial("bystander")
	defer watch.close()
	id := g.start(map[string]any{"approvalPolicy": "never", "sandbox": "read-only", "config": map[string]any{"features.default_mode_request_user_input": true}},
		"Call the request_user_input tool exactly once with one question: id \"color\", header \"Color\", question \"Which color?\", options \"Red\" and \"Blue\". After the answer, reply with exactly the chosen color.", "g10-launch")
	watch.await("waitingOnUserInput", gateTurn, statusFlag(id, "waitingOnUserInput"))

	b := g.dial("stage-respond")
	defer b.close()
	b.must("thread/resume", map[string]any{"threadId": id, "excludeTurns": true})
	req := b.await("requestUserInput replay", 10*time.Second, func(m gateMsg) bool { return m.Method == "item/tool/requestUserInput" })
	var p struct {
		Questions []struct {
			ID      string `json:"id"`
			Options []struct {
				Label string `json:"label"`
			} `json:"options"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil || len(p.Questions) == 0 {
		t.Fatalf("questions %s %v", req.Params, err)
	}
	answers := map[string]any{}
	for _, q := range p.Questions {
		pick := "Blue"
		if len(q.Options) > 0 && !slices.ContainsFunc(q.Options, func(o struct {
			Label string `json:"label"`
		}) bool {
			return o.Label == "Blue"
		}) {
			pick = q.Options[len(q.Options)-1].Label
		}
		answers[q.ID] = map[string]any{"answers": []string{pick}}
	}
	b.write(map[string]any{"id": req.ID, "result": map[string]any{"answers": answers}})
	b.await("serverRequest/resolved", 10*time.Second, forThread(id, "serverRequest/resolved"))
	b.must("thread/unsubscribe", map[string]any{"threadId": id})
	watch.await("idle", gateTurn, statusFlag(id, ""))
	var page turnsPage
	_ = json.Unmarshal(watch.must("thread/turns/list", map[string]any{"threadId": id, "limit": 1, "sortDirection": "desc", "itemsView": "full"}), &page)
	g.rec.write(t, "g10-transcript.json", g.work, "/home/u/work/g10")
	if len(page.Data) == 0 || page.Data[0].Status != turnCompleted {
		t.Fatalf("turn after answer: %+v", page.Data)
	}
}

// TestLiveGateCleanup deletes gate threads an aborted run left behind
// (working directory under the temp dir's TestLive* folders).
func TestLiveGateCleanup(t *testing.T) {
	g := newGate(t)
	c := gateDial(t, g.sock, "cleanup", &gateRecorder{})
	defer c.close()
	prefix := strings.TrimSuffix(os.TempDir(), "/") + "/TestLive"
	var page threadsPage
	if err := json.Unmarshal(c.must("thread/list", map[string]any{"limit": 100}), &page); err != nil {
		t.Fatal(err)
	}
	for _, th := range page.Data {
		if strings.HasPrefix(th.Cwd, prefix) {
			_, err := c.call("thread/delete", map[string]any{"threadId": th.ID})
			t.Logf("delete %s (%s): %v", th.ID, th.Cwd, err)
		}
	}
}

// TestLiveAdapterRoundTrip drives the adapter's interaction against the
// daemon: a thread (started as Stage's launch does) that waits on a command
// approval, Requests and Respond, then Send on the idle thread.
func TestLiveAdapterRoundTrip(t *testing.T) {
	g := newGate(t)
	a := New(NewClient(CLI{}, "verify"), 1, "")
	ctx := t.Context()
	id := g.start(map[string]any{"approvalPolicy": "on-request", "sandbox": "read-only"},
		"Run exactly `touch x` in the shell (request escalation if needed), then reply OK.", "verify-launch")
	ref := execution.SessionRef{ConnectionID: 1, ExternalSessionID: id}
	poll := func(want string) execution.Observation {
		for range 120 {
			o, err := a.Observe(ctx, ref)
			if err == nil && o.State == want {
				return o
			}
			time.Sleep(time.Second)
		}
		t.Fatalf("never %s", want)
		return execution.Observation{}
	}
	poll("needs_input")
	rs, err := a.Requests(ctx, ref)
	if err != nil || len(rs) != 1 || rs[0].Kind != execution.RequestApproval {
		t.Fatalf("requests %+v %v", rs, err)
	}
	t.Logf("request %+v", rs[0])
	if err := a.Send(ctx, ref, execution.Message{Text: "hi"}); err == nil {
		t.Fatal("send while waiting")
	} else {
		t.Logf("send while waiting: %v", err)
	}
	if err := a.Respond(ctx, ref, rs[0].ID, execution.Response{Option: "accept"}); err != nil {
		t.Fatal(err)
	}
	done := poll("done")
	t.Logf("reply %q", done.Reply.Text)
	if rs, err := a.Requests(ctx, ref); err != nil || len(rs) != 0 {
		t.Fatalf("requests after %+v %v", rs, err)
	}
	if err := a.Send(ctx, ref, execution.Message{Text: "Reply with exactly AGAIN.", RequestID: "verify-send"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Send(ctx, ref, execution.Message{Text: "Reply with exactly AGAIN.", RequestID: "verify-send"}); err == nil {
		t.Log("immediate resend: accepted (delivered check)")
	} else {
		t.Logf("immediate resend: %v", err)
	}
	done = poll("done")
	t.Logf("second reply %q (run %s)", done.Reply.Text, done.ExecutionID)
	var page turnsPage
	c := gateDial(t, g.sock, "check", &gateRecorder{})
	defer c.close()
	_ = json.Unmarshal(c.must("thread/turns/list", map[string]any{"threadId": ref.ExternalSessionID, "limit": 10, "sortDirection": "asc", "itemsView": "full"}), &page)
	if len(page.Data) != 2 {
		t.Fatalf("turns %d", len(page.Data))
	}
	if _, err := os.Stat(g.work + "/x"); err != nil {
		t.Fatalf("approved command did not run: %v", err)
	}
}
