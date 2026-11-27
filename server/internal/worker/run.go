package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"math/rand/v2"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/instance"
	"github.com/flexdinesh/stage/server/internal/listen"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/observe"
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
	// call handlers beside ping, and changes to report. call sends a
	// worker-initiated call to the main server over the live channel
	// (ErrOffline when none). Nil serves ping only.
	Host func(machineID int64, call workerchan.Upcall) Host
	// ScopedHost supplies a host and optional startup restore hook. The
	// identity is an opaque fingerprint of the join. Restore returns false
	// when setup is missing, or an error when it needs recovery.
	ScopedHost func(machineID int64, identity string, call workerchan.Upcall) (Host, func(context.Context) (bool, error))
	// ProtocolRange defaults to workerchan.CurrentRange.
	ProtocolRange workerchan.VersionRange
}

// Host is what a worker serves over its channel.
type Host interface {
	// Handlers answer the main server's calls by kind.
	Handlers() map[string]workerchan.WorkerHandler
	// Changes signals that what the host serves changed.
	Changes() <-chan struct{}
	// Attached composes the host after hello and before ready. Its ctx
	// owns persistent harness resources for the lifetime of the worker.
	Attached(ctx context.Context, serverKinds []string) error
	Snapshot(ctx context.Context) (workerchan.Ready, error)
}

// ErrOffline: no channel to the main server is live (a worker-initiated
// call was not sent).
var ErrOffline = errors.New("the worker is not connected to the Stage server")

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
	if o.ProtocolRange == (workerchan.VersionRange{}) {
		o.ProtocolRange = workerchan.CurrentRange()
	}
	if !o.ProtocolRange.Valid() {
		return config.Invalid(errors.New("invalid worker channel protocol range"))
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
	life, cancel := context.WithCancel(ctx)
	defer cancel()
	var restore func(context.Context) (bool, error)
	identity := cfg.cacheIdentity()
	if o.ScopedHost != nil {
		w.host, restore = o.ScopedHost(cfg.MachineID, identity, w.call)
	} else if o.Host != nil {
		w.host = o.Host(cfg.MachineID, w.call)
	}
	if w.host != nil {
		for kind, h := range w.host.Handlers() {
			if kind != workerchan.KindPing && kind != observe.KindWatch {
				w.handlers[kind] = h
			}
		}
	}
	settingsRestored := true
	var settingsErr error
	if restore != nil {
		settingsRestored, settingsErr = restore(life)
	}
	var observing sync.WaitGroup
	if host, ok := w.host.(interface{ Local() *machine.Local }); ok && host.Local() != nil {
		previous, _ := ReadState(dir)
		w.observer = newObservationWorker(w, host.Local(), identity)
		w.observer.restore(settingsRestored, settingsErr, previous)
		observing.Go(func() { w.observer.work.Run(life) })
		observing.Go(func() { w.observer.deliver(life) })
	}
	w.set(State{State: StateConnecting})
	if err := instance.Notify("READY=1"); err != nil {
		log.Warn("readiness notification", "err", err)
	}
	if o.Ready != nil {
		o.Ready()
	}
	log.Info("worker started", "server", cfg.URL, "machine", cfg.MachineID, "dir", dir)
	err = w.loop(life)
	cancel()
	observing.Wait()
	if w.observer != nil {
		w.observer.pause()
	}
	if _, err := LoadConfig(o.ConfigPath); errors.Is(err, ErrNotJoined) {
		removeObservationFiles(dir)
	}
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
	observer *observationWorker

	mu    sync.Mutex
	state State
	// cur is the live channel session (nil while offline).
	cur atomic.Pointer[channel]
}

// call is a worker-initiated call naming no request, over the live
// channel.
func (w *runner) call(ctx context.Context, kind string, body any) (json.RawMessage, error) {
	c := w.cur.Load()
	if c == nil {
		return nil, ErrOffline
	}
	return c.upcall(workerchan.Envelope{})(ctx, kind, body)
}

