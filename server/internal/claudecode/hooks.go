package claudecode

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Push events (ADR 0005 decision 16): every session Stage launches gets
// Claude HTTP hooks (`--settings <file>`) that POST to a private listener on
// 127.0.0.1. A hook can wake Stage: the adapter reads the session list
// and transcript again (Events). Polling stays the reconciliation, so a
// hook that never arrives (Stage down, a policy blocking HTTP hooks) only
// delays.
//
// Approvals and questions (ADR 0005 decision 8, #375): a PermissionRequest
// hook also stays open as a request the user can answer in Stage, until the
// user answers, Claude gives up (its timeout, or the process ends), the
// session stops waiting, a newer request of the same session supersedes it,
// or the receiver closes. Claude shows its own prompt meanwhile, so an
// answer in Claude still wins; every way the hook ends without a Stage
// answer leaves Claude's prompt in place. Open requests are memory only.

var observationHookEvents = []string{"Stop", "StopFailure", "Notification", "PermissionRequest", "SessionEnd", "UserPromptSubmit"}

// HookEvents are the Claude hook events new Stage launches subscribe to.
var HookEvents = append(slices.Clone(observationHookEvents), "PostToolUse", "PostToolBatch")

// HooksFile is the Claude settings file, in the hooks' state directory,
// that every launch passes with --settings. Claude stores the path with a
// session's saved options and reads the file again when it wakes the
// session, so the file always names the current listener.
const HooksFile = "hooks-settings-v1.json"

// Earlier launches keep their original hooks when Claude wakes them.
const legacyHooksFile = "hooks-settings.json"

// legacyHeadFile is the settings file earlier versions passed for a
// launch with a predecessor base. Sessions launched with it read it again
// when they wake, so an existing one keeps the current listener address.
const legacyHeadFile = "hooks-settings-head.json"

const (
	hookPath = "/hooks"
	// maxHookBody fits a PermissionRequest's tool input (a Write's whole
	// file).
	maxHookBody = 1 << 20
	// hookTimeout is how long Claude waits for Stage's answer (seconds);
	// Stage answers before it reads anything.
	hookTimeout = 5
	// permissionTimeout is how long Claude keeps a PermissionRequest hook
	// open for an answer in Stage (seconds; the daemon retires a session
	// idle for 60 minutes, waiting ones included).
	permissionTimeout = 3600
	// hookIOTimeout bounds reading a hook and writing Stage's answer.
	hookIOTimeout = 10 * time.Second
	// hookQueue bounds the nudges waiting for the adapter; beyond it a
	// nudge is dropped and polling reconciles.
	hookQueue = 256
)

// Events modes (EventsStatus.Mode).
const (
	ModePush    = "push"
	ModePolling = "polling"
)

// EventsStatus is how Stage hears about Claude sessions it launches, as the
// setup status reports it (details.events).
type EventsStatus struct {
	// Mode: push (hooks wake Stage, polling reconciles) or polling (no
	// hooks; Reason says why).
	Mode   string `json:"mode"`
	Reason string `json:"reason,omitempty"`
	// LastEventAt is when the last accepted hook arrived (null: none since
	// Stage started).
	LastEventAt *time.Time `json:"lastEventAt"`
}

// Nudge is one accepted hook: the session it names and why. It is a
// wake-up, never the session's state.
type Nudge struct {
	Session          string
	Event            string
	NotificationType string
	At               time.Time
}

// Hooks is the machine's hook receiver: a listener on 127.0.0.1 and the
// settings file that points Claude at it. One adapter (the connection's
// current one) subscribes at a time.
type Hooks struct {
	path      string
	imageRoot string
	url       string
	token     string
	ln        net.Listener
	srv       *http.Server
	now       func() time.Time
	io        time.Duration

	mu         sync.Mutex
	sub        *Subscription
	last       time.Time
	pending    map[string][]*pendingRequest // by session id
	deliveries map[string]*deliveryInbox    // by session id
}

// pendingRequest is one open PermissionRequest hook.
type pendingRequest struct {
	id          string
	session     string
	created     time.Time
	tool        string
	input       json.RawMessage
	suggestions []json.RawMessage

	claimed bool        // an answer or release took it (Hooks.mu)
	answer  chan []byte // the response body (nil: no decision); buffered
	result  chan error  // the handler's write result; buffered
}

// Subscription delivers nudges to one adapter until it is superseded or
// reset.
type Subscription struct {
	ch   chan Nudge
	done chan struct{}
	once sync.Once
}

// Nudges are the accepted hooks, in arrival order.
func (s *Subscription) Nudges() <-chan Nudge { return s.ch }

// Done closes when another adapter subscribed or the subscription was
// reset.
func (s *Subscription) Done() <-chan struct{} { return s.done }

