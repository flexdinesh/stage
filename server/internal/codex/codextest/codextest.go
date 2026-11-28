// Package codextest is an in-process fake of the Codex app-server daemon:
// a WebSocket JSON-RPC server on a unix socket speaking the subset of the
// protocol the codex adapter uses (codex-cli 0.171.0 shapes, recorded in
// internal/codex/testdata), with knobs for lost replies, restarts,
// sign-out and missing methods.
package codextest

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/flexdinesh/stage/server/internal/codex"
)

// Turn is one fake turn.
type Turn struct {
	ID, Status, ClientID, Prompt, Final string
	Images                              int
	Started, Completed                  int64
}

// Thread is one fake thread.
type Thread struct {
	ID, Cwd, Originator, Name, Preview, Parent, Model string
	Ephemeral, Archived                               bool
	// Status: idle, active, notLoaded or systemError; Flags when active.
	Status    string
	Flags     []string
	Turns     []*Turn
	Created   int64
	Updated   int64
	Effort    string
	Approval  string
	Sandbox   string
	persisted bool
	// requests are the thread's pending server requests (Request).
	requests []*serverRequest
	reqSeq   int
}

type serverRequest struct {
	ID     int
	Method string
	Params map[string]any
}

// Answer is a client's reply to a server request.
type Answer struct {
	Thread, Method string
	ID             int
	Result         json.RawMessage
}

// Server is the fake daemon.
type Server struct {
	Path      string
	CodexHome string

	mu      sync.Mutex
	ln      net.Listener
	srv     *http.Server
	clients map[*wsClient]bool
	threads map[string]*Thread
	seq     int
	now     int64
	models  []map[string]any
	// projects is the user config's projects table; configVersion its
	// layer version (config.go).
	projects      map[string]map[string]any
	configVersion int
	// concurrentWrites: the next batch writes find another client's write
	// first; readonlyConfig refuses writes; overrides are a higher layer's
	// trust levels with its message.
	concurrentWrites int
	readonlyConfig   bool
	overrides        map[string]override
	// SignedOut: account/read reports no account.
	signedOut   bool
	unsupported map[string]bool
	drop        map[string]int
	// turnsNotYet: thread/turns/list calls left that answer "not supported
	// yet" once a first turn started (TurnsNotYet); pendingNotYet arms it.
	turnsNotYet   int
	pendingNotYet int
	calls         map[string]int
	// mcpServers answers mcpServerStatus/list; rateLimits
	// account/rateLimits/read (nil: Codex's auth error).
	mcpServers []map[string]any
	rateLimits map[string]any
	answers    []Answer
}

type wsClient struct {
	ws *websocket.Conn
	mu sync.Mutex
	// subs are the threads the client subscribed to (thread/start,
	// thread/resume; thread/unsubscribe ends one). Guarded by Server.mu.
	subs map[string]bool
}

func (c *wsClient) write(v any) {
	b, _ := json.Marshal(v)
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.ws.Write(ctx, websocket.MessageText, b)
}

// New starts a fake daemon on a short temp socket path; t's cleanup stops it.
func New(t testing.TB) *Server {
	t.Helper()
	dir, err := os.MkdirTemp("", "cdx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	s := &Server{
		Path: filepath.Join(dir, "s.sock"), CodexHome: "/home/u/.codex",
		clients: map[*wsClient]bool{}, threads: map[string]*Thread{}, now: 1_795_000_000,
		projects: map[string]map[string]any{}, overrides: map[string]override{}, unsupported: map[string]bool{}, drop: map[string]int{}, calls: map[string]int{},
		mcpServers: []map[string]any{MCPServer("stage", "")},
		rateLimits: map[string]any{"primary": Window(12, 300, 1_795_003_600), "secondary": Window(30, 10080, 1_795_300_000)},
		models: []map[string]any{
			{"id": "gpt-test", "model": "gpt-test", "displayName": "GPT Test", "hidden": false, "isDefault": true, "defaultReasoningEffort": "low",
				"supportedReasoningEfforts": []map[string]string{{"reasoningEffort": "low", "description": "fast"}, {"reasoningEffort": "high", "description": "deep"}}, "inputModalities": []string{"text", "image"}},
			{"id": "gpt-hidden", "model": "gpt-hidden", "displayName": "Hidden", "hidden": true, "isDefault": false, "defaultReasoningEffort": "low", "supportedReasoningEfforts": []map[string]string{}},
		},
	}
	if err := s.listen(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	return s
}

func (s *Server) listen() error {
	ln, err := net.Listen("unix", s.Path)
	if err != nil {
		return err
	}
	// The server removes the socket itself: a late Serve on an old
	// listener must not unlink a restarted daemon's socket.
	if ul, ok := ln.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}
	srv := &http.Server{Handler: http.HandlerFunc(s.serve), ReadHeaderTimeout: 5 * time.Second}
	s.mu.Lock()
	s.ln, s.srv = ln, srv
	s.mu.Unlock()
	go func() { _ = srv.Serve(ln) }()
	return nil
}

