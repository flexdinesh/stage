// Package harness describes the harnesses Stage runs work on: their names,
// where their sessions live, and the policies product code reads instead of
// branching on a harness name. Each harness package declares its own
// Descriptor; the server composes a fixed Registry (docs/execution.md).
package harness

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Sessions is where a harness stores and runs its sessions.
type Sessions string

const (
	SessionsMachine Sessions = "machine"
	SessionsCloud   Sessions = "cloud"
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
	// Via names the closure signal after the completion timestamp.
	Via string `json:"via"`
	// Event is the journal event text of an inferred completion.
	Event string `json:"-"`
}

// Descriptor is one harness kind's static description and policies.
type Descriptor struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	// LocalProcesses requires a machine profile that permits local harness processes.
	LocalProcesses bool     `json:"localProcesses"`
	Sessions       Sessions `json:"sessions"`
	// StateReasons lists the nonempty observation reasons this harness may report.
	StateReasons []string `json:"stateReasons"`
	// StableIdentity means an instance identity always names the same instance.
	// A retired connection with that identity is revived. Otherwise the
	// harness proves continuity itself.
	StableIdentity bool `json:"-"`
	// ContinuityProof allows the module to select a retired connection using
	// its own evidence when an address is insufficient to identify an instance.
	ContinuityProof bool `json:"-"`
	// MovedRetire decides whether a reachable old machine's setup and the
	// new setup confirm that a stable instance moved between machines.
	MovedRetire func(old, next Summary) bool `json:"-"`
	Closures    []Closure                    `json:"closures"`
	// LinkSchemes are the URL schemes of the harness's own app links
	// (Links.Native, links in replies), lower case, without ":".
	LinkSchemes []string `json:"linkSchemes"`
	// WebAddress lets the UI offer browser address setup when a session has no link.
	WebAddress bool `json:"webAddress,omitempty"`
	// Setup is the connection setup schema.
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
	// DiscoveryHint explains how to make a workspace visible to this harness.
	DiscoveryHint string `json:"discoveryHint,omitempty"`
	// UnlistedRequests: the harness can wait on requests that Stage can't
	// list (Interactive.Requests omits them), so a needs-input session with
	// no listed requests waits on one of those, not on a reply. It is the
	// copy saying where to answer them. "": every request is listed.
	UnlistedRequests string `json:"unlistedRequests,omitempty"`
	// Wraps declares that this harness can run other harnesses' sessions.
	// Nil means it runs only its own.
	Wraps *Wraps `json:"wraps,omitempty"`
}

// Wraps is what a harness that runs other harnesses' sessions leaves in
// them, without naming them. Stage refuses to track such a session on the
// wrapped harness's connection (the wrapper tracks it), and the wrapper's
// session variable (Attribution.Env) names the caller over a wrapped
// harness's (one with Attribution.Origin) (docs/execution.md).
type Wraps struct {
	// Origins are the session origins it leaves, as the wrapped harness
	// records them and its adapter reports them in execution.Observation.Origin.
	Origins []string `json:"origins"`
}

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
	// Origin: the harness records the client that started each session, so
	// a wrapping harness (Descriptor.Wraps) may run its sessions; nil: it
	// records none and no harness wraps it.
	Origin *Origin `json:"origin,omitempty"`
	// Hint is the caller line of launch journal instructions; {connectionId}
	// and {cli} are filled in. Empty: the generic hint.
	Hint string `json:"-"`
}

// Origin is how a wrappable harness exposes a session's origin to its
// agents.
type Origin struct {
	// Env is the variable holding the session's origin in its agent
	// processes; "" when the harness sets none. Its value among another
	// harness's Wraps.Origins means the process runs inside that harness.
	Env string `json:"env,omitempty"`
}

// Precedence ranks a harness's session variable against the others set in
// the same process. Resolution (agenttools): a candidate whose Owns matches
// wins outright; otherwise candidates whose Yields match drop out, and so
// do wrapped candidates (Attribution.Origin) when a wrapper's variable is
// set or their origin variable names a wrapper (Wraps); one left wins,
// several name no caller.
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

var linkSchemePattern = regexp.MustCompile(`^[a-z][a-z0-9+.-]*$`)

