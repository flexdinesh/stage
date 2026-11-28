package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"

	"github.com/coder/websocket"
)

// JSON-RPC error codes Codex answers with.
const (
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// readLimit bounds one message: a thread read with turns can be large.
const readLimit = 64 << 20

// RPCError is an error reply from the app-server.
type RPCError struct {
	Method  string `json:"-"`
	Code    int    `json:"code"`
	Message string `json:"message"`
	// Data carries method-specific detail, such as a config write's
	// config_write_error_code.
	Data json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("codex %s: %s (%d)", e.Method, e.Message, e.Code)
}

// errClosed: the connection to the daemon is gone; the call's outcome is
// unknown.
var errClosed = errors.New("codex app-server connection closed")

// notification is a server notification (no id).
type notification struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// envelope is any message on the wire: a response (id with result or
// error), a server request (id with method) or a notification (method).
type envelope struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

type reply struct {
	result json.RawMessage
	err    *RPCError
}

// serverRequest is a request the daemon sends the client (an approval or
// a question); the client answers it with respond and its ID.
type serverRequest struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
}

// conn is one JSON-RPC session over a WebSocket on the daemon's unix
// socket. Messages are JSON text frames without a jsonrpc member. Server
// requests go to request; without one they are left unanswered (another
// client, or the user in Codex, answers them).
type conn struct {
	ws      *websocket.Conn
	notify  func(notification)
	request func(serverRequest)

	mu      sync.Mutex
	next    int64
	pending map[string]chan reply
	done    chan struct{}
	err     error
}

// dial opens a WebSocket on the unix socket at path. notify and request
// (either may be nil) receive notifications and server requests, in order,
// on the read loop.
func dial(ctx context.Context, path string, notify func(notification), request func(serverRequest)) (*conn, error) {
	hc := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		},
	}}
	ws, _, err := websocket.Dial(ctx, "ws://localhost/", &websocket.DialOptions{HTTPClient: hc})
	if err != nil {
		return nil, err
	}
	ws.SetReadLimit(readLimit)
	c := &conn{ws: ws, notify: notify, request: request, pending: map[string]chan reply{}, done: make(chan struct{})}
	go c.read()
	return c, nil
}

func (c *conn) read() {
	ctx := context.Background()
	for {
		_, b, err := c.ws.Read(ctx)
		if err != nil {
			c.fail(err)
			return
		}
		var m envelope
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		switch {
		case m.Method == "" && len(m.ID) > 0:
			c.mu.Lock()
			ch := c.pending[idKey(m.ID)]
			delete(c.pending, idKey(m.ID))
			c.mu.Unlock()
			if ch != nil {
				ch <- reply{result: m.Result, err: m.Error}
			}
		case m.Method != "" && len(m.ID) == 0:
			if c.notify != nil {
				c.notify(notification{Method: m.Method, Params: m.Params})
			}
		case m.Method != "":
			if c.request != nil {
				c.request(serverRequest{ID: m.ID, Method: m.Method, Params: m.Params})
			}
		}
	}
}

// idKey normalizes a numeric or string id.
func idKey(raw json.RawMessage) string {
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return strconv.FormatInt(n, 10)
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

func (c *conn) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.done:
		return
	default:
	}
	c.err = err
	close(c.done)
	for k, ch := range c.pending {
		close(ch)
		delete(c.pending, k)
	}
}

// alive reports whether the connection still reads.
func (c *conn) alive() bool {
	select {
	case <-c.done:
		return false
	default:
		return true
	}
}

func (c *conn) close() {
	_ = c.ws.Close(websocket.StatusNormalClosure, "")
	c.fail(errClosed)
}

// call sends method and decodes its result into out (nil: discard).
// errClosed (wrapped): the outcome is unknown.
func (c *conn) call(ctx context.Context, method string, params, out any) error {
	ch := make(chan reply, 1)
	c.mu.Lock()
	if !c.alive() {
		c.mu.Unlock()
		return fmt.Errorf("%w: %v", errClosed, c.err)
	}
	c.next++
	id := c.next
	c.pending[strconv.FormatInt(id, 10)] = ch
	c.mu.Unlock()
	msg := map[string]any{"id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	b, err := json.Marshal(msg)
	if err != nil {
		c.forget(id)
		return err
	}
	if err := c.ws.Write(ctx, websocket.MessageText, b); err != nil {
		c.forget(id)
		return fmt.Errorf("%w: %v", errClosed, err)
	}
	select {
	case r, ok := <-ch:
		if !ok {
			return fmt.Errorf("%w: %v", errClosed, c.err)
		}
		if r.err != nil {
			r.err.Method = method
			return r.err
		}
		if out == nil || len(r.result) == 0 {
			return nil
		}
		if err := json.Unmarshal(r.result, out); err != nil {
			return fmt.Errorf("codex %s: malformed result: %w", method, err)
		}
		return nil
	case <-ctx.Done():
		c.forget(id)
		return ctx.Err()
	}
}

// send writes a notification.
func (c *conn) send(ctx context.Context, method string, params any) error {
	msg := map[string]any{"method": method}
	if params != nil {
		msg["params"] = params
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return c.ws.Write(ctx, websocket.MessageText, b)
}

// respond answers the server request id with result.
func (c *conn) respond(ctx context.Context, id json.RawMessage, result any) error {
	b, err := json.Marshal(map[string]any{"id": id, "result": result})
	if err != nil {
		return err
	}
	if err := c.ws.Write(ctx, websocket.MessageText, b); err != nil {
		return fmt.Errorf("%w: %v", errClosed, err)
	}
	return nil
}

func (c *conn) forget(id int64) {
	c.mu.Lock()
	delete(c.pending, strconv.FormatInt(id, 10))
	c.mu.Unlock()
}
