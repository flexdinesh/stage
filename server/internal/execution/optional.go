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

// Request kinds.
const (
	RequestApproval = "approval" // pick an option
	RequestQuestion = "question" // answer every Questions entry together
	RequestInput    = "input"    // free text
)

// Request is a pending question or approval the session waits on.
type Request struct {
	ID string `json:"id"`
	// Kind: RequestApproval, RequestQuestion or RequestInput.
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	// Preview is preformatted: the command, path, URL or input the request
	// acts on (shown monospaced).
	Preview string   `json:"preview,omitempty"`
	Options []Choice `json:"options,omitempty"`
	// TextOption is the option whose answer may carry Text (a reason sent
	// with a denial); "": no option takes text.
	TextOption string `json:"textOption,omitempty"`
	// Questions of a question request, answered together (Response.Answers).
	Questions []Question `json:"questions,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
}

// Question is one question of a question request.
type Question struct {
	ID     string `json:"id"`
	Header string `json:"header,omitempty"`
	Prompt string `json:"prompt"`
	// Options are answered by label.
	Options []QuestionOption `json:"options,omitempty"`
	// Multi: several options may be picked.
	Multi bool `json:"multi,omitempty"`
	// Custom: free text is accepted in place of an option.
	Custom bool `json:"custom,omitempty"`
}

// QuestionOption is one answer a Question offers.
type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// Response answers a Request: an option id, text, or both; a question
// request answers each question by id (option labels, or one custom text).
type Response struct {
	Option  string              `json:"option,omitempty"`
	Text    string              `json:"text,omitempty"`
	Answers map[string][]string `json:"answers,omitempty"`
}

// Message is a user message sent into a session.
type Message struct {
	Text string `json:"text"`
}

// Interactive answers a session's pending requests and sends it messages.
// ErrNotFound: the request is no longer pending. *Invalid: the answer does
// not fit the request (nothing was sent).
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
