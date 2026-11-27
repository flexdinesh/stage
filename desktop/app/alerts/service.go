// Package alerts is the desktop app's native notification consumer
// (docs/desktop.md, Notifications): it follows the notification stream of
// every saved server at once (this machine's over its Unix socket,
// remotes over TCP), decides what to show (catch-up, foreground, toggle,
// grouping), keeps each server's cursor and the device preferences,
// reconciles shown notifications with each server's unread snapshot and
// sets the badge to the sum of their badge counts. It is pure Go,
// without MyGo: the OS side is a Sink.
package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/flexdinesh/stage/desktop/app/target"
)

// ErrDenied: the OS refuses notifications from the app (macOS
// authorization denied). Sink.Show returns it.
var ErrDenied = errors.New("notifications are not allowed in system settings")

// Note is one OS notification. A Show with the ID of a shown notification
// replaces it.
type Note struct {
	ID    string
	Title string
	Body  string
}

// Sink is the OS notification and badge service. Its methods may be called
// from different goroutines.
type Sink interface {
	// Show shows n; ErrDenied when the OS refuses notifications. It may
	// block (the first macOS authorization prompt) until ctx ends.
	Show(ctx context.Context, n Note) error
	// Close removes the notification with id, if shown.
	Close(id string)
	// CloseAll removes every notification of the app.
	CloseAll()
	// SetBadge sets the Dock/launcher count (0 clears it).
	SetBadge(n int)
	// Supported reports whether the system has a notification service.
	Supported() bool
}

// State is the device notification state the Settings page shows
// (PushState of the platform contract).
type State string

// States, in precedence order.
const (
	StateUnsupported State = "unsupported"
	StateOff         State = "off"
	StateBlocked     State = "blocked"
	StateOn          State = "on"
)

// showTimeout bounds one Show (the first macOS prompt waits for the user).
const showTimeout = 2 * time.Minute

// saveDelay debounces cursor and shown-list writes of live rows.
const saveDelay = time.Second

// Config configures a Service.
type Config struct {
	// PrefsPath is <userData>/notifications.json; "" keeps preferences in
	// memory only.
	PrefsPath string
	// Sink shows notifications and sets the badge.
	Sink Sink
	// Foreground reports whether the main window is visible, focused and
	// showing server id (then its rows show as in-app toasts only; other
	// servers' rows still show as OS notifications). Called from stream
	// goroutines; nil means never.
	Foreground func(serverID string) bool
	// Delivered runs after new unread rows of a server arrived (once per
	// catch-up batch, with its newest id; once per live row), shown or not.
	// It must not block. Nil means no-op.
	Delivered func(serverID string, id int64)
	// Changed runs when a server's badge count (Badges) changed. It
	// must not block. Nil means no-op.
	Changed func()
	// AccessEnded reports a rejected native sign-in after clearing its alerts.
	// The credential owner should clear its stored session only if the exact
	// target and session ID still match. It must not block. Nil means no-op.
	AccessEnded func(t target.Target, sessionID string)
	// RestoreSession supplies a saved sign-in before its target starts streaming.
	RestoreSession func(target.Target) Session
	// Now is the clock (default time.Now).
	Now func() time.Time
	// Logf logs (default log.Printf).
	Logf func(format string, args ...any)
	// MinBackoff and MaxBackoff bound the jittered reconnect delay
	// (default 1s and 30s).
	MinBackoff, MaxBackoff time.Duration
	// Window is how old a catch-up row may be to show (default 15 min).
	Window time.Duration
	// Max is how many catch-up groups show per server before its summary
	// (default 3).
	Max int
}

type shownNote struct {
	id  int64
	url string
}

// queued is a notification waiting for the delivery worker.
type queued struct {
	note Note
	// server is the server id, "" for device notes (test).
	server string
	id     int64
	url    string
	// seq is the server's unread snapshot sequence when it was queued.
	seq      uint64
	delivery context.Context
}

// stream is one server's notification stream and its notifications.
// Fields are guarded by Service.mu.
type stream struct {
	id     string
	t      target.Target
	ctx    context.Context // ends the loop when the server is removed
	stop   context.CancelFunc
	wake   chan struct{}
	gen    uint64
	cancel context.CancelFunc // the open connection
	looped bool

	noStreamLogged bool
	awaitingAccess bool
	session        Session
	delivery       context.Context
	cancelDelivery context.CancelFunc
	shown          map[string]shownNote // by note ID
	summaryTags    []string
	unread         map[string]bool
	snapSeq        uint64
	badge          int
	hasBadge       bool
}

