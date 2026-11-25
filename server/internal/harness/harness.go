// Package harness describes the harnesses Stage runs work on: their names,
// how they are hosted, and the policies product code reads instead of
// branching on a harness name. Each harness package declares its own
// Descriptor; the server composes a fixed Registry (docs/execution.md).
package harness

import (
	"errors"
	"fmt"
	"slices"
	"strings"

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

// LaunchIdentity is how a launch learns its session's identity.
type LaunchIdentity string

const (
	// LaunchReserved: the adapter reserves the session identity before any
	// external effect (a deterministic id, or one it checkpoints before
	// starting the work), so replaying Launch reconciles the same session.
	// The default.
	LaunchReserved LaunchIdentity = "reserved"
	// LaunchCorrelated: the harness assigns the identity when it creates the
	// session and creation is not idempotent. Stage dispatches once, embeds
	// execution.LaunchMarker(requestId) in the prompt, and after an uncertain
	// outcome finds the session by that marker (execution.Correlator) instead
	// of launching again. Not found: Launch unconfirmed until the user links.
	LaunchCorrelated LaunchIdentity = "correlated"
)

// AttentionPolicy is what Stage does with a session the harness's attention
// feed (execution.Adapter.Attention) surfaces that no task owns.
type AttentionPolicy string

const (
	// AttentionNotify: transitions into needs_input, done or failed after
	// startup record an inbox event and push, besides the silent waiting row.
	// The default.
	AttentionNotify AttentionPolicy = "notify"
	// AttentionSilent: the session shows only as a silent waiting row and in
	// the needs-input count; Stage records no event and sends no push until
	// the user adopts it.
	AttentionSilent AttentionPolicy = "silent"
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
	// Setup is the connection setup schema (connected and hosted harnesses).
	Setup Setup `json:"setup"`
	// Attribution is how an agent running on this harness tells Stage its
	// session.
	Attribution Attribution `json:"attribution"`
	// Launch is how a launch learns its session identity ("" is reserved).
	Launch LaunchIdentity `json:"launch"`
	// Attention is what an unowned session from the attention feed may
	// produce ("" is notify).
	Attention AttentionPolicy `json:"-"`
	// SessionHint says what names one of the harness's sessions when a
	// user tracks it (Track session); a host shows it as the field's hint.
	// "": the host's generic copy.
	SessionHint string `json:"sessionHint,omitempty"`
	// Worktree is what a worktree workspace on this harness may take from
	// Stage; task and schedule validation enforces it.
	Worktree Worktree `json:"worktree"`
}

// Worktree is the static policy for a worktree workspace's base. The live
// predecessorBase and worktreeBase operations may still be unavailable.
type Worktree struct {
	// BaseRef: Stage supplies the base ref (required) and branch
	// (optional). Otherwise the harness chooses both and Stage passes
	// neither.
	BaseRef bool `json:"baseRef"`
	// Predecessor: the worktree may start from a need's branch. Without
	// BaseRef the harness names the branch and its own default is the
	// fallback.
	Predecessor bool `json:"predecessor"`
}

// OwnsProcesses: the harness runs its sessions as processes Stage (or a
// worker) owns, so it alone may report an execution lost with its owner
// (execution.ReasonOwnerExited).
func (d Descriptor) OwnsProcesses() bool { return d.Hosting == Hosted }

// Attribution is how a calling agent's session reaches Stage. Every harness
// can use the generic channels: an explicit callerSession argument, the
// Stage-Caller-Session header on HTTP MCP, and STAGE_CALLER_SESSION for the
// CLI (agentctx). A harness may add its own per-call channel and its
// session variable (Env), and must describe the session its agents can name.
type Attribution struct {
	// MetaKeys are MCP request _meta keys whose string value is the
	// caller's external session id on the kind's active connection (sent by
	// the harness itself on each call).
	MetaKeys []string `json:"metaKeys"`
	// Env is the environment variable the harness sets in agent processes
	// to the session id. The CLI and stdio MCP (agent child processes) read
	// it as the weakest caller source, on the kind's active connection on
	// their machine (GET /api/connections/resolve).
	Env string `json:"env,omitempty"`
	// Precedence decides between several harness session variables set in
	// one process (a harness running inside another, or inheriting its
	// environment). Nil: none.
	Precedence *Precedence `json:"precedence,omitempty"`
	// Hint is the caller line of launch journal instructions; {connectionId}
	// and {cli} are filled in. Empty: the generic hint.
	Hint string `json:"-"`
}

// Precedence ranks a harness's session variable against the others set in
// the same process. Resolution (agenttools): a candidate whose Owns matches
// wins outright; otherwise candidates whose Yields match drop out; one left
// wins, several name no caller.
type Precedence struct {
	// Owns: when any matches, this harness's variable names the caller
	// whatever else is set.
	Owns []EnvMatch `json:"owns,omitempty"`
	// Yields: when any matches (and none of Owns), this harness's variable
	// is ignored.
	Yields []EnvMatch `json:"yields,omitempty"`
}

// EnvMatch matches an environment variable: Value "" matches any
// non-empty value, otherwise the exact (trimmed) value.
type EnvMatch struct {
	Env   string `json:"env"`
	Value string `json:"value,omitempty"`
}

// Matches reports whether m holds in getenv.
func (m EnvMatch) Matches(getenv func(string) string) bool {
	v := strings.TrimSpace(getenv(m.Env))
	if m.Value == "" {
		return v != ""
	}
	return v == m.Value
}

// AnyMatches reports whether any of ms holds in getenv.
func AnyMatches(ms []EnvMatch, getenv func(string) string) bool {
	return slices.ContainsFunc(ms, func(m EnvMatch) bool { return m.Matches(getenv) })
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
	ds = slices.Clone(ds)
	for i := range ds {
		if ds[i].Launch == "" {
			ds[i].Launch = LaunchReserved
		}
		if ds[i].Attention == "" {
			ds[i].Attention = AttentionNotify
		}
	}
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
		if d.Launch != LaunchReserved && d.Launch != LaunchCorrelated {
			return nil, fmt.Errorf("harness %s: unknown launch identity %q", d.Kind, d.Launch)
		}
		if d.Attention != AttentionNotify && d.Attention != AttentionSilent {
			return nil, fmt.Errorf("harness %s: unknown attention policy %q", d.Kind, d.Attention)
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
	return &Registry{list: ds}, nil
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
