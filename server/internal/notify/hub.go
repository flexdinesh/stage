package notify

import (
	"context"
	"sync"
)

// Hub wakes notification stream handlers when the inbox or the badge may
// have changed. Signals carry no data and coalesce: subscribers re-read the
// database.
type Hub struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
	done bool
}

// NewHub returns a Hub whose subscriber channels close when ctx ends, so open
// streams return and do not hold up shutdown.
func NewHub(ctx context.Context) *Hub {
	h := &Hub{subs: map[chan struct{}]struct{}{}}
	context.AfterFunc(ctx, h.close)
	return h
}

// Subscribe returns a wake channel (buffer 1) and its unsubscribe func. The
// channel is closed once the Hub's ctx ends (already closed after that).
func (h *Hub) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.done {
		close(ch)
		return ch, func() {}
	}
	h.subs[ch] = struct{}{}
	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.subs, ch)
	}
}

// Signal wakes every subscriber without blocking; a pending wake absorbs
// further signals. Safe on a nil Hub.
func (h *Hub) Signal() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (h *Hub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.done = true
	for ch := range h.subs {
		close(ch)
	}
	clear(h.subs)
}
