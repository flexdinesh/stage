// Package projectmatch resolves Stage project identity from stored workspace evidence.
package projectmatch

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Workspace struct {
	Connection, Machine                                    int64
	ExternalID, Path                                       string
	FileHost                                               int64
	CanonicalPath, CheckoutKey, Subpath, Remote, InodeHint string
	Available, Legacy                                      bool
}

// A Binding is active even if its workspace is unavailable or its connection is retired.
type Binding struct {
	ID, Project int64
	Primary     bool
	Retired     bool
	Workspace
}

type Candidate struct {
	Workspace
	Bound                int64
	Declined, Importable bool
	ActiveAt             *time.Time
}

type Input struct {
	Bindings   []Binding
	Workspaces []Candidate
}

type Outcome string

const (
	OutcomeBound  Outcome = "bound"
	OutcomeJoin   Outcome = "join"
	OutcomeNew    Outcome = "new"
	OutcomeReview Outcome = "review"
)

type Reason string

const (
	ReasonAmbiguous Reason = "ambiguous"
	ReasonDeclined  Reason = "declined"
	ReasonMoved     Reason = "moved"
	ReasonSuggested Reason = "suggested"
)

type Rule string

const (
	RuleExisting Rule = "existing"
	RuleDeclined Rule = "declined"
	RuleMoved    Rule = "moved"
	RuleExternal Rule = "external"
	RuleRemote   Rule = "remote"
	RuleLocal    Rule = "local"
	RuleNone     Rule = "none"
)

type Verdict struct {
	Outcome      Outcome
	Reason       Reason
	Rule         Rule
	Project, Via int64
	Candidates   []int64
}

type Placement struct {
	Workspace
	Verdict
	Acts     bool
	NewTitle string
}

var escapeComponent = strings.NewReplacer("%", "%25", "#", "%23")

type keyKind string

const (
	keyExternal  keyKind = "external"
	keyRemote    keyKind = "remote"
	keyCheckout  keyKind = "local"
	keyDirectory keyKind = "dir"
)

type identityKey struct {
	kind    keyKind
	scope   int64
	value   string
	subpath string
}

func Key(w Workspace) string {
	return identity(w).String()
}

// GroupingKey returns only evidence strong enough to organize two distinct
// catalog rows. Legacy and native identifiers can hint at a move, but cannot
// establish equivalence.
func GroupingKey(w Workspace) string {
	if w.Legacy {
		return ""
	}
	if w.Remote != "" && w.Subpath != "" {
		return (identityKey{kind: keyRemote, value: w.Remote, subpath: w.Subpath}).String()
	}
	switch k := identity(w); k.kind {
	case keyCheckout:
		if w.Subpath != "" {
			return k.String()
		}
	case keyDirectory:
		if w.Remote == "" && w.Subpath == "" {
			return k.String()
		}
	}
	return ""
}

func ExternalKey(w Workspace) string {
	return externalIdentity(w).String()
}

func externalIdentity(w Workspace) identityKey {
	return identityKey{kind: keyExternal, scope: w.Connection, value: w.ExternalID}
}

func identity(w Workspace) identityKey {
	switch {
	case w.Path == "":
		return externalIdentity(w)
	case w.Remote != "" && w.Subpath != "":
		return identityKey{kind: keyRemote, value: w.Remote, subpath: w.Subpath}
	case w.FileHost != 0 && w.CheckoutKey != "":
		return identityKey{kind: keyCheckout, scope: w.FileHost, value: w.CheckoutKey, subpath: w.Subpath}
	case w.FileHost != 0 && w.CanonicalPath != "":
		return identityKey{kind: keyDirectory, scope: w.FileHost, value: w.CanonicalPath}
	default:
		return externalIdentity(w)
	}
}

func (k identityKey) String() string {
	prefix := string(k.kind) + ":"
	if k.kind != keyRemote {
		prefix += strconv.FormatInt(k.scope, 10) + ":"
	}
	if k.kind == keyRemote || k.kind == keyCheckout {
		return prefix + escapeComponent.Replace(k.value) + "#" + escapeComponent.Replace(k.subpath)
	}
	return prefix + k.value
}

// TitleKey identifies a workspace in Plan's titles map within one connection.
func TitleKey(externalID, path string) string {
	return strconv.Itoa(len(externalID)) + ":" + externalID + path
}

// Resolve places one candidate. Use NewResolver to resolve several candidates
// against the same input.
func Resolve(in Input, c Candidate) Verdict {
	return NewResolver(in).Resolve(c)
}

// Plan evaluates the connection's available unbound workspaces in activity
// order. Acting new placements use temporary negative project IDs, which
// subsequent joins can refer to.
func Plan(in Input, connection int64, titles map[string]string) []Placement {
	var candidates []Candidate
	for _, c := range in.Workspaces {
		if c.Connection == connection && c.Available && c.Bound == 0 {
			candidates = append(candidates, c)
		}
	}
	slices.SortFunc(candidates, compareCandidates)

	r := NewResolver(in)
	var placements []Placement
	var temporaryProject int64
	for _, c := range candidates {
		verdict, source := r.resolve(c)
		p := Placement{Workspace: c.Workspace, Verdict: verdict}
		p.Acts = p.Outcome == OutcomeJoin || p.Outcome == OutcomeNew && c.Importable
		if p.Acts {
			binding := Binding{Project: p.Project, Workspace: c.Workspace}
			if p.Outcome == OutcomeNew {
				temporaryProject--
				binding.Project, p.Project = temporaryProject, temporaryProject
				p.NewTitle = titles[TitleKey(c.ExternalID, c.Path)]
			}
			if source != nil {
				binding.Primary = source.Primary
				r.changeBinding(source, false)
			}
			r.changeDestination(c.Workspace, false)
			r.changeBinding(&binding, true)
		}
		placements = append(placements, p)
	}
	return placements
}

func compareCandidates(a, b Candidate) int {
	switch {
	case a.ActiveAt != nil && b.ActiveAt != nil:
		if order := b.ActiveAt.Compare(*a.ActiveAt); order != 0 {
			return order
		}
	case a.ActiveAt != nil:
		return -1
	case b.ActiveAt != nil:
		return 1
	}
	if order := cmp.Compare(a.ExternalID, b.ExternalID); order != 0 {
		return order
	}
	return cmp.Compare(a.Path, b.Path)
}
