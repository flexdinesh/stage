package notify

import (
	"context"
	"testing"
	"time"
)

func woke(ch <-chan struct{}) (signalled, open bool) {
	select {
	case _, ok := <-ch:
		return true, ok
	case <-time.After(20 * time.Millisecond):
		return false, true
	}
}

func TestHubCoalesceAndUnsubscribe(t *testing.T) {
	h := NewHub(t.Context())
	a, unsubA := h.Subscribe()
	b, unsubB := h.Subscribe()
	defer unsubB()
	// Signals coalesce into one pending wake and never block.
	for range 5 {
		h.Signal()
	}
	for _, ch := range []<-chan struct{}{a, b} {
		if got, open := woke(ch); !got || !open {
			t.Fatalf("first wake: %v %v", got, open)
		}
		if got, _ := woke(ch); got {
			t.Fatal("signals did not coalesce")
		}
	}
	unsubA()
	h.Signal()
	if got, _ := woke(a); got {
		t.Fatal("unsubscribed channel woke")
	}
	if got, _ := woke(b); !got {
		t.Fatal("remaining subscriber missed the signal")
	}
	var nilHub *Hub
	nilHub.Signal()
}

func TestHubClosesOnContextEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := NewHub(ctx)
	ch, unsub := h.Subscribe()
	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("got a wake, want closed")
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber not closed when ctx ended")
	}
	unsub()
	h.Signal()
	late, unsubLate := h.Subscribe()
	defer unsubLate()
	if _, ok := <-late; ok {
		t.Fatal("Subscribe after ctx end must return a closed channel")
	}
}