func (s *Subscription) end() { s.once.Do(func() { close(s.done) }) }

// HooksOptions configure OpenHooks.
type HooksOptions struct {
	// Dir holds HooksFile (created 0700).
	Dir string
	// ImageRoot is the adapter's image root (Options.ImageRoot): the
	// settings let Claude read it (ReadRule). Empty: no rule.
	ImageRoot string
	Logger    *slog.Logger
	Now       func() time.Time
	// IOTimeout bounds reading a hook and writing an answer (default 10s);
	// tests shorten it.
	IOTimeout time.Duration
}

// OpenHooks starts the receiver. It keeps the token and, when it can bind
// it again, the port that the existing settings file names, so sessions
// launched before a restart keep reaching Stage; otherwise it takes a free
// port and rewrites the file. The error's message is a sentence for the
// setup status.
func OpenHooks(o HooksOptions) (*Hooks, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.IOTimeout == 0 {
		o.IOTimeout = hookIOTimeout
	}
	if o.Dir == "" {
		return nil, errors.New("Stage has no folder for its Claude Code hook settings.")
	}
	path := filepath.Join(o.Dir, HooksFile)
	port, token := readHookSettings(path)
	if token == "" {
		port, token = readHookSettings(filepath.Join(o.Dir, legacyHooksFile))
	}
	if token == "" {
		var b [32]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, fmt.Errorf("Stage couldn't make a token for its Claude Code hooks (%v).", err)
		}
		token = hex.EncodeToString(b[:])
	}
	var ln net.Listener
	if port > 0 {
		ln, _ = net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	}
	if ln == nil {
		var err error
		if ln, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
			return nil, fmt.Errorf("Stage couldn't open its local hook port (%v).", err)
		}
	}
	tcp, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		return nil, fmt.Errorf("Stage couldn't open its local hook port (unexpected address %s).", ln.Addr())
	}
	h := &Hooks{path: path, imageRoot: o.ImageRoot, url: "http://127.0.0.1:" + strconv.Itoa(tcp.Port) + hookPath,
		token: token, ln: ln, now: o.Now, io: o.IOTimeout, pending: map[string][]*pendingRequest{}, deliveries: map[string]*deliveryInbox{}}
	if err := h.writeSettings(o.Dir); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("Stage couldn't write its Claude Code hook settings (%v).", err)
	}
	h.srv = &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: h.io, WriteTimeout: h.io}
	log := o.Logger.With("component", "claude-hooks")
	go func() {
		if err := h.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Warn("claude hook listener stopped", "err", err)
		}
	}()
	log.Info("claude hook listener", "url", h.url, "settings", path)
	return h, nil
}

// ImageRoot is the image root the settings files let Claude read.
func (h *Hooks) ImageRoot() string { return h.imageRoot }

// SettingsPath is the file a launch passes with --settings.
func (h *Hooks) SettingsPath() string { return h.path }

// URL is where Claude posts hooks.
func (h *Hooks) URL() string { return h.url }

// Close releases every open request (Claude keeps its prompt) and stops
// the listener; its port is free when Close returns.
func (h *Hooks) Close() error {
	h.Reset()
	h.mu.Lock()
	for _, ps := range h.pending {
		for _, p := range ps {
			h.claimLocked(p, nil)
		}
	}
	h.pending = map[string][]*pendingRequest{}
	h.mu.Unlock()
	// The released requests answer first; then connections close.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := h.srv.Shutdown(ctx)
	if err != nil {
		err = h.srv.Close()
	}
	// Serve may not hold the listener yet: close it here too.
	if lerr := h.ln.Close(); lerr != nil && !errors.Is(lerr, net.ErrClosed) && err == nil {
		err = lerr
	}
	return err
}

// Subscribe makes the caller the one adapter that receives nudges; the
// previous subscription ends.
func (h *Hooks) Subscribe() *Subscription {
	s := &Subscription{ch: make(chan Nudge, hookQueue), done: make(chan struct{})}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sub != nil {
		h.sub.end()
	}
	h.sub = s
	return s
}

// Unsubscribe ends s if it is still the current subscription.
func (h *Hooks) Unsubscribe(s *Subscription) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sub == s {
		h.sub = nil
	}
	s.end()
}

// Reset ends the current subscription: the setup replaced or removed the
// connection's adapter and drops queued deliveries. Hooks arriving
// meanwhile are accepted.
func (h *Hooks) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	clear(h.deliveries)
	if h.sub != nil {
		h.sub.end()
		h.sub = nil
	}
}

// Status is push, with the last accepted hook.
func (h *Hooks) Status() EventsStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := EventsStatus{Mode: ModePush}
	if !h.last.IsZero() {
		t := h.last.UTC()
		st.LastEventAt = &t
	}
	return st
}