// set records a state change in worker.state.json.
func (w *runner) set(s State) {
	w.updateState(func(prev State) State {
		prev.State, prev.LastError, prev.Session = s.State, s.LastError, s.Session
		if s.ServerVersion != "" {
			prev.ServerVersion = s.ServerVersion
		}
		return prev
	})
}

func (w *runner) waitForObservationWatch() {
	w.updateState(func(s State) State {
		if s.Observation != observationRecovery && s.Observation != observationDegraded {
			s.Observation = observationWaitingWatch
		}
		return s
	})
}

func (w *runner) updateState(update func(State) State) {
	w.mu.Lock()
	s := update(w.state)
	s.PID, s.Managed, s.URL, s.MachineID, s.Gateway = os.Getpid(), w.o.Managed, w.cfg.URL, w.cfg.MachineID, w.gateway
	if s.ServerVersion == "" {
		s.ServerVersion = w.state.ServerVersion
	}
	if s.State == w.state.State && !w.state.Since.IsZero() {
		s.Since = w.state.Since
	} else {
		s.Since = time.Now().UTC()
	}
	changed := s.State != w.state.State || s.LastError != w.state.LastError || s.Session != w.state.Session ||
		s.Observation != w.state.Observation || s.ObservationError != w.state.ObservationError ||
		s.ObservationLost != w.state.ObservationLost || s.ObservationRecovered != w.state.ObservationRecovered ||
		s.ObservationProducer != w.state.ObservationProducer
	w.state = s
	if !changed {
		w.mu.Unlock()
		return
	}
	err := writeState(w.dir, s)
	w.mu.Unlock()
	if err != nil {
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
			w.log.Warn("worker channel protocol mismatch", "err", err)
			var he *HTTPError
			errors.As(err, &he)
			w.set(State{State: StateNeedsUpdate, LastError: he.Msg})
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
	req.Header.Set(workerchan.ProtocolMinHeader, strconv.Itoa(w.o.ProtocolRange.Min))
	req.Header.Set(workerchan.ProtocolMaxHeader, strconv.Itoa(w.o.ProtocolRange.Max))
	res, err := w.api.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if err := checkStatus(res); err != nil {
		var he *HTTPError
		if errors.As(err, &he) && he.Code == "api_version_mismatch" {
			return olderServer()
		}
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
	var sess *channel
	defer func() {
		cancel(nil)
		wg.Wait()
		if w.observer != nil && sess != nil {
			w.observer.detach(sess, parent.Err() == nil)
		}
	}()
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
	if err := json.Unmarshal(ev.Data, &hello); err != nil || hello.Session == "" || hello.MachineID != w.cfg.MachineID {
		return fmt.Errorf("worker channel: bad hello")
	}
	if hello.ProtocolRange == (workerchan.VersionRange{}) && hello.ProtocolVersion == 0 {
		return olderServer()
	}
	version, shared := w.o.ProtocolRange.Select(hello.ProtocolRange)
	if !hello.ProtocolRange.Valid() || !shared || version != hello.ProtocolVersion || hello.Kinds == nil || hello.ChannelKinds == nil || !slices.Contains(hello.ChannelKinds, workerchan.KindReady) {
		return fmt.Errorf("worker channel: invalid protocol negotiation in hello")
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
	sess = &channel{w: w, id: hello.Session, ctx: ctx, cancel: cancel, calls: map[string]context.CancelFunc{}, serverKinds: hello.ChannelKinds}
	if w.observer != nil {
		w.observer.reconcile()
	}

	wg.Go(func() { sess.heartbeats(heartbeat) })
	w.cur.Store(sess)
	defer w.cur.CompareAndSwap(sess, nil)
	wg.Go(func() { sess.advertise(parent, hello.Kinds, res.Header.Get(workerchan.VersionHeader)) })
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

func olderServer() error {
	return &HTTPError{Status: http.StatusConflict, Code: workerchan.CodeProtocolMismatch, Msg: workerchan.UpdateReason(true)}
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
	w           *runner
	id          string
	ctx         context.Context
	cancel      context.CancelCauseFunc
	serverKinds []string
	readyMu     sync.Mutex

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

// advertise composes once for this attachment, then sends complete ready snapshots serially.
func (c *channel) advertise(life context.Context, kinds []string, serverVersion string) {
	var changes <-chan struct{}
	if c.w.host != nil {
		if err := c.w.host.Attached(life, kinds); err != nil {
			c.cancel(fmt.Errorf("prepare harness setup: %w", err))
			return
		}
		changes = c.w.host.Changes()
	}
	if c.w.observer != nil {
		c.w.observer.attach(c)
	}
	if err := c.ready(); err != nil {
		c.cancel(fmt.Errorf("publish worker capabilities: %w", err))
		return
	}
	if c.w.observer != nil {
		c.w.observer.ready(c)
	}
	c.w.set(State{State: StateOnline, Session: c.id, ServerVersion: serverVersion})
	c.w.log.Info("worker channel online", "session", c.id)
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-changes:
			if err := c.ready(); err != nil {
				c.cancel(fmt.Errorf("update worker capabilities: %w", err))
				return
			}
		}
	}
}

func (c *channel) ready() error {
	c.readyMu.Lock()
	defer c.readyMu.Unlock()
	snapshot := workerchan.Ready{Kinds: []string{}, Connections: []workerchan.Connection{}}
	if c.w.host != nil {
		var err error
		if snapshot, err = c.w.host.Snapshot(c.ctx); err != nil {
			return err
		}
	}
	snapshot.ChannelKinds = slices.Collect(maps.Keys(c.w.handlers))
	snapshot.ChannelKinds = append(snapshot.ChannelKinds, workerchan.KindCancel)
	if c.w.observer != nil {
		c.w.observer.capabilities(c, &snapshot)
	}
	slices.Sort(snapshot.ChannelKinds)
	body, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return c.post(workerchan.Envelope{Kind: workerchan.KindReady, Body: body}, nil)
}

// nextID is a fresh id for a worker-initiated call.
func (c *channel) nextID() string {
	return "w" + c.id[:min(6, len(c.id))] + ":" + strconv.FormatUint(c.seq.Add(1), 10)
}

// upcall sends a worker-initiated call belonging to request parent and
// returns its reply body.
func (c *channel) upcall(parent workerchan.Envelope) workerchan.Upcall {
	return func(ctx context.Context, kind string, body any) (json.RawMessage, error) {
		if !slices.Contains(c.serverKinds, kind) {
			return nil, &workerchan.RemoteError{Code: workerchan.CodeOperationUnavailable, Message: "Update Stage on the main machine to use " + kind + "."}
		}
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
	if env.Kind == observe.KindWatch && c.w.observer != nil && slices.Contains(c.serverKinds, observe.KindBatch) {
		h, ok = func(ctx context.Context, env workerchan.Envelope, _ workerchan.Upcall) (any, error) {
			return c.w.observer.accept(c, ctx, env)
		}, true
	}
	var receipt watchReceipt
	if !ok {
		res.Error = &workerchan.Error{Code: workerchan.CodeUnknownKind, Message: "this worker does not handle " + env.Kind}
	} else {
		body, err := h(ctx, env, c.upcall(env))
		if watch, ok := body.(watchReceipt); ok {
			receipt, body = watch, watch.ack
		}
		if changed, ok := body.(workerchan.RefreshCapabilities); ok && err == nil {
			// Setup has already changed. Recover the snapshot on a fresh session
			// if the server cannot acknowledge this update.
			if err := c.ready(); err != nil {
				c.cancel(fmt.Errorf("update worker capabilities: %w", err))
				return
			}
			body = changed.Value
		}
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
	if err := c.post(res, nil); err != nil {
		if c.ctx.Err() == nil {
			c.w.log.Warn("worker result", "id", env.ID, "err", err)
		}
	} else if receipt.revision != 0 && res.Error == nil {
		c.w.observer.acknowledged(c, receipt)
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
