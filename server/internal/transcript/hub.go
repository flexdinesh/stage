package transcript

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/t3"
)

// State is the stream `state` event.
type State struct {
	Active bool            `json:"active"`
	Status t3.ThreadStatus `json:"status"`
	Error  *string         `json:"error"`
}

// Event is one stream event: Item or State is set.
type Event struct {
	Item   *Item
	State  *State
	Cursor int // the poller's cursor after this event
}

// MirrorActive reports from local state (no T3 call) whether a session has a
// run in flight; the idle poller uses it to resume.
type MirrorActive func(ctx context.Context, threadID string) (bool, error)

// Hub runs one poller per watched session and fans its changes out to every
// subscription. Pollers call T3 only while a run is active; idle pollers
// watch the mirror, and a poller stops Grace after its last subscription.
type Hub struct {
	reader Reader
	mirror MirrorActive
	log    *slog.Logger
	ctx    context.Context

	PollEvery  time.Duration // T3 reads while active
	IdleEvery  time.Duration // mirror checks while idle
	Grace      time.Duration // keep an unwatched poller this long
	MaxBackoff time.Duration // T3 error backoff cap
	Buffer     int           // per-subscription event buffer

	mu      sync.Mutex
	pollers map[string]*poller
}

// NewHub returns a hub whose pollers stop when ctx is done.
func NewHub(ctx context.Context, client t3.Client, mirror MirrorActive, log *slog.Logger) *Hub {
	if log == nil {
		log = slog.Default()
	}
	return &Hub{
		reader: Reader{T3: client}, mirror: mirror, log: log.With("component", "transcript"), ctx: ctx,
		PollEvery: 2 * time.Second, IdleEvery: 5 * time.Second, Grace: 10 * time.Second, MaxBackoff: 30 * time.Second, Buffer: 256,
		pollers: map[string]*poller{},
	}
}

// Reader returns the hub's T3 reader (REST pages, full text).
func (h *Hub) Reader() Reader { return h.reader }

// Subscription receives a session's events. Events closes when the
// subscription is dropped (Reset reports a slow consumer or a position
// mismatch: refetch and reconnect) or the hub stops.
type Subscription struct {
	Events <-chan Event
	events chan Event
	after  int
	p      *poller

	mu    sync.Mutex
	reset bool
	done  bool
}

// Reset reports that the subscription was dropped and the client must refetch.
func (s *Subscription) Reset() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reset
}

// close ends the subscription once; true if this call closed it.
func (s *Subscription) close(reset bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return false
	}
	s.done, s.reset = true, reset
	close(s.events)
	return true
}

// send delivers without blocking; a full buffer drops the subscription.
func (s *Subscription) send(ev Event) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return false
	}
	select {
	case s.events <- ev:
		return true
	default:
		s.done, s.reset = true, true
		close(s.events)
		return false
	}
}

// Close unsubscribes.
func (s *Subscription) Close() {
	if s.close(false) {
		s.p.wake()
	}
}

// Subscribe watches a session. The subscription first receives every item
// after `after` (from the poller's cache, or one T3 read), then a state
// event, then live changes.
func (h *Hub) Subscribe(threadID string, after int) *Subscription {
	ch := make(chan Event, h.Buffer)
	sub := &Subscription{Events: ch, events: ch, after: after}
	h.mu.Lock()
	p, ok := h.pollers[threadID]
	if !ok {
		p = &poller{hub: h, threadID: threadID, cursor: after, seen: map[int]Item{}, wakeC: make(chan struct{}, 1), needRead: true}
		h.pollers[threadID] = p
		go p.run()
	}
	sub.p = p
	p.mu.Lock()
	p.pending = append(p.pending, sub)
	p.mu.Unlock()
	h.mu.Unlock()
	p.wake()
	return sub
}

// Pollers is the number of running pollers (tests).
func (h *Hub) Pollers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.pollers)
}

type poller struct {
	hub      *Hub
	threadID string
	wakeC    chan struct{}

	mu      sync.Mutex
	pending []*Subscription // awaiting backfill
	subs    []*Subscription

	// Owned by run.
	cursor   int
	seen     map[int]Item // items after cursor, by position
	state    State
	needRead bool // no fresh T3 read yet (new poller or mirror says active)
	failures int
}

func (p *poller) wake() {
	select {
	case p.wakeC <- struct{}{}:
	default:
	}
}

func (p *poller) run() {
	h := p.hub
	timer := time.NewTimer(0)
	defer timer.Stop()
	var idleSince time.Time
	for {
		select {
		case <-h.ctx.Done():
			p.stop(false)
			return
		case <-p.wakeC:
		case <-timer.C:
		}
		if !p.serveJoins() {
			return
		}
		if p.live() == 0 {
			if idleSince.IsZero() {
				idleSince = time.Now()
			}
			if time.Since(idleSince) >= h.Grace && p.retire() {
				return
			}
			resetTimer(timer, h.Grace-time.Since(idleSince))
			continue
		}
		idleSince = time.Time{}
		if !p.tick() {
			return
		}
		resetTimer(timer, p.delay())
	}
}

func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(max(d, time.Millisecond))
}

func (p *poller) delay() time.Duration {
	h := p.hub
	if p.failures > 0 {
		d := h.PollEvery << min(p.failures-1, 8)
		return min(d, h.MaxBackoff)
	}
	if p.state.Active {
		return h.PollEvery
	}
	return h.IdleEvery
}

// live counts open subscriptions, pruning closed ones.
func (p *poller) live() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	open := p.subs[:0]
	for _, s := range p.subs {
		s.mu.Lock()
		done := s.done
		s.mu.Unlock()
		if !done {
			open = append(open, s)
		}
	}
	p.subs = open
	return len(p.subs) + len(p.pending)
}