var reservedLinkSchemes = []string{
	"http", "https", "ftp", "ftps", "mailto", "tel", "javascript", "data", "vbscript", "file", "blob", "about", "stage",
}

// Registry is the fixed set of harness kinds this server knows.
type Registry struct{ list []Descriptor }

// NewRegistry validates and orders ds (the server's composition order).
func NewRegistry(ds ...Descriptor) (*Registry, error) {
	seen := map[string]bool{}
	origins := map[string]string{}
	ds = slices.Clone(ds)
	for i := range ds {
		if ds[i].Launch == "" {
			ds[i].Launch = LaunchReserved
		}
		if ds[i].Attention == "" {
			ds[i].Attention = AttentionNotify
		}
		if ds[i].LinkSchemes == nil {
			ds[i].LinkSchemes = []string{}
		}
		if ds[i].StateReasons == nil {
			ds[i].StateReasons = []string{}
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
		if d.Sessions != SessionsMachine && d.Sessions != SessionsCloud {
			return nil, fmt.Errorf("harness %s: unknown session placement %q", d.Kind, d.Sessions)
		}
		seenReasons := map[string]bool{}
		for _, reason := range d.StateReasons {
			if reason == "" || strings.TrimSpace(reason) != reason || seenReasons[reason] {
				return nil, fmt.Errorf("harness %s: invalid state reason %q", d.Kind, reason)
			}
			seenReasons[reason] = true
		}
		if d.Launch != LaunchReserved && d.Launch != LaunchCorrelated {
			return nil, fmt.Errorf("harness %s: unknown launch identity %q", d.Kind, d.Launch)
		}
		if d.Attention != AttentionNotify && d.Attention != AttentionSilent {
			return nil, fmt.Errorf("harness %s: unknown attention policy %q", d.Kind, d.Attention)
		}
		seenSchemes := map[string]bool{}
		for _, s := range d.LinkSchemes {
			if !linkSchemePattern.MatchString(s) || seenSchemes[s] || slices.Contains(reservedLinkSchemes, s) || strings.HasPrefix(s, "stage-") {
				return nil, fmt.Errorf("harness %s: invalid link scheme %q", d.Kind, s)
			}
			seenSchemes[s] = true
		}
		for _, c := range d.Closures {
			if p, ok := provenances[c.Signal]; !ok || p != c.Provenance {
				return nil, fmt.Errorf("harness %s: closure %q needs provenance %q", d.Kind, c.Signal, p)
			}
		}
		if err := d.Setup.validate(); err != nil {
			return nil, fmt.Errorf("harness %s: %w", d.Kind, err)
		}
		if d.Wraps != nil {
			if len(d.Wraps.Origins) == 0 {
				return nil, fmt.Errorf("harness %s: wraps needs an origin", d.Kind)
			}
			for _, o := range d.Wraps.Origins {
				if strings.TrimSpace(o) == "" {
					return nil, fmt.Errorf("harness %s: blank wrapped origin", d.Kind)
				}
				if prev, dup := origins[o]; dup {
					return nil, fmt.Errorf("harness %s: origin %q already wrapped by %s", d.Kind, o, prev)
				}
				origins[o] = d.Kind
			}
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

// Wrapper is the harness, other than kind, whose Wraps lists origin: a
// session of kind started by it, which it tracks.
func (r *Registry) Wrapper(kind, origin string) (Descriptor, bool) {
	if origin == "" {
		return Descriptor{}, false
	}
	i := slices.IndexFunc(r.list, func(d Descriptor) bool {
		return d.Kind != kind && d.Wraps != nil && slices.Contains(d.Wraps.Origins, origin)
	})
	if i < 0 {
		return Descriptor{}, false
	}
	return r.list[i], true
}

// Refusal is the refusal for a session of kind whose origin another
// registered harness wraps; nil when none does.
func (r *Registry) Refusal(kind, origin string) *execution.Refused {
	w, ok := r.Wrapper(kind, origin)
	if !ok {
		return nil
	}
	return &execution.Refused{Reason: fmt.Sprintf("This %s session was started by %s. Track it through %s.", r.Name(kind), w.Name, w.Name)}
}

// Name is a kind's display name; an unknown kind reads as itself.
func (r *Registry) Name(kind string) string {
	if d, ok := r.Lookup(kind); ok {
		return d.Name
	}
	return kind
}
