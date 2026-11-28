package claudecode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/flexdinesh/stage/server/internal/execution"
)

const (
	hookVersionHeader = "X-Stage-Hooks-Version"
	hookVersion       = "1"
	maxDeliveryBytes  = 32 << 10
	maxPending        = 8
	finishedTurn      = "Claude Code finished its turn; send instead."
)

type pendingDelivery struct {
	mode execution.Delivery
	text string
}

// deliveryInbox is memory only, including the revision used to reject
// submissions whose roster reads overlap an unblocked Stop.
type deliveryInbox struct {
	items    []pendingDelivery
	revision uint64
	readers  int
}

// The marker records only that this UUID received the versioned settings.
// It survives a Stage restart so the same Claude session remains eligible.
func (h *Hooks) deliveryMarker(id string) string {
	return filepath.Join(filepath.Dir(h.path), "delivery-v"+hookVersion+"-"+id)
}

func (h *Hooks) markDeliveryHooks(id string) error {
	f, err := os.OpenFile(h.deliveryMarker(id), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return f.Close()
}

func (a *Adapter) deliveryAvailability() execution.Availability {
	if a.hooks == nil {
		return execution.Availability{Reason: "Stage's Claude Code hook receiver isn't running. Send once the turn finishes, or continue in Claude Code."}
	}
	return available
}

func (a *Adapter) deliverMessage(ctx context.Context, id string, m execution.Message) error {
	if !validUUID(id) {
		return execution.ErrNotFound
	}
	availability := a.deliveryAvailability()
	if !availability.Available {
		return &execution.Busy{Reason: availability.Reason}
	}
	if len(m.Text) > maxDeliveryBytes {
		return &execution.Invalid{Reason: "Claude Code steering and queued messages are limited to 32 KiB."}
	}
	if strings.TrimSpace(m.Text) == "" {
		return &execution.Invalid{Reason: "Write a message first."}
	}
	h := a.hooks
	if _, err := os.Stat(h.deliveryMarker(id)); err != nil {
		return &execution.Busy{Reason: "Launched before Stage could steer this session; send once it finishes, or answer in Claude Code (" + a.links(session{id: id}).Command + ")."}
	}
	return h.enqueue(id, pendingDelivery{mode: m.Delivery, text: m.Text}, func() error {
		es, err := a.roster.list(ctx, true)
		if err != nil {
			return err
		}
		e, listed := findSession(es, id)
		if !listed {
			h.clearDeliveries(id)
		}
		if !listed || !e.Background() || e.State != "working" || e.Status == "idle" || e.Status == "waiting" {
			return &execution.Busy{Reason: finishedTurn}
		}
		return nil
	})
}

func (h *Hooks) enqueue(id string, item pendingDelivery, checkWorking func() error) error {
	h.mu.Lock()
	inbox := h.deliveries[id]
	if inbox == nil {
		inbox = &deliveryInbox{}
		h.deliveries[id] = inbox
	}
	inbox.readers++
	revision := inbox.revision
	h.mu.Unlock()

	// The CLI can be slow. Stop and tool hooks must be able to answer
	// while we refresh its view of the session.
	err := checkWorking()
	h.mu.Lock()
	defer h.mu.Unlock()
	inbox.readers--
	if err == nil {
		switch {
		case h.deliveries[id] != inbox || inbox.revision != revision:
			err = &execution.Busy{Reason: finishedTurn}
		case len(inbox.items) >= maxPending:
			err = &execution.Busy{Reason: "Claude Code has 8 pending messages. Wait for it to take one."}
		default:
			inbox.items = append(inbox.items, item)
		}
	}
	h.dropEmptyInbox(id, inbox)
	return err
}

func (h *Hooks) dropEmptyInbox(id string, inbox *deliveryInbox) {
	if h.deliveries[id] == inbox && len(inbox.items) == 0 && inbox.readers == 0 {
		delete(h.deliveries, id)
	}
}

func (h *Hooks) clearDeliveries(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.deliveries, id)
}

type deliveryHookOutput struct {
	Decision string           `json:"decision,omitempty"`
	Reason   string           `json:"reason,omitempty"`
	Specific *deliveryContext `json:"hookSpecificOutput,omitempty"`
}

type deliveryContext struct {
	Event   string `json:"hookEventName"`
	Context string `json:"additionalContext"`
}

func isToolHook(event string) bool { return event == "PostToolUse" || event == "PostToolBatch" }

func (h *Hooks) deliveryOutput(in hookInput) (deliveryHookOutput, bool) {
	tool := isToolHook(in.Event)
	stop := in.Event == "Stop"
	if !tool && !stop && in.Event != "StopFailure" {
		return deliveryHookOutput{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	inbox := h.deliveries[in.Session]
	if inbox == nil {
		return deliveryHookOutput{}, false
	}
	if in.Event == "StopFailure" || stop && (in.StopHookActive || len(inbox.items) == 0) {
		inbox.revision++
		return deliveryHookOutput{}, false
	}
	index := 0
	if tool {
		index = slices.IndexFunc(inbox.items, func(item pendingDelivery) bool { return item.mode == execution.DeliverySteer })
		if index < 0 {
			return deliveryHookOutput{}, false
		}
	}
	item := inbox.items[index]
	inbox.items = slices.Delete(inbox.items, index, index+1)
	h.dropEmptyInbox(in.Session, inbox)
	if tool {
		return deliveryHookOutput{Specific: &deliveryContext{Event: in.Event, Context: "Message from the user (sent from Stage): " + item.text}}, true
	}
	// A Stop continuation uses Claude's reason as the next instruction.
	return deliveryHookOutput{Decision: "block", Reason: item.text}, true
}