func (st *stream) current(gen uint64) bool { return st.gen == gen && st.ctx.Err() == nil }

// Service consumes the notification streams of every saved server.
// Create it with New, then Start; SetServers on every list change;
// Connect on every (re)start of a server; Close on exit.
type Service struct {
	cfg    Config
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	saveMu sync.Mutex

	supportedOnce sync.Once
	supported     bool

	queueWake chan struct{}
	saveWake  chan struct{}

	mu            sync.Mutex
	prefs         Prefs
	dirty         bool
	started       bool
	closed        bool
	streams       map[string]*stream
	order         []string // stream ids in list order
	queue         []queued
	pendingCloses map[string]struct{}
	busy          bool
	badge         int
	badgeSet      bool
}

// New loads the preferences (a corrupt file is logged and replaced by
// defaults; a version 1 file migrates to this machine's entry) and
// returns a stopped Service without servers.
func New(cfg Config) *Service {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logf == nil {
		cfg.Logf = log.Printf
	}
	if cfg.MinBackoff <= 0 {
		cfg.MinBackoff = time.Second
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 30 * time.Second
	}
	cfg.MaxBackoff = max(cfg.MaxBackoff, cfg.MinBackoff)
	if cfg.Window <= 0 {
		cfg.Window = 15 * time.Minute
	}
	if cfg.Max <= 0 {
		cfg.Max = 3
	}
	s := &Service{
		cfg:           cfg,
		queueWake:     make(chan struct{}, 1),
		saveWake:      make(chan struct{}, 1),
		prefs:         DefaultPrefs(),
		streams:       map[string]*stream{},
		pendingCloses: map[string]struct{}{},
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	if cfg.PrefsPath != "" {
		p, err := LoadPrefs(cfg.PrefsPath)
		if err != nil {
			s.logf("preferences: %v; using defaults", err)
		}
		s.prefs = p
		if p.migrated {
			s.dirty = true
		}
	}
	return s
}

func (s *Service) logf(format string, args ...any) {
	s.cfg.Logf("notifications: "+format, args...)
}

func signal(c chan struct{}) {
	select {
	case c <- struct{}{}:
	default:
	}
}

// Start runs the delivery worker, the debounced saver and a stream loop
// per server. It does nothing after the first call or after Close.
func (s *Service) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.closed {
		return
	}
	s.started = true
	s.wg.Add(2)
	go s.worker()
	go s.saver()
	for _, id := range s.order {
		s.startLoopLocked(s.streams[id])
	}
	if s.dirty {
		signal(s.saveWake)
	}
}

func (s *Service) startLoopLocked(st *stream) {
	if !s.started || s.closed || st.looped {
		return
	}
	st.looped = true
	s.wg.Add(1)
	go s.loop(st)
}

// SetServers makes ts the servers to follow, in list order: new ones get
// a stream, removed ones lose theirs (their shown notifications close and
// their cursor is forgotten), and a server at a new address reconnects. A
// local target without a socket keeps the socket already known.
func (s *Service) SetServers(ts []target.Target) {
	s.mu.Lock()
	keep := map[string]bool{}
	var countChanged, cleared bool
	s.order = s.order[:0]
	for _, t := range ts {
		if t.ID == "" || keep[t.ID] {
			continue
		}
		keep[t.ID] = true
		s.order = append(s.order, t.ID)
		count, dropped := s.upsertLocked(t, false)
		countChanged, cleared = countChanged || count, cleared || dropped
	}
	for id, st := range s.streams {
		if keep[id] {
			continue
		}
		count, dropped := s.clearAccessLocked(st)
		countChanged, cleared = countChanged || count, cleared || dropped
		st.stop()
		delete(s.streams, id)
	}
	setBadge := s.badgeLocked()
	s.mu.Unlock()
	s.accessChanged(countChanged, cleared, setBadge)
}

// Connect (re)connects the stream of t now (adding t when it is new).
// Call it on every ready of a server. A stream waiting for sign-in
// resumes through SetSession.
func (s *Service) Connect(t target.Target) {
	if t.ID == "" {
		return
	}
	s.mu.Lock()
	if _, ok := s.streams[t.ID]; !ok {
		s.order = append(s.order, t.ID)
	}
	countChanged, cleared := s.upsertLocked(t, true)
	setBadge := s.badgeLocked()
	s.mu.Unlock()
	s.accessChanged(countChanged, cleared, setBadge)
}