// hookInput is the part of Claude's hook input Stage reads. A
// PermissionRequest's tool and input are kept in memory while it is open,
// never logged or stored; the rest (the prompt, the last reply) is never
// read.
type hookInput struct {
	Event            string            `json:"hook_event_name"`
	Session          string            `json:"session_id"`
	NotificationType string            `json:"notification_type"`
	ToolName         string            `json:"tool_name"`
	ToolInput        json.RawMessage   `json:"tool_input"`
	Suggestions      []json.RawMessage `json:"permission_suggestions"`
	StopHookActive   bool              `json:"stop_hook_active"`
}

// eventPermissionRequest is the hook Stage can answer.
const eventPermissionRequest = "PermissionRequest"

// ServeHTTP accepts a hook: POST /hooks with Stage's bearer token and a
// JSON body naming a session. It returns a delivery decision at a tool or
// Stop boundary, holds a PermissionRequest, or answers 204 with no decision.
func (h *Hooks) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != hookPath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.authorized(r.Header.Get("Authorization")) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var in hookInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxHookBody)).Decode(&in); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "hook input too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "malformed hook input", http.StatusBadRequest)
		return
	}
	if in.Event == "" || !validUUID(in.Session) {
		http.Error(w, "hook input names no session", http.StatusBadRequest)
		return
	}
	versioned := r.Header.Get(hookVersionHeader) == hookVersion
	if versioned && in.Event == "UserPromptSubmit" {
		// A launch may be correlated after --bg returned an uncertain result.
		// Its authenticated versioned hook independently records eligibility.
		_ = h.markDeliveryHooks(in.Session)
	}
	if in.Event == "SessionEnd" {
		h.clearDeliveries(in.Session)
	}
	var output deliveryHookOutput
	var hasDelivery bool
	if versioned {
		output, hasDelivery = h.deliveryOutput(in)
	}
	// Activity is polled. Tool boundaries and a Stop that continues the
	// turn do not announce a roster state change.
	h.accept(Nudge{Session: in.Session, Event: in.Event, NotificationType: in.NotificationType, At: h.now()}, !hasDelivery && !isToolHook(in.Event))
	if hasDelivery {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(output)
		return
	}
	if in.Event == eventPermissionRequest && in.ToolName != "" {
		h.hold(w, r, in)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// hold keeps a PermissionRequest open until it is answered (200 with the
// decision) or released (204, no decision). Claude closing the connection
// drops it.
func (h *Hooks) hold(w http.ResponseWriter, r *http.Request, in hookInput) {
	// The server's read and write bounds would end the request while it
	// waits; they apply again to the answer.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})
	p := h.add(in)
	var body []byte
	select {
	case body = <-p.answer:
	case <-r.Context().Done():
		h.drop(p)
		p.result <- execution.ErrNotFound
		return
	}
	h.drop(p)
	_ = rc.SetWriteDeadline(time.Now().Add(h.io))
	if body == nil {
		w.WriteHeader(http.StatusNoContent)
		p.result <- nil
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(body)
	if err == nil {
		err = rc.Flush()
	}
	p.result <- err
}

// add registers a request, releasing the session's older ones (Claude asks
// one at a time: an older one was answered in Claude).
func (h *Hooks) add(in hookInput) *pendingRequest {
	p := &pendingRequest{
		id: randomHex(8), session: in.Session, created: h.now().UTC(), tool: in.ToolName, input: in.ToolInput, suggestions: in.Suggestions,
		answer: make(chan []byte, 1), result: make(chan error, 1),
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, old := range h.pending[in.Session] {
		h.claimLocked(old, nil)
	}
	h.pending[in.Session] = []*pendingRequest{p}
	return p
}

// drop removes p from the open requests.
func (h *Hooks) drop(p *pendingRequest) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ps := h.pending[p.session]
	for i, q := range ps {
		if q == p {
			ps = append(ps[:i:i], ps[i+1:]...)
			break
		}
	}
	if len(ps) == 0 {
		delete(h.pending, p.session)
	} else {
		h.pending[p.session] = ps
	}
}

// claimLocked hands p its response body once (h.mu held); false when an
// answer or release already took it.
func (h *Hooks) claimLocked(p *pendingRequest, body []byte) bool {
	if p.claimed {
		return false
	}
	p.claimed = true
	p.answer <- body
	return true
}

// open are the session's unclaimed requests.
func (h *Hooks) open(session string) []*pendingRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []*pendingRequest
	for _, p := range h.pending[session] {
		if !p.claimed {
			out = append(out, p)
		}
	}
	return out
}

// find is the session's unclaimed request id.
func (h *Hooks) find(session, id string) (*pendingRequest, bool) {
	for _, p := range h.open(session) {
		if p.id == id {
			return p, true
		}
	}
	return nil, false
}

// release answers the session's open requests with no decision (Claude
// keeps its prompt).
func (h *Hooks) release(session string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, p := range h.pending[session] {
		h.claimLocked(p, nil)
	}
}

// answer sends body to request id and waits until Claude got it.
// ErrNotFound: no longer open, or Claude went away first.
func (h *Hooks) answer(ctx context.Context, session, id string, body []byte) error {
	h.mu.Lock()
	var p *pendingRequest
	for _, q := range h.pending[session] {
		if q.id == id {
			p = q
		}
	}
	if p == nil || !h.claimLocked(p, body) {
		h.mu.Unlock()
		return execution.ErrNotFound
	}
	h.mu.Unlock()
	select {
	case err := <-p.result:
		if errors.Is(err, execution.ErrNotFound) {
			return err
		}
		if err != nil {
			return fmt.Errorf("answer Claude Code: %w", err)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (h *Hooks) authorized(header string) bool {
	got, ok := strings.CutPrefix(header, "Bearer ")
	return ok && subtle.ConstantTimeCompare([]byte(got), []byte(h.token)) == 1
}

// accept records an accepted hook and optionally wakes the adapter. A full
// nudge channel drops the wake-up; polling reconciles.
func (h *Hooks) accept(n Nudge, wake bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.last = n.At
	if !wake || h.sub == nil {
		return
	}
	select {
	case h.sub.ch <- n:
	default:
	}
}

// Claude's settings shape, as far as Stage writes it (a file or a launch's
// JSON).
type settingsFile struct {
	Hooks       map[string][]hookMatcher `json:"hooks,omitempty"`
	Permissions *permissionSettings      `json:"permissions,omitempty"`
}

type permissionSettings struct {
	Allow []string `json:"allow"`
}

type hookMatcher struct {
	Hooks []httpHook `json:"hooks"`
}

type httpHook struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Timeout int               `json:"timeout"`
}

// HookSettings is the settings file's content: one HTTP hook per event,
// no matcher. PermissionRequest waits up to permissionTimeout for an
// answer in Stage; Claude prompts meanwhile.
func HookSettings(hookURL, token string) ([]byte, error) {
	return hookSettings(hookURL, token, "", true)
}

// hookSettings is HookSettings with, for an image root, its ReadRule.
func hookSettings(hookURL, token string, imageRoot string, versioned bool) ([]byte, error) {
	f := settingsFile{Hooks: map[string][]hookMatcher{}}
	if imageRoot != "" {
		f.Permissions = &permissionSettings{Allow: []string{ReadRule(imageRoot)}}
	}
	events := observationHookEvents
	if versioned {
		events = HookEvents
	}
	for _, e := range events {
		timeout := hookTimeout
		if e == eventPermissionRequest {
			timeout = permissionTimeout
		}
		headers := map[string]string{"Authorization": "Bearer " + token}
		if versioned {
			headers[hookVersionHeader] = hookVersion
		}
		f.Hooks[e] = []hookMatcher{{Hooks: []httpHook{{Type: "http", URL: hookURL, Headers: headers, Timeout: timeout}}}}
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// readHookSettings is the port and token an existing settings file names
// (0 and "" when it is missing or not Stage's).
func readHookSettings(path string) (int, string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, ""
	}
	var f settingsFile
	if json.Unmarshal(b, &f) != nil {
		return 0, ""
	}
	for _, m := range f.Hooks["Stop"] {
		for _, hk := range m.Hooks {
			u, err := url.Parse(hk.URL)
			token, ok := strings.CutPrefix(hk.Headers["Authorization"], "Bearer ")
			if err != nil || !ok || token == "" || u.Hostname() != "127.0.0.1" {
				continue
			}
			port, _ := strconv.Atoi(u.Port())
			return port, token
		}
	}
	return 0, ""
}

// writeSettings updates the listener address for new and earlier launches.
// Files are private and replaced atomically only when their content changes.
func (h *Hooks) writeSettings(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := hookSettings(h.url, h.token, h.imageRoot, true)
	if err != nil {
		return err
	}
	if err := writeFile0600(dir, h.path, b); err != nil {
		return err
	}
	for _, name := range []string{legacyHooksFile, legacyHeadFile} {
		legacy := filepath.Join(dir, name)
		if _, err := os.Stat(legacy); err != nil {
			continue
		}
		b, err := hookSettings(h.url, h.token, h.imageRoot, false)
		if err != nil {
			return err
		}
		if err := writeFile0600(dir, legacy, b); err != nil {
			return err
		}
	}
	return nil
}

// writeFile0600 replaces path (in dir) with b, atomically, unless it
// already holds b.
func writeFile0600(dir, path string, b []byte) error {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, b) {
		return os.Chmod(path, 0o600)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