// Stop closes the socket and every client.
func (s *Server) Stop() {
	s.mu.Lock()
	srv := s.srv
	clients := s.clients
	s.srv, s.ln, s.clients = nil, nil, map[*wsClient]bool{}
	s.mu.Unlock()
	for c := range clients {
		_ = c.ws.Close(websocket.StatusGoingAway, "")
	}
	if srv != nil {
		_ = srv.Close()
	}
	_ = os.Remove(s.Path)
}

// Restart stops and starts the daemon: running turns end interrupted,
// threads without a first turn are gone (never persisted), every thread is
// unloaded, and clients must reconnect.
func (s *Server) Restart(t testing.TB) {
	t.Helper()
	s.Stop()
	s.mu.Lock()
	for id, th := range s.threads {
		if !th.persisted {
			delete(s.threads, id)
			continue
		}
		for _, tu := range th.Turns {
			if tu.Status == "inProgress" {
				tu.Status = "interrupted"
			}
		}
		th.Status, th.Flags = "notLoaded", nil
	}
	s.mu.Unlock()
	if err := s.listen(); err != nil {
		t.Fatal(err)
	}
}

// Running reports whether the socket serves.
func (s *Server) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.srv != nil
}

// SetSignedOut sets whether account/read reports no account.
func (s *Server) SetSignedOut(v bool) { s.mu.Lock(); s.signedOut = v; s.mu.Unlock() }

// Unsupported makes method answer Codex's unknown-variant error.
func (s *Server) Unsupported(method string) { s.mu.Lock(); s.unsupported[method] = true; s.mu.Unlock() }

// Supported undoes Unsupported.
func (s *Server) Supported(method string) { s.mu.Lock(); delete(s.unsupported, method); s.mu.Unlock() }

// DropReply performs method's next n calls without answering them.
func (s *Server) DropReply(method string, n int) { s.mu.Lock(); s.drop[method] = n; s.mu.Unlock() }

// TurnsNotYet makes the next n thread/turns/list calls after a thread's
// first turn starts answer Codex's -32601 "list_turns is not supported yet"
// (codex 0.172.0, live in #551: a just-started first turn is not listable
// for a moment).
func (s *Server) TurnsNotYet(n int) { s.mu.Lock(); s.turnsNotYet = n; s.mu.Unlock() }

// SetMCPServers sets the configured MCP servers (MCPServer entries).
func (s *Server) SetMCPServers(v ...map[string]any) { s.mu.Lock(); s.mcpServers = v; s.mu.Unlock() }

// SetRateLimits sets the account's rateLimits snapshot (nil: the read
// fails as for a non-ChatGPT login).
func (s *Server) SetRateLimits(v map[string]any) { s.mu.Lock(); s.rateLimits = v; s.mu.Unlock() }

// MCPServer is a mcpServerStatus/list entry; toolsError "" is none.
func MCPServer(name, toolsError string) map[string]any {
	m := map[string]any{"name": name, "runtimeStatus": nil, "serverInfo": nil, "tools": map[string]any{}, "toolsError": nil, "resources": []any{}, "resourceTemplates": []any{}, "authStatus": "unsupported"}
	if toolsError != "" {
		m["toolsError"] = toolsError
	}
	return m
}

// Window is a rate limit window: percent used, length in minutes and the
// Unix reset time.
func Window(used float64, mins, resetsAt int64) map[string]any {
	return map[string]any{"usedPercent": used, "windowDurationMins": mins, "resetsAt": resetsAt}
}

// Calls is how often method ran.
func (s *Server) Calls(method string) int { s.mu.Lock(); defer s.mu.Unlock(); return s.calls[method] }