// upsertLocked adds or updates the stream of t; it reconnects when force
// or the address changed.
func (s *Service) upsertLocked(t target.Target, force bool) (countChanged, cleared bool) {
	st, ok := s.streams[t.ID]
	if !ok {
		ctx, stop := context.WithCancel(s.ctx)
		st = &stream{id: t.ID, t: t, ctx: ctx, stop: stop, wake: make(chan struct{}, 1), shown: map[string]shownNote{}}
		st.delivery, st.cancelDelivery = context.WithCancel(ctx)
		s.streams[t.ID] = st
		countChanged, cleared = s.restoreSessionLocked(st)
		s.startLoopLocked(st)
		return countChanged, cleared
	}
	if t.Kind == target.KindLocal && t.Socket == "" {
		t.Socket = st.t.Socket
	}
	moved := !st.t.Same(t)
	if moved && (st.t.Remote() || st.t.Socket != "") {
		countChanged, cleared = s.clearAccessLocked(st)
		st.awaitingAccess = false
	}
	st.t = t
	if moved {
		count, dropped := s.restoreSessionLocked(st)
		countChanged, cleared = countChanged || count, cleared || dropped
	}
	if force || moved {
		s.reconnectLocked(st)
	}
	return countChanged, cleared
}

func (s *Service) restoreSessionLocked(st *stream) (countChanged, cleared bool) {
	if s.cfg.RestoreSession == nil || (!st.t.Remote() && st.t.Socket == "") {
		return false, false
	}
	session := s.cfg.RestoreSession(st.t)
	if sp := s.prefs.Servers[st.id]; sp != nil && sp.SessionScope != session.scope(st.t) {
		countChanged, cleared = s.clearAccessLocked(st)
	}
	st.session = session
	return countChanged, cleared
}

func (s *Service) reconnectLocked(st *stream) {
	st.gen++
	st.noStreamLogged = false
	if st.cancel != nil {
		st.cancel()
	}
	signal(st.wake)
}

// Flush writes pending preference changes now.
func (s *Service) Flush() error {
	return s.save(false)
}

// Close stops the streams and the worker and flushes the preferences.
func (s *Service) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	s.wg.Wait()
	return s.Flush()
}

// State is the device state: unsupported, off, blocked or on.
func (s *Service) State() State {
	s.supportedOnce.Do(func() { s.supported = s.cfg.Sink.Supported() })
	if !s.supported {
		return StateUnsupported
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case !s.prefs.Enabled:
		return StateOff
	case s.prefs.Denied:
		return StateBlocked
	}
	return StateOn
}

// Badges is each server's count, for the servers whose
// stream sent an unread snapshot.
func (s *Service) Badges() map[string]int {
	return s.badges(false)
}

// NeedsInput is the count shown in the server list for personal servers.
func (s *Service) NeedsInput() map[string]int {
	return s.badges(true)
}

func (s *Service) badges(personalOnly bool) map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int{}
	for id, st := range s.streams {
		if st.hasBadge && (!personalOnly || st.session.ID == "") {
			out[id] = st.badge
		}
	}
	return out
}

// Enable turns notifications on and, at the user gesture, shows "Notifications
// are on" (on macOS the first Show asks for authorization). A denial leaves
// the state blocked and is not an error; other Show errors are.
func (s *Service) Enable(ctx context.Context) error {
	s.mu.Lock()
	s.prefs.Enabled = true
	s.mu.Unlock()
	s.saveLogged()
	err := s.showNow(ctx, Note{ID: TestID, Title: "Stage", Body: "Notifications are on"})
	if errors.Is(err, ErrDenied) {
		return nil
	}
	return err
}

// Disable turns notifications off and closes shown ones. The streams keep
// running (badge, cursors).
func (s *Service) Disable() {
	s.mu.Lock()
	s.prefs.Enabled = false
	for _, st := range s.streams {
		s.resetDeliveryLocked(st)
		s.clearLocked(st)
	}
	for _, sp := range s.prefs.Servers {
		sp.Shown = []string{}
	}
	s.markDirtyLocked()
	s.mu.Unlock()
	s.saveLogged()
	s.cfg.Sink.CloseAll()
}

