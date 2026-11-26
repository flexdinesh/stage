package workerchan

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Options configure a Hub; zero values take the defaults.
type Options struct {
	// Keepalive is how often the down stream sends a comment and the worker
	// a heartbeat (DefaultKeepalive).
	Keepalive time.Duration
	// Timeout is the silence after which a channel ends (DefaultTimeout).
	Timeout time.Duration
	// PersistEvery throttles Seen while a channel is live (1 minute).
	PersistEvery time.Duration
	// Seen persists that a machine's worker was heard from (connect,
	// disconnect, and at most every PersistEvery in between); version is
	// its build when known. Called outside the hub's lock; may be nil.
	Seen func(machineID int64, at time.Time, version string)
	// Now is the clock (time.Now).
	Now func() time.Time
	// Version is this server's build, reported in pong replies.
	Version string
	// ProtocolRange defaults to the current channel protocol version.
	ProtocolRange VersionRange
}

// Hub multiplexes calls to remote machines over their worker channels: at
// most one live session per machine, a newer stream replacing an older
// one. It is bound to the server's context: when that ends, every session
// ends, so open streams never delay shutdown.
type Hub struct {
	ctx  context.Context
	opts Options

	mu            sync.Mutex
	sessions      map[int64]*Session
	stale         map[int64]Mismatch
	seq           atomic.Uint64
	handlers      map[string]Handler
	initializing  map[string]bool
	kinds         []string
	callbackKinds []string
	onReady       func(machineID int64)
}

// Callback answers one worker-initiated call that names a pending request
// (Envelope.Call): it runs only while that request waits for its result.
// A *RemoteError result keeps its code; any other error is internal.
type Callback func(ctx context.Context, env Envelope) (any, error)

// Handler answers one worker-initiated call of a kind that names no
// request (Handle).
type Handler func(ctx context.Context, machineID int64, env Envelope) (any, error)

// pending is one request waiting for its result. Its callbacks run under
// ctx, which ends when the request stops waiting; the request returns only
// after its running callbacks did (running), so a callback never outlives
// its caller.
type pending struct {
	reply    chan Envelope
	callback Callback
	ctx      context.Context
	running  sync.WaitGroup
}

// Mismatch is a refused channel negotiation for an authenticated worker.
type Mismatch struct {
	Reason      string
	Version     string
	ServerOlder bool
}

// Presence is what the hub knows about a machine right now.
type Presence struct {
	// Online: a live session heard from within the timeout.
	Online      bool
	Negotiating bool
	// LastSeen is the last message from its worker this process saw (zero
	// when none).
	LastSeen time.Time
	// Version is the build of the connected (or last) worker ("" unknown).
	Version string
	// NeedsUpdate records an incompatible channel opening.
	NeedsUpdate *Mismatch
}

// Session is one live down stream of one machine.
type Session struct {
	ID        string
	MachineID int64
	Version   string
	// Gateway is the worker's agent gateway listener URLs (fixed for the
	// session).
	Gateway []string
	// ready is immutable after publication and protected by hub.mu.
	ready *Ready

	hub  *Hub
	out  chan Envelope
	done chan struct{}
	once sync.Once

	mu        sync.Mutex
	pending   map[string]*pending
	lastSeen  time.Time
	persisted time.Time
}