// Trust sets a project's trust level in the user config.
func (s *Server) Trust(path, level string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.projects[path] == nil {
		s.projects[path] = map[string]any{}
	}
	s.projects[path]["trust_level"] = level
}

// AddThread adds a persisted thread (another client's: adoption tests).
func (s *Server) AddThread(th Thread) *Thread {
	s.mu.Lock()
	defer s.mu.Unlock()
	th.persisted = true
	if th.Created == 0 {
		th.Created, th.Updated = s.now, s.now
	}
	if th.Status == "" {
		th.Status = "notLoaded"
	}
	p := &th
	s.threads[th.ID] = p
	return p
}

// Thread is a copy of a thread's state (nil: unknown).
func (s *Server) Thread(id string) *Thread {
	s.mu.Lock()
	defer s.mu.Unlock()
	th, ok := s.threads[id]
	if !ok {
		return nil
	}
	c := *th
	c.Turns = make([]*Turn, len(th.Turns))
	for i, tu := range th.Turns {
		x := *tu
		c.Turns[i] = &x
	}
	return &c
}

// Threads are the ids of every thread, in creation order.
func (s *Server) Threads() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.threads))
	for id := range s.threads {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// Wait marks the thread's running turn as waiting on an approval.
func (s *Server) Wait(id string) {
	s.mu.Lock()
	th := s.threads[id]
	th.Status, th.Flags = "active", []string{"waitingOnApproval"}
	s.mu.Unlock()
	s.broadcastStatus(id)
}

// Finish ends the thread's last turn with status and final answer text.
func (s *Server) Finish(id, status, final string) {
	s.mu.Lock()
	th := s.threads[id]
	s.now += 10
	tu := th.Turns[len(th.Turns)-1]
	tu.Status, tu.Final, tu.Completed = status, final, s.now
	th.Status, th.Flags, th.Updated = "idle", nil, s.now
	s.mu.Unlock()
	s.broadcastStatus(id)
	s.broadcast("turn/completed", map[string]any{"threadId": id})
}

// Archive archives the thread as `codex archive` does.
func (s *Server) Archive(id string) {
	s.mu.Lock()
	th := s.threads[id]
	th.Archived, th.Status, th.Flags = true, "notLoaded", nil
	s.mu.Unlock()
	s.broadcastStatus(id)
	s.broadcast("thread/archived", map[string]any{"threadId": id})
}

// SetStatus sets a thread's status directly.
func (s *Server) SetStatus(id, status string) {
	s.mu.Lock()
	s.threads[id].Status = status
	s.mu.Unlock()
}

func (s *Server) broadcastStatus(id string) {
	s.mu.Lock()
	st := statusOf(s.threads[id])
	s.mu.Unlock()
	s.broadcast("thread/status/changed", map[string]any{"threadId": id, "status": st})
}

func (s *Server) broadcast(method string, params any) {
	s.mu.Lock()
	cs := make([]*wsClient, 0, len(s.clients))
	for c := range s.clients {
		cs = append(cs, c)
	}
	s.mu.Unlock()
	for _, c := range cs {
		c.write(map[string]any{"method": method, "params": params})
	}
}

func statusOf(th *Thread) map[string]any {
	st := map[string]any{"type": th.Status}
	if th.Status == "active" {
		flags := th.Flags
		if flags == nil {
			flags = []string{}
		}
		st["activeFlags"] = flags
	}
	return st
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(64 << 20)
	c := &wsClient{ws: ws, subs: map[string]bool{}}
	s.mu.Lock()
	s.clients[c] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, c)
		s.mu.Unlock()
	}()
	initialized := false
	for {
		_, b, err := ws.Read(context.Background())
		if err != nil {
			return
		}
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		if len(m.ID) == 0 {
			continue // notifications (initialized)
		}
		if m.Method == "" {
			s.answered(c, m.ID, b)
			continue
		}
		if m.Method != "initialize" && !initialized {
			c.write(map[string]any{"id": m.ID, "error": map[string]any{"code": -32600, "message": "Not initialized"}})
			continue
		}
		if m.Method == "initialize" {
			initialized = true
		}
		result, rpcErr := s.handle(m.Method, m.Params)
		s.mu.Lock()
		s.calls[m.Method]++
		drop := s.drop[m.Method] > 0
		if drop {
			s.drop[m.Method]--
		}
		s.mu.Unlock()
		if drop {
			continue
		}
		if rpcErr != nil {
			c.write(map[string]any{"id": m.ID, "error": rpcErr})
		} else {
			c.write(map[string]any{"id": m.ID, "result": result})
		}
		if rpcErr == nil {
			s.subscribe(c, m.Method, m.Params, result)
		}
		if m.Method == "turn/start" && rpcErr == nil {
			var p struct{ ThreadID string }
			_ = json.Unmarshal(m.Params, &p)
			s.broadcastStatus(p.ThreadID)
		}
		if m.Method == "thread/archive" && rpcErr == nil {
			var p struct{ ThreadID string }
			_ = json.Unmarshal(m.Params, &p)
			s.broadcastStatus(p.ThreadID)
			s.broadcast("thread/archived", map[string]any{"threadId": p.ThreadID})
		}
	}
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func invalid(format string, args ...any) *rpcError {
	return &rpcError{Code: -32600, Message: fmt.Sprintf(format, args...)}
}