// Test shows "Test notification" whether or not the window is in front; a
// click opens Settings. ErrDenied marks the state blocked.
func (s *Service) Test(ctx context.Context) error {
	return s.showNow(ctx, Note{ID: TestID, Title: "Stage", Body: "Test notification"})
}

// CloseShown closes server's notification for tag (task-12, session-x,
// summary), or all of that server's for "".
func (s *Service) CloseShown(server, tag string) {
	var closes []string
	s.mu.Lock()
	st := s.streams[server]
	switch {
	case st == nil:
	case tag == "":
		for id := range st.shown {
			closes = append(closes, id)
		}
		if sp := s.prefs.Servers[server]; sp != nil {
			closes = append(closes, sp.Shown...)
		}
		s.queue = slices.DeleteFunc(s.queue, func(q queued) bool { return q.server == server })
		s.clearLocked(st)
		s.prefs.server(server).Shown = []string{}
	default:
		id := noteKey(server, tag)
		closes = append(closes, id)
		s.forgetLocked(st, id)
		s.queue = slices.DeleteFunc(s.queue, func(q queued) bool { return q.note.ID == id })
		if tag == SummaryID {
			st.summaryTags = nil
		}
	}
	s.markDirtyLocked()
	s.mu.Unlock()
	slices.Sort(closes)
	for _, id := range slices.Compact(closes) {
		s.cfg.Sink.Close(id)
	}
}

// Sync closes shown notifications of server ("" for every server) whose
// groups have no unread rows in its latest unread snapshot.
func (s *Service) Sync(server string) {
	var closes []string
	s.mu.Lock()
	for id, st := range s.streams {
		if (server == "" || id == server) && st.snapSeq > 0 {
			closes = append(closes, s.reconcileLocked(st)...)
		}
	}
	s.mu.Unlock()
	for _, id := range closes {
		s.cfg.Sink.Close(id)
	}
}

// Target is where a click on notification id goes: the server it came
// from ("" for the device's test note) and the app path there (see
// Target). A version 1 ID without a server ("task-7") is this machine's.
func (s *Service) Target(id string) (server, path string) {
	if id == TestID {
		return "", Target(id, nil)
	}
	server, tag, ok := strings.Cut(id, "/")
	if !ok {
		server, tag = target.LocalID, id
	}
	known := map[string]string{}
	s.mu.Lock()
	if st := s.streams[server]; st != nil {
		if n, ok := st.shown[id]; ok {
			known[tag] = n.url
		} else if st.awaitingAccess || st.session.ID != "" {
			sp := s.prefs.Servers[server]
			if sp == nil || sp.SessionScope != st.session.scope(st.t) || !slices.Contains(sp.Shown, id) {
				s.mu.Unlock()
				return "", "/"
			}
		} else if sp := s.prefs.Servers[server]; sp != nil && sp.SessionScope != "" {
			s.mu.Unlock()
			return "", "/"
		}
	}
	s.mu.Unlock()
	return server, Target(tag, known)
}

// clearLocked forgets every shown notification of st.
func (s *Service) clearLocked(st *stream) {
	st.shown = map[string]shownNote{}
	st.summaryTags = nil
	s.markDirtyLocked()
}

// forgetLocked drops note id from st's shown notifications.
func (s *Service) forgetLocked(st *stream, id string) {
	delete(st.shown, id)
	if sp := s.prefs.Servers[st.id]; sp != nil && sp.removeShown(id) {
		s.markDirtyLocked()
	}
}

func (s *Service) changed() {
	if s.cfg.Changed != nil {
		s.cfg.Changed()
	}
}

// badgeLocked updates the sum and reports if the OS badge needs updating.
func (s *Service) badgeLocked() bool {
	sum, known := 0, false
	for _, st := range s.streams {
		if st.hasBadge {
			sum += st.badge
			known = true
		}
	}
	if !known && !s.badgeSet {
		return false
	}
	if s.badgeSet && sum == s.badge {
		return false
	}
	s.badge, s.badgeSet = sum, true
	return true
}

// publishBadge corrects an older write if another stream changed the total
// while the sink was updating it. The OS call runs outside the state lock.
func (s *Service) publishBadge() {
	for {
		s.mu.Lock()
		n := s.badge
		s.mu.Unlock()
		s.cfg.Sink.SetBadge(n)
		s.mu.Lock()
		current := s.badge == n
		s.mu.Unlock()
		if current {
			return
		}
	}
}

