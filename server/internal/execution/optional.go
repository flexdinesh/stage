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