func (s *Server) handle(method string, raw json.RawMessage) (any, *rpcError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unsupported[method] {
		return nil, invalid("Invalid request: unknown variant `%s`, expected one of `initialize`", method)
	}
	var p map[string]json.RawMessage
	_ = json.Unmarshal(raw, &p)
	str := func(k string) string {
		var v string
		_ = json.Unmarshal(p[k], &v)
		return v
	}
	thread := func() (*Thread, *rpcError) {
		id := str("threadId")
		th, ok := s.threads[id]
		if !ok {
			return nil, invalid("thread not loaded: %s", id)
		}
		return th, nil
	}
	switch method {
	case "initialize":
		return map[string]any{"userAgent": "Codex Desktop/0.171.0", "codexHome": s.CodexHome, "platformFamily": "unix", "platformOs": "linux"}, nil
	case "account/read":
		if s.signedOut {
			return map[string]any{"account": nil, "userId": nil, "requiresOpenaiAuth": true}, nil
		}
		return map[string]any{"account": map[string]any{"type": "chatgpt", "email": "u@example.com", "planType": "pro"}, "userId": "u", "requiresOpenaiAuth": true}, nil
	case "model/list":
		return map[string]any{"data": s.models, "nextCursor": nil}, nil
	case "mcpServerStatus/list":
		name := str("serverName")
		data := []map[string]any{}
		for _, m := range s.mcpServers {
			if name == "" || m["name"] == name {
				data = append(data, m)
			}
		}
		return map[string]any{"data": data, "nextCursor": nil}, nil
	case "account/rateLimits/read":
		if s.signedOut || s.rateLimits == nil {
			return nil, invalid("codex account authentication required to read rate limits")
		}
		snap := map[string]any{"limitId": "codex", "limitName": nil, "primary": nil, "secondary": nil, "credits": nil, "planType": "pro", "rateLimitReachedType": nil}
		for k, v := range s.rateLimits {
			snap[k] = v
		}
		return map[string]any{"ordinaryUsageAllowed": true, "rateLimits": snap, "rateLimitsByLimitId": map[string]any{"codex": snap}, "rateLimitResetCredits": nil, "accountId": nil, "rateLimitUpsell": nil}, nil
	case "config/read":
		var includeLayers bool
		_ = json.Unmarshal(p["includeLayers"], &includeLayers)
		return s.configRead(includeLayers), nil
	case "config/batchWrite":
		return s.configBatchWrite(raw)
	case "fs/getMetadata":
		return fsMetadata(str("path"))
	case "fs/readDirectory":
		return fsReadDirectory(str("path"))
	case "thread/start":
		s.seq++
		s.now++
		id := fmt.Sprintf("019a0000-0000-7000-8000-%012d", s.seq)
		th := &Thread{ID: id, Cwd: str("cwd"), Originator: "Codex Desktop", Model: str("model"), Status: "idle", Created: s.now, Updated: s.now, Approval: str("approvalPolicy"), Sandbox: str("sandbox")}
		if th.Model == "" {
			th.Model = "gpt-test"
		}
		s.threads[id] = th
		return map[string]any{"thread": wireThread(th, false), "model": th.Model, "cwd": th.Cwd}, nil
	case "thread/name/set":
		th, e := thread()
		if e != nil {
			return nil, e
		}
		th.Name = str("name")
		return map[string]any{}, nil
	case "thread/resume":
		th, e := thread()
		if e != nil {
			return nil, e
		}
		if th.Archived {
			return nil, invalid("session %s is archived. Run `codex unarchive %s` to unarchive it first.", th.ID, th.ID)
		}
		if th.Status == "notLoaded" {
			th.Status, th.Flags = "idle", nil
		}
		return map[string]any{"thread": wireThread(th, false), "model": th.Model, "cwd": th.Cwd}, nil
	case "turn/start":
		th, e := thread()
		if e != nil {
			return nil, e
		}
		if th.Archived {
			return nil, invalid("session %s is archived", th.ID)
		}
		if th.Status == "notLoaded" {
			return nil, invalid("thread not found: %s", th.ID)
		}
		var input []struct {
			Type, Text, URL string
		}
		_ = json.Unmarshal(p["input"], &input)
		tu := &Turn{ID: fmt.Sprintf("%s-t%d", th.ID[:20], len(th.Turns)+1), Status: "inProgress", ClientID: str("clientUserMessageId")}
		for _, in := range input {
			switch in.Type {
			case "text":
				tu.Prompt = in.Text
			case "image":
				if strings.HasPrefix(in.URL, "data:image/") {
					tu.Images++
				}
			}
		}
		s.now++
		tu.Started = s.now
		if len(th.Turns) == 0 {
			s.pendingNotYet = s.turnsNotYet
		}
		th.Turns = append(th.Turns, tu)
		th.Effort = str("effort")
		th.persisted, th.Status, th.Flags, th.Updated = true, "active", []string{}, s.now
		if th.Preview == "" {
			th.Preview = tu.Prompt
		}
		return map[string]any{"turn": wireTurn(tu, "notLoaded")}, nil
	case "thread/archive":
		th, e := thread()
		if e != nil {
			return nil, e
		}
		if th.Archived {
			return nil, invalid("thread already archived: %s", th.ID)
		}
		th.Archived, th.Status, th.Flags = true, "notLoaded", nil
		return map[string]any{}, nil
	case "thread/read":
		th, e := thread()
		if e != nil {
			return nil, e
		}
		var include bool
		_ = json.Unmarshal(p["includeTurns"], &include)
		return map[string]any{"thread": wireThread(th, include)}, nil
	case "thread/turns/list":
		th, e := thread()
		if e != nil {
			return nil, e
		}
		if s.pendingNotYet > 0 {
			s.pendingNotYet--
			return nil, &rpcError{Code: -32601, Message: "list_turns is not supported yet"}
		}
		turns := slices.Clone(th.Turns)
		if str("sortDirection") == "desc" {
			slices.Reverse(turns)
		}
		var limit int
		_ = json.Unmarshal(p["limit"], &limit)
		if limit > 0 && len(turns) > limit {
			turns = turns[:limit]
		}
		view := str("itemsView")
		out := make([]any, 0, len(turns))
		for _, tu := range turns {
			out = append(out, wireTurn(tu, view))
		}
		return map[string]any{"data": out, "nextCursor": nil, "backwardsCursor": nil}, nil
	case "thread/list":
		var ids []string
		_ = json.Unmarshal(p["includedThreadIds"], &ids)
		var archived bool
		_ = json.Unmarshal(p["archived"], &archived)
		var threads []*Thread
		for id, th := range s.threads {
			if th.persisted && th.Archived == archived && (ids == nil || slices.Contains(ids, id)) {
				threads = append(threads, th)
			}
		}
		slices.SortFunc(threads, func(a, b *Thread) int {
			if n := cmp.Compare(b.Created, a.Created); n != 0 {
				return n
			}
			return cmp.Compare(a.ID, b.ID)
		})
		if cursor := str("cursor"); cursor != "" {
			i := slices.IndexFunc(threads, func(th *Thread) bool { return cursor == "after/"+th.ID })
			if i < 0 {
				return nil, invalid("invalid cursor")
			}
			threads = threads[i+1:]
		}
		var limit int
		_ = json.Unmarshal(p["limit"], &limit)
		var next any
		if limit > 0 && limit < len(threads) {
			threads = threads[:limit]
			next = "after/" + threads[len(threads)-1].ID
		}
		out := make([]any, 0, len(threads))
		for _, th := range threads {
			out = append(out, wireThread(th, false))
		}
		return map[string]any{"data": out, "nextCursor": next, "backwardsCursor": nil}, nil
	case "thread/loaded/list":
		ids := []string{}
		for id, th := range s.threads {
			if th.Status != "notLoaded" {
				ids = append(ids, id)
			}
		}
		slices.Sort(ids)
		return map[string]any{"data": ids, "nextCursor": nil}, nil
	case "thread/unsubscribe":
		return map[string]any{"status": "unsubscribed"}, nil
	case "turn/interrupt":
		th, e := thread()
		if e != nil {
			return nil, e
		}
		for _, tu := range th.Turns {
			if tu.ID == str("turnId") && tu.Status == "inProgress" {
				s.now++
				tu.Status, tu.Completed = "interrupted", s.now
				th.Status, th.Flags = "idle", nil
				return map[string]any{}, nil
			}
		}
		return nil, invalid("no active turn to interrupt")
	}
	return nil, invalid("Invalid request: unknown variant `%s`, expected one of `initialize`", method)
}

