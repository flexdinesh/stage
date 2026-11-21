package execution

import (
	"context"
	"time"
)

// Optional adapter interfaces and operations. Product code type-asserts an
// interface and checks the operation's availability in Features; a harness
// without one returns operation_unavailable for it and limits nothing else.

// LaunchMarker is the searchable marker a correlated launch embeds in its
// prompt (harness.LaunchCorrelated).
func LaunchMarker(requestID string) string { return "stage-launch:" + requestID }

// Correlator finds the session a correlated launch created, by
// intent.Marker, after an uncertain outcome. ErrNotFound: no session carries
// the marker. It never creates a session.
type Correlator interface {
	Correlate(context.Context, Intent) (Observation, error)
}

// Sink ingests one pushed observation through the same path as polling.
type Sink func(context.Context, Observation) error

// EventSource pushes observations of its connection's sessions as they
// change. Events blocks until ctx ends or the stream breaks (it is called
// again); polling continues as reconciliation, so a lost event only delays.
type EventSource interface {
	Events(ctx context.Context, sink Sink) error
}

// Interaction operations (Capabilities.Operations).
const (
	OpRespond = "respond" // list and answer pending requests
	OpSend    = "send"    // send a message to the session
)

// Request is a pending question or approval the session waits on.
type Request struct {
	ID string `json:"id"`
	// Kind: approval (pick an option), question (pick or answer) or input
	// (free text).
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Detail    string    `json:"detail,omitempty"`
	Options   []Choice  `json:"options,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// Response answers a Request: an option id, text, or both.
type Response struct {
	Option string `json:"option,omitempty"`
	Text   string `json:"text,omitempty"`
}

// Message is a user message sent into a session.
type Message struct {
	Text string `json:"text"`
}

// Interactive answers a session's pending requests and sends it messages.
// ErrNotFound: the request is no longer pending.
type Interactive interface {
	Requests(context.Context, SessionRef) ([]Request, error)
	Respond(ctx context.Context, ref SessionRef, requestID string, r Response) error
	Send(context.Context, SessionRef, Message) error
}

// Traits name the optional interfaces, as a remote machine reports which
// ones its adapter implements.
const (
	TraitLinkResolver      = "linkResolver"
	TraitWorkspaceFeatures = "workspaceFeatures"
	TraitCorrelator        = "correlator"
	TraitEventSource       = "eventSource"
	TraitInteractive       = "interactive"
)

// Partial is an adapter that implements every optional interface in Go but
// forwards to one that may implement only some (a remote machine's proxy).
// Implements reports whether the forwarded adapter has trait.
type Partial interface {
	Implements(trait string) bool
}

// As is a's optional interface T. Product code uses it instead of a type
// assertion, so a Partial adapter offers only what it forwards to.
func As[T any](a Adapter) (T, bool) {
	t, ok := any(a).(T)
	if !ok {
		return t, false
	}
	if p, ok := a.(Partial); ok {
		if trait := traitOf[T](); trait != "" && !p.Implements(trait) {
			var zero T
			return zero, false
		}
	}
	return t, true
}

func traitOf[T any]() string {
	switch any((*T)(nil)).(type) {
	case *LinkResolver:
		return TraitLinkResolver
	case *WorkspaceFeatures:
		return TraitWorkspaceFeatures
	case *Correlator:
		return TraitCorrelator
	case *EventSource:
		return TraitEventSource
	case *Interactive:
		return TraitInteractive
	}
	return ""
}

// Traits are the optional interfaces a implements, in a fixed order.
func Traits(a Adapter) []string {
	out := []string{}
	if _, ok := As[LinkResolver](a); ok {
		out = append(out, TraitLinkResolver)
	}
	if _, ok := As[WorkspaceFeatures](a); ok {
		out = append(out, TraitWorkspaceFeatures)
	}
	if _, ok := As[Correlator](a); ok {
		out = append(out, TraitCorrelator)
	}
	if _, ok := As[EventSource](a); ok {
		out = append(out, TraitEventSource)
	}
	if _, ok := As[Interactive](a); ok {
		out = append(out, TraitInteractive)
	}
	return out
}
