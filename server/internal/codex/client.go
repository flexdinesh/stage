package codex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// startBackoff spaces automatic daemon starts after one fails or the
// daemon stops again.
const startBackoff = 30 * time.Second

// quiet are high-volume notifications Stage never reads.
var quiet = []string{
	"item/agentMessage/delta", "item/commandExecution/outputDelta", "item/fileChange/outputDelta",
	"item/plan/delta", "item/reasoning/summaryTextDelta", "item/reasoning/textDelta",
	"thread/tokenUsage/updated", "turn/diff/updated", "turn/plan/updated",
}

// Client is one connection to the Codex daemon, dialed on demand: it
// starts the daemon when it is not running (at most once per
// startBackoff), connects to its socket and initializes. A dropped
// connection is redialed by the next call.
type Client struct {
	daemon  Daemon
	version string
	now     func() time.Time

	mu        sync.Mutex
	c         *conn
	init      initializeResponse
	info      DaemonInfo
	lastStart time.Time
	listeners map[int]func(notification)
	nextL     int
}

// NewClient connects through daemon; version is Stage's, sent as clientInfo.
func NewClient(daemon Daemon, version string) *Client {
	return &Client{daemon: daemon, version: version, now: time.Now, listeners: map[int]func(notification){}}
}

// DaemonDownError: the daemon is not serving and Stage did not (or could
// not) start it.
type DaemonDownError struct{ Reason string }

func (e *DaemonDownError) Error() string { return e.Reason }

func (cl *Client) connect(ctx context.Context) (*conn, error) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.c != nil && cl.c.alive() {
		return cl.c, nil
	}
	cl.c = nil
	info, err := cl.daemon.Version(ctx)
	if err != nil {
		return nil, err
	}
	if !info.Up() {
		if !cl.lastStart.IsZero() && cl.now().Sub(cl.lastStart) < startBackoff {
			return nil, &DaemonDownError{Reason: "The Codex daemon is not running; Stage retries starting it shortly."}
		}
		cl.lastStart = cl.now()
		if info, err = cl.daemon.Start(ctx); err != nil {
			return nil, &DaemonDownError{Reason: "Stage could not start the Codex daemon: " + err.Error()}
		}
		if !info.Up() {
			return nil, &DaemonDownError{Reason: fmt.Sprintf("The Codex daemon did not start (status %q).", info.Status)}
		}
	}
	cl.info = info
	c, err := dial(ctx, info.SocketPath, cl.dispatch)
	if err != nil {
		return nil, &DaemonDownError{Reason: "Stage could not reach the Codex daemon: " + err.Error()}
	}
	var res initializeResponse
	params := initializeParams{
		ClientInfo:   clientInfo{Name: ClientName, Title: "Stage", Version: cl.version},
		Capabilities: map[string]any{"optOutNotificationMethods": quiet},
	}
	if err := c.call(ctx, "initialize", params, &res); err != nil {
		c.close()
		return nil, err
	}
	if res.CodexHome == "" {
		c.close()
		return nil, errors.New("codex initialize: missing codexHome")
	}
	if err := c.send(ctx, "initialized", nil); err != nil {
		c.close()
		return nil, err
	}
	cl.c, cl.init = c, res
	return c, nil
}

func (cl *Client) dispatch(n notification) {
	cl.mu.Lock()
	fns := make([]func(notification), 0, len(cl.listeners))
	for _, fn := range cl.listeners {
		fns = append(fns, fn)
	}
	cl.mu.Unlock()
	for _, fn := range fns {
		fn(n)
	}
}

// listen registers fn for every notification until the returned func runs.
func (cl *Client) listen(fn func(notification)) func() {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	id := cl.nextL
	cl.nextL++
	cl.listeners[id] = fn
	return func() {
		cl.mu.Lock()
		delete(cl.listeners, id)
		cl.mu.Unlock()
	}
}

// done is closed when the current connection drops (nil: none).
func (cl *Client) done() <-chan struct{} {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.c == nil {
		return nil
	}
	return cl.c.done
}

// call runs method on the daemon. Reaching the daemon failed:
// execution.ErrUnavailable. A dropped connection mid-call: the outcome is
// unknown (also ErrUnavailable, wrapping errClosed).
func (cl *Client) call(ctx context.Context, method string, params, out any) error {
	c, err := cl.connect(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", execution.ErrUnavailable, err)
	}
	err = c.call(ctx, method, params, out)
	if errors.Is(err, errClosed) {
		cl.mu.Lock()
		if cl.c == c {
			cl.c = nil
		}
		cl.mu.Unlock()
		return fmt.Errorf("%w: %w", execution.ErrUnavailable, err)
	}
	return err
}

// Close drops the connection; the daemon keeps running.
func (cl *Client) Close() {
	cl.mu.Lock()
	c := cl.c
	cl.c = nil
	cl.mu.Unlock()
	if c != nil {
		c.close()
	}
}

// Probe is the daemon and account state a connect action and health read.
type Probe struct {
	Daemon    DaemonInfo
	CodexHome string
	UserAgent string
	// Account: nil when signed out.
	Account      *Account
	RequiresAuth bool
}

// Account is the Codex login (Stage stores no Codex credentials).
type Account struct {
	Type  string  `json:"type"`
	Email *string `json:"email"`
	Plan  *string `json:"plan"`
}

// SignedIn: Codex can run turns with its current login.
func (p Probe) SignedIn() bool { return p.Account != nil || !p.RequiresAuth }

// Label names the instance: the account email, else the Codex home.
func (p Probe) Label() string {
	if p.Account != nil && p.Account.Email != nil && *p.Account.Email != "" {
		return "Codex · " + *p.Account.Email
	}
	return "Codex · " + p.CodexHome
}

// Probe connects (starting the daemon when needed) and reads the account.
func (cl *Client) Probe(ctx context.Context) (Probe, error) {
	var acct accountResponse
	if err := cl.call(ctx, "account/read", map[string]any{}, &acct); err != nil {
		return Probe{}, err
	}
	cl.mu.Lock()
	p := Probe{Daemon: cl.info, CodexHome: cl.init.CodexHome, UserAgent: cl.init.UserAgent, RequiresAuth: acct.RequiresOpenaiAuth}
	cl.mu.Unlock()
	if acct.Account != nil {
		p.Account = &Account{Type: acct.Account.Type, Email: acct.Account.Email, Plan: acct.Account.PlanType}
	}
	return p, nil
}

// unsupported reports an RPC error naming a method this daemon lacks
// (Codex answers -32600 "unknown variant `method`").
func unsupported(err error) bool {
	var re *RPCError
	if !errors.As(err, &re) {
		return false
	}
	return re.Code == codeMethodNotFound || (re.Code == codeInvalidRequest && strings.Contains(re.Message, "unknown variant `"+re.Method+"`"))
}

// notLoaded reports Codex's answer for a thread it does not know: one
// never persisted (no first turn) after the daemon restarted, or a
// foreign id.
func notLoaded(err error) bool {
	var re *RPCError
	return errors.As(err, &re) && re.Code == codeInvalidRequest &&
		(strings.HasPrefix(re.Message, "thread not loaded") || strings.HasPrefix(re.Message, "invalid thread id") || strings.HasPrefix(re.Message, "no rollout found"))
}