// NewHub returns a hub bound to ctx.
func NewHub(ctx context.Context, o Options) *Hub {
	if o.Keepalive <= 0 {
		o.Keepalive = DefaultKeepalive
	}
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	if o.PersistEvery <= 0 {
		o.PersistEvery = time.Minute
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.ProtocolRange == (VersionRange{}) {
		o.ProtocolRange = CurrentRange()
	}
	h := &Hub{ctx: ctx, opts: o, sessions: map[int64]*Session{}, stale: map[int64]Mismatch{}, handlers: map[string]Handler{}, initializing: map[string]bool{}}
	go func() {
		<-ctx.Done()
		h.mu.Lock()
		ss := make([]*Session, 0, len(h.sessions))
		for _, s := range h.sessions {
			ss = append(ss, s)
		}
		h.mu.Unlock()
		for _, s := range ss {
			h.Detach(s)
		}
	}()
	return h
}

// Handle answers worker-initiated calls of kind that name no request.
// Register handlers before workers connect.
func (h *Hub) Handle(kind string, fn Handler) {
	h.mu.Lock()
	h.handlers[kind] = fn
	h.mu.Unlock()
}

// HandleInitialization registers a handler that composition needs before ready.
func (h *Hub) HandleInitialization(kind string, fn Handler) {
	h.mu.Lock()
	h.handlers[kind] = fn
	h.initializing[kind] = true
	h.mu.Unlock()
}

// SetHarnessKinds sets the harness kinds advertised by the server.
func (h *Hub) SetHarnessKinds(kinds []string) {
	h.mu.Lock()
	h.kinds = slices.Clone(kinds)
	h.mu.Unlock()
}

// SetCallbackKinds sets the request-scoped callbacks served by the server.
func (h *Hub) SetCallbackKinds(callbackKinds []string) {
	h.mu.Lock()
	h.callbackKinds = slices.Clone(callbackKinds)
	h.mu.Unlock()
}

func (h *Hub) advertised() ([]string, []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	kinds := append([]string{}, h.kinds...)
	channels := append([]string{KindHeartbeat, KindPing, KindReady, KindResult}, h.callbackKinds...)
	channels = append(channels, slices.Collect(maps.Keys(h.handlers))...)
	slices.Sort(channels)
	return kinds, slices.Compact(channels)
}

// OnReady runs after a new ready snapshot is published for a machine.
func (h *Hub) OnReady(fn func(machineID int64)) {
	h.mu.Lock()
	h.onReady = fn
	h.mu.Unlock()
}

// Keepalive is the configured keepalive interval.
func (h *Hub) Keepalive() time.Duration { return h.opts.Keepalive }

// Timeout is the configured silence timeout.
func (h *Hub) Timeout() time.Duration { return h.opts.Timeout }

func randomID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// attach starts negotiation and replaces any older session for the machine.
func (h *Hub) attach(machineID int64, version string, gateway []string) *Session {
	now := h.opts.Now()
	s := &Session{
		ID: randomID(), MachineID: machineID, Version: version, Gateway: gateway, hub: h,
		out: make(chan Envelope, 64), done: make(chan struct{}),
		pending: map[string]*pending{}, lastSeen: now, persisted: now,
	}
	h.mu.Lock()
	old := h.sessions[machineID]
	if old != nil {
		old.close()
	}
	h.sessions[machineID] = s
	delete(h.stale, machineID)
	h.mu.Unlock()
	if h.ctx.Err() != nil {
		h.Detach(s)
		return s
	}
	h.seen(machineID, now, version)
	go h.watch(s)
	return s
}

// watch ends a session its worker stopped speaking on.
func (h *Hub) watch(s *Session) {
	t := time.NewTicker(h.opts.Timeout / 3)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
			s.mu.Lock()
			silent := h.opts.Now().Sub(s.lastSeen) > h.opts.Timeout
			s.mu.Unlock()
			if silent {
				h.Detach(s)
				return
			}
		}
	}
}

// Detach ends s: its in-flight calls resolve as lost replies. A newer
// session of the same machine is untouched.
func (h *Hub) Detach(s *Session) {
	h.mu.Lock()
	current := h.sessions[s.MachineID] == s
	if current {
		delete(h.sessions, s.MachineID)
	}
	h.mu.Unlock()
	first := s.close()
	if current && first {
		s.mu.Lock()
		last := s.lastSeen
		s.mu.Unlock()
		h.seen(s.MachineID, last, s.Version)
	}
}

// Close ends machineID's session, if any (the machine was removed).
func (h *Hub) Close(machineID int64) {
	h.mu.Lock()
	s := h.sessions[machineID]
	delete(h.stale, machineID)
	h.mu.Unlock()
	if s != nil {
		h.Detach(s)
	}
}

