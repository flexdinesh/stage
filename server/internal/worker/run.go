package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/instance"
	"github.com/flexdinesh/stage/server/internal/listen"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// ErrRevoked: the main server refused the machine token (the machine was
// removed). The worker stops; restarting cannot fix it (exit 78).
var ErrRevoked = errors.New("the Stage server refused this machine's token: the machine was removed (join again with a new pairing link)")

// RunOptions configure Run.
type RunOptions struct {
	ConfigPath string
	Logger     *slog.Logger
	// Managed: run by the worker service; Manager and Service name it in
	// the lock record.
	Managed          bool
	Manager, Service string
	HTTP             *http.Client
	// MinBackoff and MaxBackoff bound reconnect delays (1s, 60s).
	MinBackoff, MaxBackoff time.Duration
	// Ready is called once the directory is locked and the config loaded
	// (tests).
	Ready func()
	// OnState is called on every state change (tests).
	OnState func(State)
	// Listen is the agent gateway's listen list (config.ParseWorkerListen;
	// nil: unix, the default socket).
	Listen []listen.Addr
	// Getenv resolves the gateway's default socket (os.Getenv).
	Getenv func(string) string
	// Host builds what the worker serves for its machine (remote.Host): its
	// call handlers beside ping, and changes to report. Nil serves ping only.
	Host func(machineID int64) Host
}

// Host is what a worker serves over its channel.
type Host interface {
	// Handlers answer the main server's calls by kind.
	Handlers() map[string]workerchan.WorkerHandler
	// Changes signals that what the host serves changed (reported to the
	// main server as machine.changed).
	Changes() <-chan struct{}
}

// Run is `stage worker run`: lock the worker directory, open the agent
// gateway, then keep a channel to the main server open until ctx ends,
// reconnecting with backoff. Errors: config.InvalidError wrapping
// ErrNotJoined or a bad config, or ErrRevoked (exit 78); *LockedError or a
// gateway socket's *listen.InUseError (exit 75).
func Run(ctx context.Context, o RunOptions) error {
	log := o.Logger
	if log == nil {
		log = slog.Default()
	}
	if o.MinBackoff <= 0 {
		o.MinBackoff = time.Second
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = time.Minute
	}
	dir := Dir(o.ConfigPath)
	if _, err := LoadConfig(o.ConfigPath); err != nil {
		return config.Invalid(err)
	}
	l, err := acquire(dir, Holder{PID: os.Getpid(), Version: buildinfo.Version, StartedAt: time.Now().UTC(), Managed: o.Managed, Manager: o.Manager, Service: o.Service})
	if err != nil {
		return err
	}
	defer l.release()
	// Re-read under the lock: a leave may have raced the start.
	cfg, err := LoadConfig(o.ConfigPath)
	if err != nil {
		return config.Invalid(err)
	}
	if o.Listen == nil {
		if o.Listen, err = config.ParseWorkerListen(""); err != nil {
			return config.Invalid(err)
		}
	}
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	var transport http.RoundTripper
	if o.HTTP != nil {
		transport = o.HTTP.Transport
	}
	h, err := NewGateway(cfg, transport, log)
	if err != nil {
		return config.Invalid(err)
	}
	gw, err := openGateway(o.Listen, o.Getenv, h, log)
	if err != nil {
		return err
	}
	defer gw.close()
	w := &runner{o: o, cfg: cfg, dir: dir, log: log, api: newAPI(cfg.URL, cfg.Token, o.HTTP), gateway: gw.URLs}
	w.handlers = map[string]workerchan.WorkerHandler{
		workerchan.KindPing: func(context.Context, workerchan.Envelope, workerchan.Upcall) (any, error) {
			return workerchan.Pong{Version: buildinfo.Version, Time: time.Now().UTC()}, nil
		},
	}
	if o.Host != nil {
		w.host = o.Host(cfg.MachineID)
		for kind, h := range w.host.Handlers() {
			if kind != workerchan.KindPing {
				w.handlers[kind] = h
			}
		}
	}
	w.set(State{State: StateConnecting})
	if err := instance.Notify("READY=1"); err != nil {
		log.Warn("readiness notification", "err", err)
	}
	if o.Ready != nil {
		o.Ready()
	}
	log.Info("worker started", "server", cfg.URL, "machine", cfg.MachineID, "dir", dir)
	err = w.loop(ctx)
	_ = instance.Notify("STOPPING=1")
	if errors.Is(err, ErrRevoked) {
		w.set(State{State: StateRevoked, LastError: err.Error()})
		return config.Invalid(err)
	}
	w.set(State{State: StateStopped})
	log.Info("worker stopped")
	return err
}

