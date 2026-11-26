package projectmatch

import "slices"

type legacyPath struct {
	machine int64
	path    string
}

type moveHint struct {
	connection int64
	key        identityKey
	fileHost   int64
	inode      string
}

func moveHints(w Workspace, key identityKey) []moveHint {
	hints := []moveHint{{connection: w.Connection, key: key}}
	if w.FileHost != 0 && w.InodeHint != "" {
		hints = append(hints, moveHint{connection: w.Connection, fileHost: w.FileHost, inode: w.InodeHint})
	}
	return hints
}

type workspaceRef struct {
	externalID, path string
}

type claims struct {
	bindings map[*Binding]struct{}
	projects map[int64]int
}

type claimIndex[K comparable] map[K]*claims

func (i claimIndex[K]) change(key K, b *Binding, add bool) {
	bucket := i[key]
	if add {
		if bucket == nil {
			bucket = &claims{bindings: make(map[*Binding]struct{}), projects: make(map[int64]int)}
			i[key] = bucket
		}
		bucket.bindings[b] = struct{}{}
		bucket.projects[b.Project]++
		return
	}
	delete(bucket.bindings, b)
	bucket.projects[b.Project]--
	if bucket.projects[b.Project] == 0 {
		delete(bucket.projects, b.Project)
	}
	if len(bucket.bindings) == 0 {
		delete(i, key)
	}
}

func owners(exclude int64, buckets ...*claims) []int64 {
	var projects []int64
	for _, bucket := range buckets {
		if bucket == nil {
			continue
		}
		for project := range bucket.projects {
			if project != exclude {
				projects = append(projects, project)
			}
		}
	}
	slices.Sort(projects)
	return slices.Compact(projects)
}

// Resolver indexes active bindings and eligible move destinations for a batch
// of decisions over one inventory snapshot.
type Resolver struct {
	keys         claimIndex[identityKey]
	legacyPaths  claimIndex[legacyPath]
	sources      claimIndex[moveHint]
	destinations map[moveHint]map[workspaceRef]struct{}
}

func NewResolver(in Input) *Resolver {
	r := &Resolver{
		keys:         make(claimIndex[identityKey]),
		legacyPaths:  make(claimIndex[legacyPath]),
		sources:      make(claimIndex[moveHint]),
		destinations: make(map[moveHint]map[workspaceRef]struct{}),
	}
	for _, binding := range in.Bindings {
		r.changeBinding(&binding, true)
	}
	for _, c := range in.Workspaces {
		if c.Available && c.Bound == 0 && !c.Declined {
			r.changeDestination(c.Workspace, true)
		}
	}
	return r
}

func (r *Resolver) changeBinding(b *Binding, add bool) {
	key := identity(b.Workspace)
	r.keys.change(key, b, add)
	if b.Legacy {
		r.legacyPaths.change(legacyPath{b.Machine, b.Path}, b, add)
	}
	if !b.Available && !b.Retired {
		for _, hint := range moveHints(b.Workspace, key) {
			r.sources.change(hint, b, add)
		}
	}
}

func (r *Resolver) changeDestination(w Workspace, add bool) {
	ref := workspaceRef{w.ExternalID, w.Path}
	for _, hint := range moveHints(w, identity(w)) {
		bucket := r.destinations[hint]
		if add {
			if bucket == nil {
				bucket = make(map[workspaceRef]struct{})
				r.destinations[hint] = bucket
			}
			bucket[ref] = struct{}{}
		} else {
			delete(bucket, ref)
			if len(bucket) == 0 {
				delete(r.destinations, hint)
			}
		}
	}
}

func (r *Resolver) Resolve(c Candidate) Verdict {
	verdict, _ := r.resolve(c)
	return verdict
}

func (r *Resolver) resolve(c Candidate) (Verdict, *Binding) {
	key := identity(c.Workspace)
	exact := r.keys[key]
	if c.Bound != 0 {
		return Verdict{Outcome: OutcomeBound, Rule: RuleExisting, Project: c.Bound, Candidates: owners(c.Bound, exact)}, nil
	}
	if c.Declined {
		return Verdict{Outcome: OutcomeReview, Reason: ReasonDeclined, Rule: RuleDeclined, Candidates: owners(0, exact)}, nil
	}
	if c.Available {
		if verdict, source := r.resolveMove(c.Workspace, key); verdict.Outcome != "" {
			return verdict, source
		}
	}
	if key.kind == keyExternal {
		return Verdict{Outcome: OutcomeNew, Rule: RuleExternal}, nil
	}
	rule := RuleRemote
	var legacy *claims
	if key.kind != keyRemote {
		rule = RuleLocal
		legacy = r.legacyPaths[legacyPath{c.FileHost, c.Path}]
	}
	projects := owners(0, exact, legacy)
	switch {
	case len(projects) == 0:
		return Verdict{Outcome: OutcomeNew, Rule: RuleNone}, nil
	case len(projects) > 1:
		return Verdict{Outcome: OutcomeReview, Reason: ReasonAmbiguous, Rule: rule, Candidates: projects}, nil
	case !c.Importable:
		return Verdict{Outcome: OutcomeReview, Reason: ReasonSuggested, Rule: rule, Candidates: projects}, nil
	default:
		return Verdict{Outcome: OutcomeJoin, Rule: rule, Project: projects[0]}, nil
	}
}

func (r *Resolver) resolveMove(w Workspace, key identityKey) (Verdict, *Binding) {
	var buckets []*claims
	for _, hint := range moveHints(w, key) {
		if bucket := r.sources[hint]; bucket != nil {
			buckets = append(buckets, bucket)
		}
	}
	if len(buckets) == 0 {
		return Verdict{}, nil
	}
	if source := soleSource(buckets); source != nil && r.soleDestination(source.Workspace, w) {
		return Verdict{Outcome: OutcomeJoin, Rule: RuleMoved, Project: source.Project, Via: source.ID}, source
	}
	return Verdict{Outcome: OutcomeReview, Reason: ReasonMoved, Rule: RuleMoved, Candidates: owners(0, buckets...)}, nil
}

func soleSource(buckets []*claims) *Binding {
	var source *Binding
	for _, bucket := range buckets {
		if len(bucket.bindings) != 1 {
			return nil
		}
		for b := range bucket.bindings {
			if source != nil && source != b {
				return nil
			}
			source = b
		}
	}
	return source
}

func (r *Resolver) soleDestination(source, candidate Workspace) bool {
	ref := workspaceRef{candidate.ExternalID, candidate.Path}
	found := false
	for _, hint := range moveHints(source, identity(source)) {
		bucket := r.destinations[hint]
		if len(bucket) == 0 {
			continue
		}
		if _, same := bucket[ref]; len(bucket) != 1 || !same {
			return false
		}
		found = true
	}
	return found
}