// markNeedsUpdate records a refused range without replacing a live session.
func (h *Hub) markNeedsUpdate(machineID int64, serverOlder bool, version string) {
	h.mu.Lock()
	if h.sessions[machineID] == nil {
		h.stale[machineID] = Mismatch{Reason: UpdateReason(serverOlder), Version: version, ServerOlder: serverOlder}
	}
	h.mu.Unlock()
}

// Presence reports machineID's channel state.
func (h *Hub) Presence(machineID int64) Presence {
	h.mu.Lock()
	s := h.sessions[machineID]
	var p Presence
	if m, ok := h.stale[machineID]; ok {
		p.NeedsUpdate = &m
	}
	ready := s != nil && s.ready != nil
	h.mu.Unlock()
	if s == nil {
		return p
	}
	s.mu.Lock()
	p.LastSeen = s.lastSeen
	s.mu.Unlock()
	p.Version = s.Version
	live := s.alive() && h.opts.Now().Sub(p.LastSeen) <= h.opts.Timeout
	p.Online, p.Negotiating = live && ready, live && !ready
	return p
}

// Advertisement is the live session's immutable ready snapshot (nil while negotiating).
func (h *Hub) Advertisement(machineID int64) (*Ready, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.sessions[machineID]
	if s == nil || !s.alive() {
		return nil, ""
	}
	return s.ready, s.ID
}

// SessionID is machineID's live channel session ("" when offline).
func (h *Hub) SessionID(machineID int64) string {
	h.mu.Lock()
	s := h.sessions[machineID]
	h.mu.Unlock()
	if s == nil || !s.alive() {
		return ""
	}
	return s.ID
}

// Gateways is the agent gateway listener URLs machineID's live worker
// reported (nil when offline or none).
func (h *Hub) Gateways(machineID int64) []string {
	h.mu.Lock()
	s := h.sessions[machineID]
	h.mu.Unlock()
	if s == nil || !s.alive() {
		return nil
	}
	return slices.Clone(s.Gateway)
}

