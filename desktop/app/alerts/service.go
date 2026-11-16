// Package alerts is the desktop app's native notification consumer
// (docs/desktop.md, Notifications): it follows the server's notification
// stream over the Unix socket, decides what to show (catch-up, foreground,
// toggle, grouping), keeps the cursor and device preferences, reconciles
// shown notifications with the unread snapshot and sets the badge. It is
// pure Go, without MyGo: the OS side is a Sink.
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
	"sync"
	"time"
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
	// Foreground reports whether the main window is visible and focused
	// (then rows show as in-app toasts only). Called from the stream
	// goroutine; nil means never.
	Foreground func() bool
	// Delivered runs after new unread rows arrived (once per catch-up
	// batch, with its newest id; once per live row), shown or not. It must
	// not block. Nil means no-op.
	Delivered func(id int64)
	// Now is the clock (default time.Now).
	Now func() time.Time
	// Logf logs (default log.Printf).
	Logf func(format string, args ...any)
	// MinBackoff and MaxBackoff bound the jittered reconnect delay
	// (default 1s and 30s).
	MinBackoff, MaxBackoff time.Duration
	// Window is how old a catch-up row may be to show (default 15 min).
	Window time.Duration
	// Max is how many catch-up groups show before the summary (default 3).
	Max int
}

type shownNote struct {
	id  int64
	url string
}

// queued is a notification waiting for the delivery worker.
type queued struct {
	note Note
	id   int64
	url  string
	// seq is the unread snapshot sequence when it was queued.
	seq uint64
}

// Service consumes the notification stream. Create it with New, then
// Start; Connect on every server (re)start; Close on exit.
type Service struct {
	cfg    Config
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	saveMu sync.Mutex

	supportedOnce sync.Once
	supported     bool

	connWake  chan struct{}
	queueWake chan struct{}
	saveWake  chan struct{}

	mu             sync.Mutex
	prefs          Prefs
	dirty          bool
	started        bool
	closed         bool
	socket         string
	gen            uint64
	streamCancel   context.CancelFunc
	noStreamLogged bool
	queue          []queued
	busy           bool
	shown          map[string]shownNote
	summaryTags    []string
	unread         map[string]bool
	snapSeq        uint64
	badge          int
}

// New loads the preferences (a corrupt file is logged and replaced by
// defaults) and returns a stopped Service.
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
		cfg:       cfg,
		connWake:  make(chan struct{}, 1),
		queueWake: make(chan struct{}, 1),
		saveWake:  make(chan struct{}, 1),
		shown:     map[string]shownNote{},
		prefs:     DefaultPrefs(),
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	if cfg.PrefsPath != "" {
		p, err := LoadPrefs(cfg.PrefsPath)
		if err != nil {
			s.logf("preferences: %v; using defaults", err)
		}
		s.prefs = p
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

// Start runs the stream loop, the delivery worker and the debounced saver.
// It does nothing after the first call or after Close.
func (s *Service) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.closed {
		return
	}
	s.started = true
	s.wg.Add(3)
	go s.loop()
	go s.worker()
	go s.saver()
}

// Connect (re)connects the stream to the server at socket: the current
// stream and any backoff wait end and a new stream opens at once.
func (s *Service) Connect(socket string) {
	s.mu.Lock()
	s.socket = socket
	s.gen++
	s.noStreamLogged = false
	if s.streamCancel != nil {
		s.streamCancel()
	}
	s.mu.Unlock()
	signal(s.connWake)
}

// Flush writes pending preference changes now.
func (s *Service) Flush() error {
	s.mu.Lock()
	dirty := s.dirty
	s.mu.Unlock()
	if !dirty {
		return nil
	}
	return s.save()
}

// Close stops the stream and the worker and flushes the preferences.
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

// Disable turns notifications off and closes shown ones. The stream keeps
// running (badge, cursor).
func (s *Service) Disable() {
	s.mu.Lock()
	s.prefs.Enabled = false
	s.clearLocked()
	s.mu.Unlock()
	s.saveLogged()
	s.cfg.Sink.CloseAll()
}

// Test shows "Test notification" whether or not the window is in front; a
// click opens Settings. ErrDenied marks the state blocked.
func (s *Service) Test(ctx context.Context) error {
	return s.showNow(ctx, Note{ID: TestID, Title: "Stage", Body: "Test notification"})
}