type runner struct {
	o   RunOptions
	cfg Config
	dir string
	log *slog.Logger
	api *api
	// gateway is the agent gateway's listener URLs.
	gateway []string
	// handlers answer the main server's calls by kind; host may be nil.
	handlers map[string]workerchan.WorkerHandler
	host     Host

	mu    sync.Mutex
	state State
}

// set records a state change in worker.state.json.
func (w *runner) set(s State) {
	w.mu.Lock()
	s.PID, s.Managed, s.URL, s.MachineID, s.Gateway = os.Getpid(), w.o.Managed, w.cfg.URL, w.cfg.MachineID, w.gateway
	if s.ServerVersion == "" {
		s.ServerVersion = w.state.ServerVersion
	}
	if s.State == w.state.State && !w.state.Since.IsZero() {
		s.Since = w.state.Since
	} else {
		s.Since = time.Now().UTC()
	}
	changed := s.State != w.state.State || s.LastError != w.state.LastError || s.Session != w.state.Session
	w.state = s
	w.mu.Unlock()
	if !changed {
		return
	}
	if err := writeState(w.dir, s); err != nil {
		w.log.Warn("worker state", "err", err)
	}
	if w.o.OnState != nil {
		w.o.OnState(s)
	}
}

// stableAfter is how long a channel must have been live for the next
// reconnect to start again at MinBackoff.
const stableAfter = 30 * time.Second

func (w *runner) loop(ctx context.Context) error {
	backoff := w.o.MinBackoff
	for {
		start := time.Now()
		err := w.session(ctx)
		if ctx.Err() != nil {
			return nil
		}
		wait := backoff
		switch {
		case Revoked(err):
			w.log.Error("machine token refused; stopping", "err", err)
			return ErrRevoked
		case VersionMismatch(err):
			w.log.Warn("API version mismatch; install the same Stage version as the server", "err", err)
			w.set(State{State: StateNeedsUpdate, LastError: err.Error()})
			wait = w.o.MaxBackoff
		default:
			msg := ""
			if err != nil {
				msg = err.Error()
				w.log.Warn("worker channel ended; reconnecting", "err", err, "in", backoff)
			}
			w.set(State{State: StateConnecting, LastError: msg})
		}
		if time.Since(start) >= stableAfter {
			backoff, wait = w.o.MinBackoff, w.o.MinBackoff
		}
		// Full jitter in [wait/2, wait].
		d := wait/2 + rand.N(wait/2+1)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(d):
		}
		backoff = min(backoff*2, w.o.MaxBackoff)
	}
}