// ParseGateway reads GatewayHeader's comma list (empty items dropped).
func ParseGateway(v string) []string {
	var out []string
	for item := range strings.SplitSeq(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func (h *Hub) seen(machineID int64, at time.Time, version string) {
	if h.opts.Seen != nil {
		h.opts.Seen(machineID, at, version)
	}
}

// current is machineID's session when its id is sessionID.
func (h *Hub) current(machineID int64, sessionID string) *Session {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.sessions[machineID]
	if s == nil || s.ID != sessionID || !s.alive() {
		return nil
	}
	return s
}

// Deliver handles one up message of machineID's session sessionID. A
// result resolves its call (an unknown or late one is dropped); a
// heartbeat only counts as liveness; a worker-initiated call is answered
// with the returned envelope: a callback of the pending request it names
// (Envelope.Call; call_ended once that request stopped waiting), else the
// kind's Handler. ErrReplaced when the session is not the machine's live
// one.
func (h *Hub) Deliver(ctx context.Context, machineID int64, sessionID string, env Envelope) (*Envelope, error) {
	s := h.current(machineID, sessionID)
	if s == nil {
		return nil, ErrReplaced
	}
	s.touch()
	switch env.Kind {
	case KindHeartbeat:
		return nil, nil
	case KindResult:
		h.mu.Lock()
		if h.sessions[machineID] != s {
			h.mu.Unlock()
			return nil, ErrReplaced
		}
		s.resolve(env)
		h.mu.Unlock()
		return nil, nil
	case KindReady:
		return nil, h.publish(s, env.Body)
	case KindPing:
		body, _ := json.Marshal(Pong{Version: h.opts.Version, Time: h.opts.Now().UTC()})
		return &Envelope{ID: env.ID, Kind: KindResult, Body: body}, nil
	}
	h.mu.Lock()
	ready, initializing := s.ready != nil, h.initializing[env.Kind]
	fn := h.handlers[env.Kind]
	h.mu.Unlock()
	if !ready && !initializing {
		return answer(ctx, env, func(context.Context) (any, error) {
			return nil, &RemoteError{Code: CodeOperationUnavailable, Message: ErrNegotiating.Error()}
		}), nil
	}
	if env.Call != "" {
		s.mu.Lock()
		p, ok := s.pending[env.Call]
		if ok && p.callback != nil {
			p.running.Add(1) // under s.mu: the request cannot have stopped waiting
		}
		s.mu.Unlock()
		if !ok || p.callback == nil {
			return &Envelope{ID: env.ID, Kind: KindResult, Error: &Error{Code: CodeCallEnded, Message: "the request " + strconv.Quote(env.Call) + " no longer waits for this worker"}}, nil
		}
		defer p.running.Done()
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(p.ctx, cancel)
		defer stop()
		return answer(ctx, env, func(ctx context.Context) (any, error) { return p.callback(ctx, env) }), nil
	}
	if fn == nil {
		return &Envelope{ID: env.ID, Kind: KindResult, Error: &Error{Code: CodeUnknownKind, Message: "unknown kind " + strconv.Quote(env.Kind)}}, nil
	}
	return answer(ctx, env, func(ctx context.Context) (any, error) { return fn(ctx, machineID, env) }), nil
}

func (h *Hub) publish(s *Session, body json.RawMessage) error {
	var next Ready
	if err := json.Unmarshal(body, &next); err != nil || next.Kinds == nil || next.ChannelKinds == nil || next.Connections == nil {
		return &RemoteError{Code: "invalid_ready", Message: "ready requires kinds, channelKinds and connections arrays"}
	}
	ids := make(map[int64]bool, len(next.Connections))
	for _, c := range next.Connections {
		if c.ConnectionID <= 0 || ids[c.ConnectionID] {
			return &RemoteError{Code: "invalid_ready", Message: "ready requires unique positive connection IDs"}
		}
		ids[c.ConnectionID] = true
	}
	h.mu.Lock()
	if h.sessions[s.MachineID] != s || !s.alive() {
		h.mu.Unlock()
		return ErrReplaced
	}
	if reflect.DeepEqual(s.ready, &next) {
		h.mu.Unlock()
		return nil
	}
	s.ready = &next
	ready := h.onReady
	h.mu.Unlock()
	if ready != nil {
		ready(s.MachineID)
	}
	return nil
}

// answer runs a worker-initiated call and wraps its outcome as a result.
func answer(ctx context.Context, env Envelope, run func(context.Context) (any, error)) *Envelope {
	res := &Envelope{ID: env.ID, Kind: KindResult}
	body, err := run(ctx)
	if err != nil {
		res.Error = ErrorOf(err)
		return res
	}
	b, err := json.Marshal(body)
	if err != nil {
		res.Error = &Error{Code: CodeInternal, Message: err.Error()}
		return res
	}
	res.Body = b
	return res
}

// ErrorOf is err as a result error: a *RemoteError keeps its code, anything
// else is internal.
func ErrorOf(err error) *Error {
	var re *RemoteError
	if errors.As(err, &re) {
		return &Error{Code: re.Code, Message: re.Message}
	}
	return &Error{Code: CodeInternal, Message: err.Error()}
}

// Call sends a request to machineID's worker and waits for its result
// until timeout (or ctx) ends. ErrOffline when no channel is live (nothing
// was sent); ErrLostReply when the deadline passes or the stream ends
// first; *RemoteError when the worker answered with an error. When ctx
// ends first, a cancel is sent and ctx's error returned.
func (h *Hub) Call(ctx context.Context, machineID int64, kind string, connectionID int64, body any, timeout time.Duration) (json.RawMessage, error) {
	return h.CallWith(ctx, machineID, kind, connectionID, body, timeout, nil)
}

// CallWith is Call whose worker may call back while it waits: worker
// calls naming this request (Envelope.Call) run callback; once CallWith
// returns, they are refused with call_ended.
func (h *Hub) CallWith(ctx context.Context, machineID int64, kind string, connectionID int64, body any, timeout time.Duration, callback Callback) (json.RawMessage, error) {
	return h.CallWithTrait(ctx, machineID, kind, connectionID, body, timeout, callback, "")
}

// CallWithTrait requires the current ready snapshot to declare trait for the connection.
func (h *Hub) CallWithTrait(ctx context.Context, machineID int64, kind string, connectionID int64, body any, timeout time.Duration, callback Callback, trait string) (json.RawMessage, error) {
	h.mu.Lock()
	s := h.sessions[machineID]
	var ready *Ready
	if s != nil {
		ready = s.ready
	}
	h.mu.Unlock()
	if s == nil || !s.alive() {
		return nil, ErrOffline
	}
	if ready == nil {
		return nil, ErrNegotiating
	}
	if !slices.Contains(ready.ChannelKinds, kind) {
		return nil, &RemoteError{Code: CodeOperationUnavailable, Message: "Update Stage on the worker machine to use " + kind + "."}
	}
	if connectionID != 0 {
		i := slices.IndexFunc(ready.Connections, func(c Connection) bool { return c.ConnectionID == connectionID })
		if i < 0 {
			return nil, &RemoteError{Code: CodeOperationUnavailable, Message: "The worker has no adapter for this connection."}
		}
		if trait != "" && !slices.Contains(ready.Connections[i].Traits, trait) {
			return nil, &RemoteError{Code: CodeOperationUnavailable, Message: "This connection does not support " + kind + "."}
		}
	}
	var raw json.RawMessage
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	deadline := h.opts.Now().Add(timeout).UTC()
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d.UTC()
	}
	id := s.ID[:6] + ":" + strconv.FormatUint(h.seq.Add(1), 10)
	reply := make(chan Envelope, 1)
	waiting, stopWaiting := context.WithCancel(context.WithoutCancel(ctx))
	p := &pending{reply: reply, callback: callback, ctx: waiting}
	s.mu.Lock()
	s.pending[id] = p
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		stopWaiting()
		p.running.Wait()
	}()
	if !s.send(Envelope{ID: id, Kind: kind, Deadline: &deadline, ConnectionID: connectionID, Body: raw}) {
		return nil, ErrOffline
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case env := <-reply:
		if env.Error != nil {
			return nil, &RemoteError{Code: env.Error.Code, Message: env.Error.Message}
		}
		return env.Body, nil
	case <-s.done:
		return nil, ErrLostReply
	case <-timer.C:
		return nil, ErrReplyTimeout
	case <-ctx.Done():
		s.send(Envelope{ID: id, Kind: KindCancel})
		return nil, ctx.Err()
	}
}

