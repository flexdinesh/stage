package claudecode

import (
	"bytes"
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
	"strconv"
	"strings"
	"sync"
	"time"
)

// Push events (ADR 0005 decision 16): every session Stage launches gets
// Claude HTTP hooks (`--settings <file>`) that POST to a private listener on
// 127.0.0.1. A hook only wakes Stage: the adapter reads the session list
// and transcript again (Events). Polling stays the reconciliation, so a
// hook that never arrives (Stage down, a policy blocking HTTP hooks) only
// delays.

// HookEvents are the Claude hook events Stage subscribes to.
var HookEvents = []string{"Stop", "StopFailure", "Notification", "PermissionRequest", "SessionEnd", "UserPromptSubmit"}

// HooksFile is the Claude settings file, in the hooks' state directory,
// that every launch passes with --settings. Claude stores the path with a
// session's saved options and reads the file again when it wakes the
// session, so the file always names the current listener.
const HooksFile = "hooks-settings.json"

// HooksHeadFile is HooksFile plus worktree.baseRef "head", for a launch
// with a predecessor base (Claude takes one --settings; base.go).
const HooksHeadFile = "hooks-settings-head.json"

const (
	hookPath    = "/hooks"
	maxHookBody = 64 << 10
	// hookTimeout is how long Claude waits for Stage's answer (seconds);
	// Stage answers before it reads anything.
	hookTimeout = 5
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
	path  string
	url   string
	token string
	ln    net.Listener
	srv   *http.Server
	now   func() time.Time

	mu   sync.Mutex
	sub  *Subscription
	last time.Time
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
	Dir    string
	Logger *slog.Logger
	Now    func() time.Time
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
	if o.Dir == "" {
		return nil, errors.New("Stage has no folder for its Claude Code hook settings.")
	}
	path := filepath.Join(o.Dir, HooksFile)
	port, token := readHookSettings(path)
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
	h := &Hooks{path: path, url: "http://127.0.0.1:" + strconv.Itoa(tcp.Port) + hookPath, token: token, ln: ln, now: o.Now}
	if err := h.writeSettings(o.Dir); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("Stage couldn't write its Claude Code hook settings (%v).", err)
	}
	h.srv = &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
	log := o.Logger.With("component", "claude-hooks")
	go func() {
		if err := h.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Warn("claude hook listener stopped", "err", err)
		}
	}()
	log.Info("claude hook listener", "url", h.url, "settings", path)
	return h, nil
}

// SettingsPath is the file a launch passes with --settings.
func (h *Hooks) SettingsPath() string { return h.path }

// HeadSettingsPath is the file a launch with a predecessor base passes
// with --settings (HooksHeadFile).
func (h *Hooks) HeadSettingsPath() string { return filepath.Join(filepath.Dir(h.path), HooksHeadFile) }

// URL is where Claude posts hooks.
func (h *Hooks) URL() string { return h.url }

// Close stops the listener; its port is free when Close returns.
func (h *Hooks) Close() error {
	h.Reset()
	err := h.srv.Close()
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
// connection's adapter. Hooks arriving meanwhile are accepted and dropped.
func (h *Hooks) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
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

// hookInput is the part of Claude's hook input Stage reads. The rest (the
// prompt, the last reply, tool input) is never read, logged or stored.
type hookInput struct {
	Event            string `json:"hook_event_name"`
	Session          string `json:"session_id"`
	NotificationType string `json:"notification_type"`
}

// ServeHTTP accepts a hook: POST /hooks with Stage's bearer token and a
// JSON body naming a session. It answers 204 with no body (Claude reads no
// decision from it) before anything else happens.
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
	h.deliver(Nudge{Session: in.Session, Event: in.Event, NotificationType: in.NotificationType, At: h.now()})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Hooks) authorized(header string) bool {
	got, ok := strings.CutPrefix(header, "Bearer ")
	return ok && subtle.ConstantTimeCompare([]byte(got), []byte(h.token)) == 1
}

// deliver queues n for the subscribed adapter; with none, or a full
// queue, it is dropped (polling reconciles).
func (h *Hooks) deliver(n Nudge) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.last = n.At
	if h.sub == nil {
		return
	}
	select {
	case h.sub.ch <- n:
	default:
	}
}

// Claude's settings file shape, as far as Stage writes it.
type hookSettingsFile struct {
	Hooks    map[string][]hookMatcher `json:"hooks"`
	Worktree *worktreeSettings        `json:"worktree,omitempty"`
}

type worktreeSettings struct {
	BaseRef string `json:"baseRef"`
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
// no matcher.
func HookSettings(hookURL, token string) ([]byte, error) {
	return hookSettings(hookURL, token, nil)
}

// hookSettings is HookSettings with an optional worktree section.
func hookSettings(hookURL, token string, wt *worktreeSettings) ([]byte, error) {
	f := hookSettingsFile{Hooks: map[string][]hookMatcher{}, Worktree: wt}
	for _, e := range HookEvents {
		f.Hooks[e] = []hookMatcher{{Hooks: []httpHook{{Type: "http", URL: hookURL, Headers: map[string]string{"Authorization": "Bearer " + token}, Timeout: hookTimeout}}}}
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
	var f hookSettingsFile
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

// writeSettings writes the settings files (0600, in a 0700 dir) when their
// content changed, atomically: HooksFile and its head variant, so both name
// the current listener.
func (h *Hooks) writeSettings(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := hookSettings(h.url, h.token, nil)
	if err != nil {
		return err
	}
	if err := writeFile0600(dir, h.path, b); err != nil {
		return err
	}
	head, err := hookSettings(h.url, h.token, &worktreeSettings{BaseRef: "head"})
	if err != nil {
		return err
	}
	return writeFile0600(dir, h.HeadSettingsPath(), head)
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
