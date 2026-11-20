// Package harness describes the harnesses Stage runs work on: their names,
// how they are hosted, and the policies product code reads instead of
// branching on a harness name. Each harness package declares its own
// Descriptor; the server composes a fixed Registry (docs/execution.md).
package harness

import (
	"errors"
	"fmt"
	"slices"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Hosting is where a harness's sessions live.
type Hosting string

const (
	// Connected: Stage connects to a service the harness runs (an HTTP
	// server). Available wherever Stage can reach it, Docker included.
	Connected Hosting = "connected"
	// Hosted: the harness runs as processes on the machine Stage (or a
	// future worker) runs on. Needs a profile that allows local processes.
	Hosted Hosting = "hosted"
)

// Closure is a closure signal the harness reports (execution.Closure) and
// the copy that names it. Stage infers completion from a known signal only.
type Closure struct {
	Signal string `json:"signal"`
	// Provenance is the stored acceptance provenance (task_acceptances).
	Provenance string `json:"provenance"`
	// Setting labels the connection's auto-complete toggle.
	Setting string `json:"setting"`
	// Completed is the accepted result line.
	Completed string `json:"completed"`
	// Via follows "completed 3h ago": "via T3 settle".
	Via string `json:"via"`
	// Event is the journal event text of an inferred completion.
	Event string `json:"-"`
}

// Descriptor is one harness kind's static description and policies.
type Descriptor struct {
	Kind    string  `json:"kind"`
	Name    string  `json:"name"`
	Hosting Hosting `json:"hosting"`
	// StableIdentity: an instance identity names one instance for good (a
	// T3 environment id), so a retired connection with the same identity is
	// revived. Otherwise the harness proves continuity itself.
	StableIdentity bool      `json:"-"`
	Closures       []Closure `json:"closures"`
	// Setup is the connection setup schema (connected harnesses).
	Setup Setup `json:"setup"`
	// Attribution is how an agent running on this harness tells Stage its
	// session.
	Attribution Attribution `json:"attribution"`
}

// Attribution is how a calling agent's session reaches Stage. Every harness
// can use the generic channels: an explicit callerSession argument, the
// Stage-Caller-Session header on HTTP MCP, and STAGE_CALLER_SESSION for the
// CLI (agentctx). A harness may add its own per-call channel and must
// describe the session its agents can name.
type Attribution struct {
	// MetaKeys are MCP request _meta keys whose string value is the
	// caller's external session id on the kind's active connection (sent by
	// the harness itself on each call).
	MetaKeys []string `json:"metaKeys"`
	// Env is the environment variable the harness sets in agent processes
	// to the session id. A hint for agents; Stage never reads it.
	Env string `json:"env,omitempty"`
	// Hint is the caller line of launch journal instructions; {connectionId}
	// and {cli} are filled in. Empty: the generic hint.
	Hint string `json:"-"`
}

// Closure is the descriptor's closure for signal.
func (d Descriptor) Closure(signal string) (Closure, bool) {
	i := slices.IndexFunc(d.Closures, func(c Closure) bool { return c.Signal == signal })
	if i < 0 {
		return Closure{}, false
	}
	return d.Closures[i], true
}

// Closure signals with a stored provenance (task_acceptances CHECK).
var provenances = map[string]string{
	execution.ClosedSettled:  "harness_settled",
	execution.ClosedArchived: "harness_archived",
}

// Registry is the fixed set of harness kinds this server knows.
type Registry struct{ list []Descriptor }

// NewRegistry validates and orders ds (the server's composition order).
func NewRegistry(ds ...Descriptor) (*Registry, error) {
	seen := map[string]bool{}
	for _, d := range ds {
		if d.Kind == "" || d.Name == "" {
			return nil, errors.New("harness descriptor needs a kind and a name")
		}
		if seen[d.Kind] {
			return nil, fmt.Errorf("harness %s registered twice", d.Kind)
		}
		seen[d.Kind] = true
		if d.Hosting != Connected && d.Hosting != Hosted {
			return nil, fmt.Errorf("harness %s: unknown hosting %q", d.Kind, d.Hosting)
		}
		for _, c := range d.Closures {
			if p, ok := provenances[c.Signal]; !ok || p != c.Provenance {
				return nil, fmt.Errorf("harness %s: closure %q needs provenance %q", d.Kind, c.Signal, p)
			}
		}
		if err := d.Setup.validate(); err != nil {
			return nil, fmt.Errorf("harness %s: %w", d.Kind, err)
		}
	}
	return &Registry{list: slices.Clone(ds)}, nil
}

// MustRegistry is NewRegistry for fixed compositions and tests.
func MustRegistry(ds ...Descriptor) *Registry {
	r, err := NewRegistry(ds...)
	if err != nil {
		panic(err)
	}
	return r
}

// All descriptors in composition order.
func (r *Registry) All() []Descriptor { return slices.Clone(r.list) }

// Kinds in composition order.
func (r *Registry) Kinds() []string {
	out := make([]string, len(r.list))
	for i, d := range r.list {
		out[i] = d.Kind
	}
	return out
}

// Lookup finds a kind.
func (r *Registry) Lookup(kind string) (Descriptor, bool) {
	i := slices.IndexFunc(r.list, func(d Descriptor) bool { return d.Kind == kind })
	if i < 0 {
		return Descriptor{}, false
	}
	return r.list[i], true
}

// Name is a kind's display name; an unknown kind reads as itself.
func (r *Registry) Name(kind string) string {
	if d, ok := r.Lookup(kind); ok {
		return d.Name
	}
	return kind
}