// Outbound is the requests and cancels to write to the stream.
func (s *Session) Outbound() <-chan Envelope { return s.out }

// Done is closed when the session ends (replaced, detached, timed out, or
// the hub's context ended).
func (s *Session) Done() <-chan struct{} { return s.done }

func (s *Session) alive() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

// close ends the session once; true for the call that ended it.
func (s *Session) close() bool {
	first := false
	s.once.Do(func() { close(s.done); first = true })
	return first
}

// send queues env for the stream; false when the session ended or its
// queue is full (the stream is stuck: treat as offline).
func (s *Session) send(env Envelope) bool {
	if !s.alive() {
		return false
	}
	select {
	case s.out <- env:
		return true
	case <-s.done:
		return false
	default:
		return false
	}
}

// touch records a message from the worker and persists it when the last
// persist is older than PersistEvery.
func (s *Session) touch() {
	h := s.hub
	now := h.opts.Now()
	s.mu.Lock()
	s.lastSeen = now
	persist := now.Sub(s.persisted) >= h.opts.PersistEvery
	if persist {
		s.persisted = now
	}
	s.mu.Unlock()
	if persist {
		h.seen(s.MachineID, now, "")
	}
}

func (s *Session) resolve(env Envelope) {
	s.mu.Lock()
	p, ok := s.pending[env.ID]
	if ok {
		delete(s.pending, env.ID)
	}
	s.mu.Unlock()
	if ok {
		p.reply <- env // buffered 1, sole sender
	}
}