// --- persistence ---

func (s *Service) markDirtyLocked() {
	s.dirty = true
	signal(s.saveWake)
}

// save writes the current preferences. Holding saveMu while taking the
// snapshot makes the last write the newest.
func (s *Service) save(force bool) error {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	s.mu.Lock()
	if !force && !s.dirty {
		s.mu.Unlock()
		return nil
	}
	p := s.prefs.clone()
	s.dirty = false
	s.mu.Unlock()
	if s.cfg.PrefsPath == "" {
		return nil
	}
	if err := SavePrefs(s.cfg.PrefsPath, p); err != nil {
		s.mu.Lock()
		s.dirty = true
		s.mu.Unlock()
		return err
	}
	return nil
}

func (s *Service) saveLogged() {
	if err := s.save(true); err != nil {
		s.logf("save preferences: %v", err)
	}
}

// saver writes dirty preferences saveDelay after the first change.
func (s *Service) saver() {
	defer s.wg.Done()
	var t *time.Timer
	var fire <-chan time.Time
	for {
		select {
		case <-s.ctx.Done():
			if t != nil {
				t.Stop()
			}
			return
		case <-s.saveWake:
			if t == nil {
				t = time.NewTimer(saveDelay)
				fire = t.C
			}
		case <-fire:
			t, fire = nil, nil
			if err := s.Flush(); err != nil {
				s.logf("save preferences: %v", err)
			}
		}
	}
}

// --- delivery ---

func (s *Service) foreground(server string) bool {
	return s.cfg.Foreground != nil && s.cfg.Foreground(server)
}

func (s *Service) delivered(server string, id int64) {
	if s.cfg.Delivered != nil {
		s.cfg.Delivered(server, id)
	}
}

// noteKey is the OS notification ID of tag on server: "<server>/<tag>".
func noteKey(server, tag string) string { return server + "/" + tag }

// titleLocked prefixes a title with the server's name once more than one
// server is followed.
func (s *Service) titleLocked(st *stream, title string) string {
	if len(s.streams) > 1 && st.t.Name != "" {
		return st.t.Name + " · " + title
	}
	return title
}

// enqueueLocked queues q for the worker; a queued note with the same ID is
// replaced in place.
func (s *Service) enqueueLocked(st *stream, q queued) {
	q.server = st.id
	q.seq = st.snapSeq
	q.delivery = st.delivery
	for i := range s.queue {
		if s.queue[i].note.ID == q.note.ID {
			s.queue[i] = q
			return
		}
	}
	s.queue = append(s.queue, q)
	signal(s.queueWake)
}

// worker shows queued notifications one at a time, so a blocking Show
// never stalls a stream.
func (s *Service) worker() {
	defer s.wg.Done()
	for {
		s.mu.Lock()
		for len(s.queue) == 0 && len(s.pendingCloses) == 0 {
			s.mu.Unlock()
			select {
			case <-s.ctx.Done():
				return
			case <-s.queueWake:
			}
			s.mu.Lock()
		}
		if len(s.pendingCloses) > 0 {
			closes := s.pendingCloses
			s.pendingCloses = map[string]struct{}{}
			s.mu.Unlock()
			for id := range closes {
				s.cfg.Sink.Close(id)
			}
			continue
		}
		q := s.queue[0]
		s.queue = s.queue[1:]
		s.busy = true
		s.mu.Unlock()

		ctx, cancel := context.WithTimeout(q.delivery, showTimeout)
		if ctx.Err() == nil {
			s.record(q, s.cfg.Sink.Show(ctx, q.note))
		}
		cancel()

		s.mu.Lock()
		s.busy = false
		s.mu.Unlock()
	}
}

// showNow shows n at a user gesture (Enable, Test).
func (s *Service) showNow(ctx context.Context, n Note) error {
	ctx, cancel := context.WithTimeout(ctx, showTimeout)
	defer cancel()
	err := s.cfg.Sink.Show(ctx, n)
	s.record(queued{note: n, url: "/settings"}, err)
	return err
}