// subscribe tracks the client's thread subscriptions after a successful
// call; a resume replays the thread's pending requests to the client after
// the reply, as Codex does.
func (s *Server) subscribe(c *wsClient, method string, params json.RawMessage, result any) {
	var p struct{ ThreadID string }
	_ = json.Unmarshal(params, &p)
	var replay []*serverRequest
	s.mu.Lock()
	switch method {
	case "thread/start":
		if r, ok := result.(map[string]any); ok {
			if th, ok := r["thread"].(map[string]any); ok {
				if id, ok := th["id"].(string); ok {
					c.subs[id] = true
				}
			}
		}
	case "thread/resume":
		c.subs[p.ThreadID] = true
		if th := s.threads[p.ThreadID]; th != nil {
			replay = slices.Clone(th.requests)
		}
	case "thread/unsubscribe":
		delete(c.subs, p.ThreadID)
	}
	s.mu.Unlock()
	for _, r := range replay {
		c.write(map[string]any{"id": r.ID, "method": r.Method, "params": r.Params})
	}
}

// subscribers are the clients subscribed to thread id.
func (s *Server) subscribers(id string) []*wsClient {
	s.mu.Lock()
	defer s.mu.Unlock()
	var cs []*wsClient
	for c := range s.clients {
		if c.subs[id] {
			cs = append(cs, c)
		}
	}
	return cs
}