// CloseShown closes the notification with id tag, or all of them for "".
func (s *Service) CloseShown(tag string) {
	s.mu.Lock()
	if tag == "" {
		s.clearLocked()
	} else {
		s.forgetLocked(tag)
		s.queue = slices.DeleteFunc(s.queue, func(q queued) bool { return q.note.ID == tag })
		if tag == SummaryID {
			s.summaryTags = nil
		}
	}
	s.markDirtyLocked()
	s.mu.Unlock()
	if tag == "" {
		s.cfg.Sink.CloseAll()
	} else {
		s.cfg.Sink.Close(tag)
	}
}

// Sync closes shown notifications whose groups have no unread rows in the
// latest unread snapshot.
func (s *Service) Sync() {
	s.mu.Lock()
	if s.snapSeq == 0 {
		s.mu.Unlock()
		return
	}
	closes := s.reconcileLocked()
	s.mu.Unlock()
	for _, id := range closes {
		s.cfg.Sink.Close(id)
	}
}

// Target is the app path a click on notification id opens (see Target).
func (s *Service) Target(id string) string {
	s.mu.Lock()
	known := make(map[string]string, len(s.shown))
	for k, v := range s.shown {
		known[k] = v.url
	}
	s.mu.Unlock()
	return Target(id, known)
}

// clearLocked forgets every shown and queued notification.
func (s *Service) clearLocked() {
	s.queue = nil
	s.shown = map[string]shownNote{}
	s.summaryTags = nil
	s.prefs.Shown = []string{}
	s.markDirtyLocked()
}

// forgetLocked drops id from the shown notifications.
func (s *Service) forgetLocked(id string) {
	delete(s.shown, id)
	if s.prefs.removeShown(id) {
		s.markDirtyLocked()
	}
}

// --- persistence ---

func (s *Service) markDirtyLocked() {
	s.dirty = true
	signal(s.saveWake)
}