// session runs one channel: the down stream until it ends, a heartbeat
// ticker, and one goroutine per call.
func (w *runner) session(parent context.Context) error {
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	req, err := w.api.request(ctx, "GET", "/api/worker/channel", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set(workerchan.GatewayHeader, strings.Join(w.gateway, ","))
	res, err := w.api.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if err := checkStatus(res); err != nil {
		return err
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		return fmt.Errorf("worker channel: unexpected content type %q", ct)
	}

	// Silence watchdog: the server writes at least a comment every
	// keepalive; a stream silent for the timeout is dead.
	timeout := workerchan.DefaultTimeout
	alive := make(chan struct{}, 1)
	rd := workerchan.NewReader(res.Body)
	rd.OnLine = func() {
		select {
		case alive <- struct{}{}:
		default:
		}
	}
	var wg sync.WaitGroup
	defer func() { cancel(nil); wg.Wait() }()
	var timeoutMu sync.Mutex
	initial := timeout
	wg.Go(func() {
		t := time.NewTimer(initial)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-alive:
				timeoutMu.Lock()
				d := timeout
				timeoutMu.Unlock()
				t.Reset(d)
			case <-t.C:
				cancel(errors.New("worker channel silent; reconnecting"))
				return
			}
		}
	})

	ev, err := rd.Next()
	if err != nil {
		return streamErr(ctx, err)
	}
	if ev.Name != workerchan.EventHello {
		return fmt.Errorf("worker channel: first event %q, want hello", ev.Name)
	}
	var hello workerchan.Hello
	if err := json.Unmarshal(ev.Data, &hello); err != nil || hello.Session == "" {
		return fmt.Errorf("worker channel: bad hello")
	}
	if hello.TimeoutSec > 0 {
		timeoutMu.Lock()
		timeout = time.Duration(hello.TimeoutSec) * time.Second
		timeoutMu.Unlock()
	}
	heartbeat := workerchan.DefaultKeepalive
	if hello.HeartbeatSec > 0 {
		heartbeat = time.Duration(hello.HeartbeatSec) * time.Second
	}
	sess := &channel{w: w, id: hello.Session, ctx: ctx, cancel: cancel, calls: map[string]context.CancelFunc{}}
	w.set(State{State: StateOnline, Session: hello.Session, ServerVersion: res.Header.Get(workerchan.VersionHeader)})
	w.log.Info("worker channel online", "session", hello.Session)

	wg.Go(func() { sess.heartbeats(heartbeat) })
	if w.host != nil {
		wg.Go(func() { sess.changes(w.host.Changes()) })
	}
	defer sess.cancelAll()
	for {
		ev, err := rd.Next()
		if err != nil {
			return streamErr(ctx, err)
		}
		if ev.Name != workerchan.EventRPC {
			continue
		}
		var env workerchan.Envelope
		if err := json.Unmarshal(ev.Data, &env); err != nil {
			w.log.Warn("worker channel: bad message", "err", err)
			continue
		}
		if env.Kind == workerchan.KindCancel {
			sess.cancelCall(env.ID)
			continue
		}
		wg.Go(func() { sess.handle(env) })
	}
}

// streamErr is the reason a stream read failed: the session's cancel cause
// (watchdog, replaced, revoked) when it ended, else the read error.
func streamErr(ctx context.Context, err error) error {
	if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
		return cause
	}
	if errors.Is(err, io.EOF) {
		return errors.New("the server closed the worker channel")
	}
	return err
}

// channel is one live channel session on the worker.
type channel struct {
	w      *runner
	id     string
	ctx    context.Context
	cancel context.CancelCauseFunc

	mu    sync.Mutex
	calls map[string]context.CancelFunc
	seq   atomic.Uint64
}

// post sends one up message; a worker-initiated call's reply is decoded
// into reply. A refused session or token ends the session.
func (c *channel) post(env workerchan.Envelope, reply *workerchan.Envelope) error {
	return c.postCtx(c.ctx, env, reply)
}

// postCtx is post bounded by ctx as well as the session.
func (c *channel) postCtx(ctx context.Context, env workerchan.Envelope, reply *workerchan.Envelope) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stop := context.AfterFunc(c.ctx, cancel)
	defer stop()
	req, err := c.w.api.request(ctx, "POST", "/api/worker/channel", env)
	if err != nil {
		return err
	}
	req.Header.Set(workerchan.SessionHeader, c.id)
	res, err := c.w.api.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if err := checkStatus(res); err != nil {
		var he *HTTPError
		if errors.As(err, &he) && (he.Status == http.StatusUnauthorized || he.Status == http.StatusConflict) {
			c.cancel(err)
		}
		return err
	}
	if reply != nil && res.StatusCode != http.StatusNoContent {
		return json.NewDecoder(io.LimitReader(res.Body, 32<<20)).Decode(reply)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	return nil
}