// Request makes the thread's running turn wait on a server request
// (method and params; threadId is set): its subscribers receive it now, a
// client resuming the thread later gets it replayed. The thread waits on
// approval, or on user input for item/tool/requestUserInput. Returns the
// request id (per thread, from 0).
func (s *Server) Request(id, method string, params map[string]any) int {
	s.mu.Lock()
	th := s.threads[id]
	p := map[string]any{"threadId": id}
	for k, v := range params {
		p[k] = v
	}
	r := &serverRequest{ID: th.reqSeq, Method: method, Params: p}
	th.reqSeq++
	th.requests = append(th.requests, r)
	flag := "waitingOnApproval"
	if method == "item/tool/requestUserInput" {
		flag = "waitingOnUserInput"
	}
	th.Status = "active"
	if !slices.Contains(th.Flags, flag) {
		th.Flags = append(slices.Clone(th.Flags), flag)
	}
	s.mu.Unlock()
	for _, c := range s.subscribers(id) {
		c.write(map[string]any{"id": r.ID, "method": r.Method, "params": r.Params})
	}
	s.broadcastStatus(id)
	return r.ID
}

// answered resolves the server request a client's reply names: the first
// reply wins, subscribers hear serverRequest/resolved, and the thread
// stops waiting once none is left.
func (s *Server) answered(c *wsClient, rawID json.RawMessage, b []byte) {
	var id int
	var m struct {
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(rawID, &id) != nil || json.Unmarshal(b, &m) != nil {
		return
	}
	s.mu.Lock()
	var thread string
	for tid := range c.subs {
		th := s.threads[tid]
		if th == nil {
			continue
		}
		i := slices.IndexFunc(th.requests, func(r *serverRequest) bool { return r.ID == id })
		if i < 0 {
			continue
		}
		s.answers = append(s.answers, Answer{Thread: tid, Method: th.requests[i].Method, ID: id, Result: m.Result})
		th.requests = slices.Delete(th.requests, i, i+1)
		if len(th.requests) == 0 && th.Status == "active" {
			th.Flags = []string{}
		}
		thread = tid
		break
	}
	s.mu.Unlock()
	if thread == "" {
		return
	}
	for _, sc := range s.subscribers(thread) {
		sc.write(map[string]any{"method": "serverRequest/resolved", "params": map[string]any{"threadId": thread, "requestId": id}})
	}
	s.broadcastStatus(thread)
}

// Answers are the replies clients gave to server requests, in order.
func (s *Server) Answers() []Answer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.answers)
}