// save writes the current preferences. Holding saveMu while taking the
// snapshot makes the last write the newest.
func (s *Service) save() error {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	s.mu.Lock()
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
	if err := s.save(); err != nil {
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

func (s *Service) foreground() bool {
	return s.cfg.Foreground != nil && s.cfg.Foreground()
}

func (s *Service) delivered(id int64) {
	if s.cfg.Delivered != nil {
		s.cfg.Delivered(id)
	}
}

// enqueueLocked queues q for the worker; a queued note with the same ID is
// replaced in place.
func (s *Service) enqueueLocked(q queued) {
	q.seq = s.snapSeq
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
// never stalls the stream.
func (s *Service) worker() {
	defer s.wg.Done()
	for {
		s.mu.Lock()
		for len(s.queue) == 0 {
			s.mu.Unlock()
			select {
			case <-s.ctx.Done():
				return
			case <-s.queueWake:
			}
			s.mu.Lock()
		}
		q := s.queue[0]
		s.queue = s.queue[1:]
		s.busy = true
		s.mu.Unlock()

		ctx, cancel := context.WithTimeout(s.ctx, showTimeout)
		err := s.cfg.Sink.Show(ctx, q.note)
		cancel()
		s.record(q, err, true)

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
	s.mu.Lock()
	seq := s.snapSeq
	s.mu.Unlock()
	s.record(queued{note: n, url: "/settings", seq: seq}, err, false)
	return err
}

// record applies a Show result: a denial marks the state blocked, a
// success clears it and remembers the notification, unless it went stale
// while showing (its group was read, or notifications were turned off).
func (s *Service) record(q queued, err error, fromQueue bool) {
	id := q.note.ID
	saveNow, closeNow := false, false
	s.mu.Lock()
	switch {
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
		stale := fromQueue && (!s.prefs.Enabled ||
			(isGroupTag(id) && s.snapSeq > q.seq && !s.unread[id]))
		if stale {
			closeNow = true
			break
		}
		s.shown[id] = shownNote{id: q.id, url: q.url}
		s.prefs.addShown(id)
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

// loop keeps one stream open to the current socket: reconnects with
// jittered backoff (reset by a hello or a Connect), and after a 404 waits
// for the next Connect.
func (s *Service) loop() {
	defer s.wg.Done()
	backoff := s.cfg.MinBackoff
	var lastGen uint64
	for {
		if s.ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		socket, gen := s.socket, s.gen
		s.mu.Unlock()
		if gen != lastGen {
			backoff, lastGen = s.cfg.MinBackoff, gen
		}
		if socket == "" {
			if !s.wait(gen, 0) {
				return
			}
			continue
		}
		ctx, cancel := context.WithCancel(s.ctx)
		s.mu.Lock()
		if s.gen != gen {
			s.mu.Unlock()
			cancel()
			continue
		}
		s.streamCancel = cancel
		s.mu.Unlock()

		hello, err := s.run(ctx, socket)

		s.mu.Lock()
		s.streamCancel = nil
		changed := s.gen != gen
		s.mu.Unlock()
		cancel()
		if s.ctx.Err() != nil {
			return
		}
		if changed {
			continue
		}
		if hello {
			backoff = s.cfg.MinBackoff
		}
		if errors.Is(err, errNoStream) {
			s.mu.Lock()
			first := !s.noStreamLogged
			s.noStreamLogged = true
			s.mu.Unlock()
			if first {
				s.logf("%v (server older than this app); waiting for the next server start", err)
			}
			if !s.wait(gen, 0) {
				return
			}
			continue
		}
		d := jitter(backoff, s.cfg.MaxBackoff)
		s.logf("stream: %v; reconnecting in %s", err, d.Round(time.Millisecond))
		if !s.wait(gen, d) {
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

// wait sleeps d (forever for 0) or until a Connect changes the
// generation; false when the Service closes.
func (s *Service) wait(gen uint64, d time.Duration) bool {
	var fire <-chan time.Time
	if d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		fire = t.C
	}
	for {
		select {
		case <-s.ctx.Done():
			return false
		case <-fire:
			return true
		case <-s.connWake:
			s.mu.Lock()
			g := s.gen
			s.mu.Unlock()
			if g != gen {
				return true
			}
		}
	}
}

// conn is the state of one stream connection.
type conn struct {
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

// run reads one stream until it ends; hello reports whether it got a
// hello (the server was reachable and current).
func (s *Service) run(ctx context.Context, socket string) (hello bool, err error) {
	s.mu.Lock()
	var cursor *int64
	if s.prefs.Cursor != nil {
		c := *s.prefs.Cursor
		cursor = &c
	}
	s.mu.Unlock()
	body, err := openStream(ctx, socket, cursor)
	if err != nil {
		return false, err
	}
	defer body.Close()
	rd := newSSEReader(body)
	st := &conn{}
	for {
		ev, err := rd.Next()
		if err != nil {
			return st.hello, err
		}
		switch ev.Event {
		case "hello":
			var h wireHello
			if err := json.Unmarshal([]byte(ev.Data), &h); err != nil {
				return st.hello, fmt.Errorf("hello: %w", err)
			}
			s.onHello(st, h)
		case "gap":
			s.logf("stream skipped older rows (more than the replay limit)")
		case "notification":
			var n wireNotification
			if err := json.Unmarshal([]byte(ev.Data), &n); err != nil {
				s.logf("notification: %v", err)
				continue
			}
			s.onNotification(st, Item{
				ID: n.ID, Event: n.Event, CreatedAt: parseTime(n.CreatedAt), Read: n.Read,
				Tag: n.Tag, Title: n.Title, Body: n.Body, URL: n.URL,
			})
		case "unread":
			var u wireUnread
			if err := json.Unmarshal([]byte(ev.Data), &u); err != nil {
				s.logf("unread: %v", err)
				continue
			}
			if st.hello && !st.caughtUp {
				s.finishCatchUp(st)
			}
			s.onUnread(u)
		}
	}
}

// onHello: without a cursor (first run), after a database reset or when
// the server is behind the cursor, the cursor moves to latestId silently.
func (s *Service) onHello(st *conn, h wireHello) {
	s.mu.Lock()
	moved := s.prefs.Cursor == nil || h.Reset || h.LatestID < *s.prefs.Cursor
	if moved {
		v := h.LatestID
		s.prefs.Cursor = &v
	}
	*st = conn{hello: true, liveFrom: h.LatestID, lastSeen: *s.prefs.Cursor}
	s.mu.Unlock()
	if moved {
		s.saveLogged()
	}
}

func (s *Service) onNotification(st *conn, it Item) {
	if !st.hello || it.ID <= st.lastSeen {
		return
	}
	st.lastSeen = it.ID
	if !st.caughtUp && it.ID <= st.liveFrom {
		st.catchUp = append(st.catchUp, it)
		return
	}
	fg := s.foreground()
	s.mu.Lock()
	if s.prefs.Cursor == nil || it.ID > *s.prefs.Cursor {
		v := it.ID
		s.prefs.Cursor = &v
		s.markDirtyLocked()
	}
	if it.Read {
		s.mu.Unlock()
		return
	}
	if s.prefs.Enabled && !fg {
		s.enqueueLocked(queued{note: Note{ID: noteID(it), Title: it.Title, Body: it.Body}, id: it.ID, url: it.URL})
	}
	s.mu.Unlock()
	s.delivered(it.ID)
}

// finishCatchUp shows the buffered replay per PlanCatchUp (plus a summary)
// and moves the cursor to the hello's latestId.
func (s *Service) finishCatchUp(st *conn) {
	st.caughtUp = true
	items := st.catchUp
	st.catchUp = nil
	fg := s.foreground()
	show, summarized := PlanCatchUp(items, s.cfg.Now(), s.cfg.Window, s.cfg.Max)
	s.mu.Lock()
	moved := false
	if s.prefs.Cursor == nil || st.liveFrom > *s.prefs.Cursor {
		v := st.liveFrom
		s.prefs.Cursor = &v
		moved = true
	}
	if s.prefs.Enabled && !fg {
		for _, it := range show {
			s.enqueueLocked(queued{note: Note{ID: noteID(it), Title: it.Title, Body: it.Body}, id: it.ID, url: it.URL})
		}
		if len(summarized) > 0 {
			s.summaryTags = s.summaryTags[:0]
			for _, it := range summarized {
				s.summaryTags = append(s.summaryTags, it.Tag)
			}
			s.enqueueLocked(queued{
				note: Note{ID: SummaryID, Title: strconv.Itoa(len(summarized)) + " more in Stage", Body: "Open notifications"},
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
		s.delivered(newest)
	}
}

// onUnread applies an unread snapshot: the badge when it changed, and
// closes notifications of groups without unread rows.
func (s *Service) onUnread(u wireUnread) {
	s.mu.Lock()
	set := make(map[string]bool, len(u.Tags))
	for _, t := range u.Tags {
		set[t] = true
	}
	badgeChanged := s.snapSeq == 0 || s.badge != u.Badge
	s.unread = set
	s.snapSeq++
	s.badge = u.Badge
	closes := s.reconcileLocked()
	s.mu.Unlock()
	if badgeChanged {
		s.cfg.Sink.SetBadge(u.Badge)
	}
	for _, id := range closes {
		s.cfg.Sink.Close(id)
	}
}

// reconcileLocked drops queued and shown group notifications not in the
// unread set, including earlier runs' IDs in Prefs.Shown (so the first
// snapshot closes those), and the summary when none of its groups is
// unread. It returns the IDs to close.
func (s *Service) reconcileLocked() []string {
	set := s.unread
	stale := func(id string) bool { return isGroupTag(id) && !set[id] }
	var closes []string
	s.queue = slices.DeleteFunc(s.queue, func(q queued) bool { return stale(q.note.ID) })
	for id := range s.shown {
		if stale(id) {
			closes = append(closes, id)
			s.forgetLocked(id)
		}
	}
	if len(s.summaryTags) > 0 && !slices.ContainsFunc(s.summaryTags, func(t string) bool { return set[t] }) {
		s.summaryTags = nil
		s.queue = slices.DeleteFunc(s.queue, func(q queued) bool { return q.note.ID == SummaryID })
		if _, ok := s.shown[SummaryID]; ok {
			closes = append(closes, SummaryID)
			s.forgetLocked(SummaryID)
		}
	}
	// Prefs.Shown also holds earlier runs' IDs (macOS can still close
	// those); this run's stale ones were dropped above.
	for _, id := range slices.Clone(s.prefs.Shown) {
		if stale(id) {
			closes = append(closes, id)
			s.forgetLocked(id)
		}
	}
	slices.Sort(closes)
	return closes
}