func (c *channel) heartbeats(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-t.C:
			if err := c.post(workerchan.Envelope{Kind: workerchan.KindHeartbeat}, nil); err != nil && c.ctx.Err() == nil {
				c.w.log.Warn("worker heartbeat", "err", err)
			}
		}
	}
}

// changes reports each change of what the host serves.
func (c *channel) changes(ch <-chan struct{}) {
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ch:
			if err := c.post(workerchan.Envelope{ID: c.nextID(), Kind: remoteChanged}, nil); err != nil && c.ctx.Err() == nil {
				c.w.log.Warn("worker change report", "err", err)
			}
		}
	}
}

// remoteChanged is remote.KindChanged (the worker package does not import
// the remote machine's protocol).
const remoteChanged = "machine.changed"

// nextID is a fresh id for a worker-initiated call.
func (c *channel) nextID() string {
	return "w" + c.id[:min(6, len(c.id))] + ":" + strconv.FormatUint(c.seq.Add(1), 10)
}

// upcall sends a worker-initiated call belonging to request parent and
// returns its reply body.
func (c *channel) upcall(parent workerchan.Envelope) workerchan.Upcall {
	return func(ctx context.Context, kind string, body any) (json.RawMessage, error) {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		var reply workerchan.Envelope
		if err := c.postCtx(ctx, workerchan.Envelope{ID: c.nextID(), Kind: kind, Call: parent.ID, ConnectionID: parent.ConnectionID, Body: raw}, &reply); err != nil {
			return nil, err
		}
		if reply.Error != nil {
			return nil, &workerchan.RemoteError{Code: reply.Error.Code, Message: reply.Error.Message}
		}
		return reply.Body, nil
	}
}

// handle runs one call under its deadline and posts its result.
func (c *channel) handle(env workerchan.Envelope) {
	ctx, cancel := context.WithCancel(c.ctx)
	if env.Deadline != nil {
		var dc context.CancelFunc
		ctx, dc = context.WithDeadline(ctx, *env.Deadline)
		defer dc()
	}
	c.mu.Lock()
	c.calls[env.ID] = cancel
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.calls, env.ID)
		c.mu.Unlock()
		cancel()
	}()
	res := workerchan.Envelope{ID: env.ID, Kind: workerchan.KindResult}
	h, ok := c.w.handlers[env.Kind]
	if !ok {
		res.Error = &workerchan.Error{Code: workerchan.CodeUnknownKind, Message: "this worker does not handle " + env.Kind}
	} else {
		body, err := h(ctx, env, c.upcall(env))
		var re *workerchan.RemoteError
		switch {
		case err == nil:
			b, merr := json.Marshal(body)
			if merr != nil {
				res.Error = &workerchan.Error{Code: workerchan.CodeInternal, Message: merr.Error()}
			} else {
				res.Body = b
			}
		case errors.As(err, &re):
			res.Error = &workerchan.Error{Code: re.Code, Message: re.Message}
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			res.Error = &workerchan.Error{Code: workerchan.CodeTimeout, Message: err.Error()}
		case errors.Is(ctx.Err(), context.Canceled):
			res.Error = &workerchan.Error{Code: workerchan.CodeCancelled, Message: err.Error()}
		default:
			res.Error = &workerchan.Error{Code: workerchan.CodeInternal, Message: err.Error()}
		}
	}
	if c.ctx.Err() != nil {
		return // the stream ended: the main server resolves the call as lost
	}
	if err := c.post(res, nil); err != nil && c.ctx.Err() == nil {
		c.w.log.Warn("worker result", "id", env.ID, "err", err)
	}
}

func (c *channel) cancelCall(id string) {
	c.mu.Lock()
	cancel := c.calls[id]
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *channel) cancelAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, cancel := range c.calls {
		cancel()
	}
}