// Subscribed reports how many clients hold the thread's subscription.
func (s *Server) Subscribed(id string) int { return len(s.subscribers(id)) }

func wireThread(th *Thread, turns bool) map[string]any {
	m := map[string]any{
		"id": th.ID, "preview": th.Preview, "ephemeral": th.Ephemeral, "createdAt": th.Created, "updatedAt": th.Updated,
		"status": statusOf(th), "cwd": th.Cwd, "originator": th.Originator, "model": th.Model,
		"parentThreadId": nil, "name": nil, "gitInfo": map[string]any{"branch": "main"}, "turns": []any{},
	}
	if th.Parent != "" {
		m["parentThreadId"] = th.Parent
	}
	if th.Name != "" {
		m["name"] = th.Name
	}
	if turns {
		ts := make([]any, 0, len(th.Turns))
		for _, tu := range th.Turns {
			ts = append(ts, wireTurn(tu, "full"))
		}
		m["turns"] = ts
	}
	return m
}

func wireTurn(tu *Turn, view string) map[string]any {
	items := []any{}
	if view != "notLoaded" {
		if view == "full" {
			client := any(nil)
			if tu.ClientID != "" {
				client = tu.ClientID
			}
			items = append(items, map[string]any{"type": "userMessage", "id": tu.ID + "-u", "clientId": client,
				"content": []map[string]any{{"type": "text", "text": tu.Prompt, "text_elements": []any{}}}})
		}
		if tu.Final != "" {
			items = append(items,
				map[string]any{"type": "agentMessage", "id": tu.ID + "-c", "text": "Looking.", "phase": "commentary"},
				map[string]any{"type": "agentMessage", "id": tu.ID + "-a", "text": tu.Final, "phase": "final_answer"})
		}
	}
	m := map[string]any{"id": tu.ID, "items": items, "itemsView": view, "status": tu.Status, "error": nil, "startedAt": nil, "completedAt": nil}
	if tu.Started > 0 {
		m["startedAt"] = tu.Started
	}
	if tu.Completed > 0 {
		m["completedAt"] = tu.Completed
	}
	return m
}

// Daemon is a fake codex.Daemon over a Server: it reports the socket while
// the server runs, and Start brings a stopped server back.
type Daemon struct {
	Srv *Server
	T   testing.TB

	mu sync.Mutex
	// Missing: the CLI is not installed.
	Missing bool
	starts  int
}

var _ codex.Daemon = (*Daemon)(nil)

// Starts is how often Start ran.
func (d *Daemon) Starts() int { d.mu.Lock(); defer d.mu.Unlock(); return d.starts }

func (d *Daemon) info(status string) codex.DaemonInfo {
	return codex.DaemonInfo{Status: status, SocketPath: d.Srv.Path, CLIVersion: "0.171.0", AppServerVersion: "0.171.0"}
}

func (d *Daemon) Version(context.Context) (codex.DaemonInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.Missing {
		return codex.DaemonInfo{}, fmt.Errorf("%w: codex", codex.ErrNotInstalled)
	}
	if !d.Srv.Running() {
		return d.info("notRunning"), nil
	}
	return d.info("running"), nil
}

func (d *Daemon) Start(context.Context) (codex.DaemonInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.Missing {
		return codex.DaemonInfo{}, fmt.Errorf("%w: codex", codex.ErrNotInstalled)
	}
	d.starts++
	if d.Srv.Running() {
		return d.info("alreadyRunning"), nil
	}
	if err := d.Srv.listen(); err != nil {
		return codex.DaemonInfo{}, err
	}
	return d.info("started"), nil
}