// record applies a Show result: a denial marks the state blocked, a
// success clears it and remembers a server's notification, unless it went
// stale while showing (its group was read, its server removed, or
// notifications were turned off).
func (s *Service) record(q queued, err error) {
	id := q.note.ID
	saveNow, closeNow := false, false
	s.mu.Lock()
	switch {
	case q.delivery != nil && q.delivery.Err() != nil:
		closeNow = err == nil
	case errors.Is(err, ErrDenied):
		s.logf("show %s: %v", id, err)
		if !s.prefs.Denied {
			s.prefs.Denied = true
			saveNow = true
		}
	case err != nil:
		s.logf("show %s: %v", id, err)
	default:
		if s.prefs.Denied {
			s.prefs.Denied = false
			saveNow = true
		}
		if q.server == "" {
			break
		}
		st := s.streams[q.server]
		tag := strings.TrimPrefix(id, q.server+"/")
		stale := st == nil || !s.prefs.Enabled ||
			(isGroupTag(tag) && st.snapSeq > q.seq && !st.unread[tag])
		if stale {
			closeNow = true
			break
		}
		st.shown[id] = shownNote{id: q.id, url: q.url}
		s.prefs.server(q.server).addShown(id)
		s.markDirtyLocked()
	}
	s.mu.Unlock()
	if closeNow {
		s.cfg.Sink.Close(id)
	}
	if saveNow {
		s.saveLogged()
	}
}

// --- stream ---

// loop keeps one connection open to st's server: reconnects with
// jittered backoff (reset by a hello or a Connect), and after a 404 waits
// for the next Connect. It ends when the server is removed or the
// Service closes.
func (s *Service) loop(st *stream) {
	defer s.wg.Done()
	backoff := s.cfg.MinBackoff
	var lastGen uint64
	for {
		if st.ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		t, gen, awaiting := st.t, st.gen, st.awaitingAccess
		s.mu.Unlock()
		if gen != lastGen {
			backoff, lastGen = s.cfg.MinBackoff, gen
		}
		if awaiting || (!t.Remote() && t.Socket == "") {
			if !s.wait(st, gen, 0) {
				return
			}
			continue
		}
		ctx, cancel := context.WithCancel(st.ctx)
		s.mu.Lock()
		if st.gen != gen {
			s.mu.Unlock()
			cancel()
			continue
		}
		st.cancel = cancel
		s.mu.Unlock()

		hello, err := s.run(ctx, st, t, gen)

		s.mu.Lock()
		st.cancel = nil
		changed := st.gen != gen
		s.mu.Unlock()
		cancel()
		if st.ctx.Err() != nil {
			return
		}
		if changed {
			continue
		}
		if hello {
			backoff = s.cfg.MinBackoff
		}
		if errors.Is(err, ErrAccessEnded) {
			s.endAccess(st, gen)
			continue
		}
		if errors.Is(err, errNoStream) {
			s.mu.Lock()
			first := !st.noStreamLogged
			st.noStreamLogged = true
			s.mu.Unlock()
			if first {
				s.logf("%s: %v (server older than this app); waiting for the next server start", st.id, err)
			}
			if !s.wait(st, gen, 0) {
				return
			}
			continue
		}
		d := jitter(backoff, s.cfg.MaxBackoff)
		s.logf("%s stream: %v; reconnecting in %s", st.id, err, d.Round(time.Millisecond))
		if !s.wait(st, gen, d) {
			return
		}
		backoff = min(backoff*2, s.cfg.MaxBackoff)
	}
}

// jitter is d plus up to half of d, at most max.
func jitter(d, max time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return min(d+rand.N(d/2+1), max)
}

// wait sleeps d (forever for 0) or until a Connect changes st's
// generation; false when the stream ends.
func (s *Service) wait(st *stream, gen uint64, d time.Duration) bool {
	var fire <-chan time.Time
	if d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		fire = t.C
	}
	for {
		select {
		case <-st.ctx.Done():
			return false
		case <-fire:
			return true
		case <-st.wake:
			s.mu.Lock()
			g := st.gen
			s.mu.Unlock()
			if g != gen {
				return true
			}
		}
	}
}

// conn is the state of one stream connection.
type conn struct {
	gen   uint64
	hello bool
	// liveFrom is hello's latestId: rows up to it are catch-up.
	liveFrom int64
	// lastSeen is the newest row id handled (dedupe).
	lastSeen int64
	caughtUp bool
	catchUp  []Item
}

type wireHello struct {
	LatestID int64 `json:"latestId"`
	Reset    bool  `json:"reset"`
}