// retire removes the poller unless a subscription raced in.
func (p *poller) retire() bool {
	h := p.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	if p.live() > 0 {
		return false
	}
	delete(h.pollers, p.threadID)
	return true
}

// serveJoins backfills pending subscriptions; false if the poller stopped.
func (p *poller) serveJoins() bool {
	p.mu.Lock()
	joins := p.pending
	p.pending = nil
	p.mu.Unlock()
	if len(joins) == 0 {
		return true
	}
	from := p.cursor
	for _, s := range joins {
		from = min(from, s.after)
	}
	if p.needRead || from < p.cursor {
		alive, err := p.read(from, true)
		if !alive {
			return false
		}
		if err != nil {
			// Report the error but keep them pending: the backfill retries
			// with the next read.
			st := p.state
			for _, s := range joins {
				s.send(Event{State: &st, Cursor: p.cursor})
			}
			p.mu.Lock()
			p.pending = append(joins, p.pending...)
			p.mu.Unlock()
			return true
		}
	}
	defer p.compact()
	for _, s := range joins {
		ok := true
		for _, pos := range p.positions() {
			if pos > s.after {
				it := p.seen[pos]
				if ok = s.send(Event{Item: &it, Cursor: p.cursor}); !ok {
					break
				}
			}
		}
		st := p.state
		if ok && s.send(Event{State: &st, Cursor: p.cursor}) {
			p.mu.Lock()
			p.subs = append(p.subs, s)
			p.mu.Unlock()
		}
	}
	return true
}

func (p *poller) positions() []int {
	out := make([]int, 0, len(p.seen))
	for pos := range p.seen {
		out = append(out, pos)
	}
	slices.Sort(out)
	return out
}

// tick polls T3 while active, else checks the mirror; false if stopped.
func (p *poller) tick() bool {
	h := p.hub
	if p.state.Active || p.needRead || p.failures > 0 {
		alive, _ := p.read(p.cursor, false)
		return alive
	}
	on, err := h.mirror(h.ctx, p.threadID)
	if err != nil {
		if h.ctx.Err() == nil {
			h.log.Warn("transcript mirror check failed", "thread", p.threadID, "err", err)
		}
		return true
	}
	if on {
		alive, _ := p.read(p.cursor, false)
		return alive
	}
	return true
}

// same: no visible change between two copies of an item.
func same(a, b Item) bool {
	return a.ItemID == b.ItemID && a.Status == b.Status && a.Streaming == b.Streaming && a.Text == b.Text &&
		a.Truncated == b.Truncated && a.UpdatedAt.Equal(b.UpdatedAt)
}

// read reads T3 after `from`, broadcasts changed items after the cursor and
// state changes, and reads once more when a run just ended (final items).
// forJoins keeps items below the cursor cached for serveJoins, which
// compacts after using them. alive is false if the poller stopped
// (position mismatch); err is the T3 read error.
func (p *poller) read(from int, forJoins bool) (alive bool, err error) {
	h := p.hub
	items, _, thread, err := h.reader.After(h.ctx, p.threadID, from)
	if err != nil {
		if h.ctx.Err() != nil {
			return true, err
		}
		p.failures++
		msg := err.Error()
		if p.state.Error == nil || *p.state.Error != msg {
			p.state.Error = &msg
			p.broadcastState()
		}
		h.log.Warn("transcript read failed", "thread", p.threadID, "err", err)
		return true, err
	}
	wasActive, first := p.state.Active, p.needRead
	p.failures, p.needRead = 0, false
	for _, it := range items {
		old, known := p.seen[it.Position]
		if known && old.ItemID != it.ItemID {
			h.log.Info("transcript positions shifted; resetting streams", "thread", p.threadID, "position", it.Position)
			p.stop(true)
			return false, nil
		}
		if known && same(old, it) {
			continue
		}
		p.seen[it.Position] = it
		if it.Position > p.cursor || known {
			p.broadcast(Event{Item: &it})
		}
	}
	if !forJoins {
		p.compact()
	}
	next := State{Active: active(thread), Status: thread.Status}
	if next.Active != p.state.Active || next.Status != p.state.Status || p.state.Error != nil {
		p.state = next
		p.broadcastState()
	}
	if wasActive && !next.Active && !first {
		return p.read(p.cursor, forJoins)
	}
	return true, nil
}

// compact advances the cursor over final items and forgets them.
func (p *poller) compact() {
	pos := p.positions()
	items := make([]Item, 0, len(pos))
	for _, x := range pos {
		items = append(items, p.seen[x])
	}
	p.cursor = advance(p.cursor, items)
	for _, x := range pos {
		if x <= p.cursor {
			delete(p.seen, x)
		}
	}
}

func (p *poller) broadcast(ev Event) {
	ev.Cursor = p.cursor
	p.mu.Lock()
	subs := p.subs
	p.mu.Unlock()
	for _, s := range subs {
		s.send(ev)
	}
}

func (p *poller) broadcastState() {
	st := p.state
	p.broadcast(Event{State: &st})
}

// stop ends every subscription and removes the poller.
func (p *poller) stop(reset bool) {
	h := p.hub
	h.mu.Lock()
	if h.pollers[p.threadID] == p {
		delete(h.pollers, p.threadID)
	}
	h.mu.Unlock()
	p.mu.Lock()
	all := append(p.subs, p.pending...)
	p.subs, p.pending = nil, nil
	p.mu.Unlock()
	for _, s := range all {
		s.close(reset)
	}
}
