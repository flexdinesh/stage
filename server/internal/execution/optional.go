package execution

import (
	"context"
	"reflect"
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

// TrackedEventSource is an adapter-local event stream that can subscribe to
// sessions Stage owns. refs is the runner's current set of tracked sessions,
// excluding discovery. Canceling ctx releases the subscriptions.
type TrackedEventSource interface {
	TrackedEvents(ctx context.Context, refs []SessionRef, sink Sink) error
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
	// Preview is preformatted text the option shows (shown monospaced).
	Preview string `json:"preview,omitempty"`
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
	Text     string   `json:"text"`
	Delivery Delivery `json:"delivery,omitempty"`
	// RequestID identifies this send so a retry delivers it once, where the
	// harness deduplicates (T3 clientRequestId). The coordinator sets a fresh
	// one per API call; a caller that retries (steering delivery) passes its
	// own stable ID.
	RequestID string `json:"requestId,omitempty"`
}

// Interactive answers a session's pending requests and sends it messages.
// ErrNotFound: the request is no longer pending. *Invalid: the answer does
// not fit the request or the adapter cannot use the requested delivery.
// *Busy: the session's turn state does not fit the delivery.
type Interactive interface {
	Requests(context.Context, SessionRef) ([]Request, error)
	Respond(ctx context.Context, ref SessionRef, requestID string, r Response) error
	Send(context.Context, SessionRef, Message) error
}

// Partial forwards optional interfaces only when the peer declares their traits.
type Partial interface {
	Implements(trait string) bool
}

type optionalTrait struct {
	name string
	typ  reflect.Type
	has  func(Adapter) bool
}

func registerTrait[T any](name string) optionalTrait {
	return optionalTrait{name: name, typ: reflect.TypeFor[T](), has: func(a Adapter) bool {
		_, ok := any(a).(T)
		return ok
	}}
}

var optionalTraits = []optionalTrait{
	registerTrait[LinkResolver]("linkResolver"),
	registerTrait[SessionSearcher]("sessionSearcher"),
	registerTrait[SessionNamer]("sessionNamer"),
	registerTrait[WorkspaceFeatures]("workspaceFeatures"),
	registerTrait[Correlator]("correlator"),
	registerTrait[EventSource]("eventSource"),
	registerTrait[Interactive]("interactive"),
	registerTrait[ActivityReader]("activityReader"),
	registerTrait[Actor]("actor"),
	registerTrait[WorkspaceAdder]("workspaceAdder"),
	registerTrait[DirectoryBrowser]("directoryBrowser"),
}

func (t optionalTrait) offered(a Adapter) bool {
	p, partial := a.(Partial)
	return !partial || p.Implements(t.name)
}

// TraitOf is the channel name for the registered optional interface T.
func TraitOf[T any]() string {
	typ := reflect.TypeFor[T]()
	for _, trait := range optionalTraits {
		if trait.typ == typ {
			return trait.name
		}
	}
	return ""
}

// As reads an optional interface. A partial adapter must declare a registered
// trait even when its Go type implements the interface.
func As[T any](a any) (T, bool) {
	t, ok := a.(T)
	if p, partial := a.(Partial); ok && partial {
		name := TraitOf[T]()
		if name == "" || !p.Implements(name) {
			var zero T
			return zero, false
		}
	}
	return t, ok
}

// Traits returns the registered optional interfaces the adapter offers.
func Traits(a Adapter) []string {
	out := []string{}
	for _, trait := range optionalTraits {
		if trait.has(a) && trait.offered(a) {
			out = append(out, trait.name)
		}
	}
	return out
}