type wireNotification struct {
	ID        int64  `json:"id"`
	Event     string `json:"event"`
	CreatedAt string `json:"createdAt"`
	Read      bool   `json:"read"`
	Tag       string `json:"tag"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	URL       string `json:"url"`
}

type wireUnread struct {
	Tags  []string `json:"tags"`
	Badge int      `json:"badge"`
}

// run reads one connection until it ends; hello reports whether it got a
// hello (the server was reachable and current).
func (s *Service) run(ctx context.Context, st *stream, t target.Target, gen uint64) (hello bool, err error) {
	s.mu.Lock()
	if !st.current(gen) {
		s.mu.Unlock()
		return false, context.Canceled
	}
	session := st.session
	var cursor *int64
	var countChanged, cleared, setBadge bool
	if sp := s.prefs.Servers[st.id]; sp != nil {
		if sp.SessionScope != session.scope(t) {
			countChanged, cleared = s.clearAccessLocked(st)
			setBadge = s.badgeLocked()
		} else if sp.Cursor != nil {
			c := *sp.Cursor
			cursor = &c
		}
	}
	s.mu.Unlock()
	s.accessChanged(countChanged, cleared, setBadge)
	body, err := openStream(ctx, t, cursor, session)
	if err != nil {
		return false, err
	}
	defer body.Close()
	rd := newSSEReader(body)
	c := &conn{gen: gen}
	for {
		ev, err := rd.Next()
		if err != nil {
			return c.hello, err
		}
		switch ev.Event {
		case "access-ended":
			return c.hello, ErrAccessEnded
		case "hello":
			var h wireHello
			if err := json.Unmarshal([]byte(ev.Data), &h); err != nil {
				return c.hello, fmt.Errorf("hello: %w", err)
			}
			s.onHello(st, c, h)
		case "gap":
			s.logf("%s stream skipped older rows (more than the replay limit)", st.id)
		case "notification":
			var n wireNotification
			if err := json.Unmarshal([]byte(ev.Data), &n); err != nil {
				s.logf("notification: %v", err)
				continue
			}
			s.onNotification(st, c, Item{
				ID: n.ID, Event: n.Event, CreatedAt: parseTime(n.CreatedAt), Read: n.Read,
				Tag: n.Tag, Title: n.Title, Body: n.Body, URL: n.URL,
			})
		case "unread":
			var u wireUnread
			if err := json.Unmarshal([]byte(ev.Data), &u); err != nil {
				s.logf("unread: %v", err)
				continue
			}
			if c.hello && !c.caughtUp {
				s.finishCatchUp(st, c)
			}
			s.onUnread(st, gen, u)
		}
	}
}

// onHello: without a cursor (first run), after a database reset or when
// the server is behind the cursor, the cursor moves to latestId silently.
func (s *Service) onHello(st *stream, c *conn, h wireHello) {
	s.mu.Lock()
	if !st.current(c.gen) {
		s.mu.Unlock()
		return
	}
	sp := s.prefs.server(st.id)
	sp.SessionScope = st.session.scope(st.t)
	moved := sp.Cursor == nil || h.Reset || h.LatestID < *sp.Cursor
	if moved {
		v := h.LatestID
		sp.Cursor = &v
	}
	*c = conn{gen: c.gen, hello: true, liveFrom: h.LatestID, lastSeen: *sp.Cursor}
	s.mu.Unlock()
	if moved {
		s.saveLogged()
	}
}

func (s *Service) onNotification(st *stream, c *conn, it Item) {
	if !c.hello || it.ID <= c.lastSeen {
		return
	}
	c.lastSeen = it.ID
	if !c.caughtUp && it.ID <= c.liveFrom {
		c.catchUp = append(c.catchUp, it)
		return
	}
	fg := s.foreground(st.id)
	s.mu.Lock()
	if !st.current(c.gen) {
		s.mu.Unlock()
		return
	}
	sp := s.prefs.server(st.id)
	if sp.Cursor == nil || it.ID > *sp.Cursor {
		v := it.ID
		sp.Cursor = &v
		s.markDirtyLocked()
	}
	if it.Read {
		s.mu.Unlock()
		return
	}
	if s.prefs.Enabled && !fg {
		s.enqueueLocked(st, queued{note: Note{ID: noteKey(st.id, noteID(it)), Title: s.titleLocked(st, it.Title), Body: it.Body}, id: it.ID, url: it.URL})
	}
	s.mu.Unlock()
	s.delivered(st.id, it.ID)
}

// finishCatchUp shows the buffered replay per PlanCatchUp (plus a summary)
// and moves the cursor to the hello's latestId.
func (s *Service) finishCatchUp(st *stream, c *conn) {
	c.caughtUp = true
	items := c.catchUp
	c.catchUp = nil
	fg := s.foreground(st.id)
	show, summarized := PlanCatchUp(items, s.cfg.Now(), s.cfg.Window, s.cfg.Max)
	s.mu.Lock()
	if !st.current(c.gen) {
		s.mu.Unlock()
		return
	}
	sp := s.prefs.server(st.id)
	moved := false
	if sp.Cursor == nil || c.liveFrom > *sp.Cursor {
		v := c.liveFrom
		sp.Cursor = &v
		moved = true
	}
	if s.prefs.Enabled && !fg {
		for _, it := range show {
			s.enqueueLocked(st, queued{note: Note{ID: noteKey(st.id, noteID(it)), Title: s.titleLocked(st, it.Title), Body: it.Body}, id: it.ID, url: it.URL})
		}
		if len(summarized) > 0 {
			st.summaryTags = st.summaryTags[:0]
			for _, it := range summarized {
				st.summaryTags = append(st.summaryTags, it.Tag)
			}
			s.enqueueLocked(st, queued{
				note: Note{ID: noteKey(st.id, SummaryID), Title: s.titleLocked(st, strconv.Itoa(len(summarized))+" more in Stage"), Body: "Open notifications"},
				url:  "/?inbox",
			})
		}
	}
	s.mu.Unlock()
	if moved {
		s.saveLogged()
	}
	if len(items) > 0 {
		var newest int64
		for _, it := range items {
			newest = max(newest, it.ID)
		}
		s.delivered(st.id, newest)
	}
}

// onUnread applies a server's unread snapshot: its badge count (the
// badge is the sum over servers, set when it changed), and closes its
// notifications of groups without unread rows.
func (s *Service) onUnread(st *stream, gen uint64, u wireUnread) {
	s.mu.Lock()
	if !st.current(gen) {
		s.mu.Unlock()
		return
	}
	set := make(map[string]bool, len(u.Tags))
	for _, t := range u.Tags {
		set[t] = true
	}
	countChanged := !st.hasBadge || st.badge != u.Badge
	st.unread = set
	st.snapSeq++
	st.badge, st.hasBadge = u.Badge, true
	closes := s.reconcileLocked(st)
	setBadge := s.badgeLocked()
	s.mu.Unlock()
	for _, id := range closes {
		s.cfg.Sink.Close(id)
	}
	if setBadge {
		s.publishBadge()
	}
	if countChanged {
		s.changed()
	}
}

// reconcileLocked drops st's queued and shown group notifications not in
// its unread set, including earlier runs' IDs in its Shown (so the first
// snapshot closes those), and its summary when none of its groups is
// unread. It returns the IDs to close.
func (s *Service) reconcileLocked(st *stream) []string {
	set := st.unread
	prefix := st.id + "/"
	stale := func(id string) bool {
		tag := id
		if strings.HasPrefix(id, prefix) {
			tag = id[len(prefix):]
		} else if strings.Contains(id, "/") {
			return false // another server's
		}
		return isGroupTag(tag) && !set[tag]
	}
	var closes []string
	s.queue = slices.DeleteFunc(s.queue, func(q queued) bool { return q.server == st.id && stale(q.note.ID) })
	for id := range st.shown {
		if stale(id) {
			closes = append(closes, id)
			s.forgetLocked(st, id)
		}
	}
	summary := noteKey(st.id, SummaryID)
	if len(st.summaryTags) > 0 && !slices.ContainsFunc(st.summaryTags, func(t string) bool { return set[t] }) {
		st.summaryTags = nil
		s.queue = slices.DeleteFunc(s.queue, func(q queued) bool { return q.note.ID == summary })
		if _, ok := st.shown[summary]; ok {
			closes = append(closes, summary)
			s.forgetLocked(st, summary)
		}
	}
	// Shown also holds earlier runs' IDs (macOS can still close those);
	// this run's stale ones were dropped above.
	if sp := s.prefs.Servers[st.id]; sp != nil {
		for _, id := range slices.Clone(sp.Shown) {
			if stale(id) {
				closes = append(closes, id)
				s.forgetLocked(st, id)
			}
		}
	}
	slices.Sort(closes)
	return closes
}
