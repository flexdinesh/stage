// Package projectmatch is the project identity ladder (ADR 0007 decision
// 11, docs/architecture.md "Project import"): the one place that decides
// which Stage project a harness workspace belongs to. Projects stay a
// Stage primitive; deduplication across harnesses is best effort. Project
// import (db.Store.ImportWorkspaces) and the explicit match action (api)
// feed it the evidence they hold and act on its verdict. It reads nothing
// itself: Git evidence comes from machine.Ops on the machine where the
// files live.
package projectmatch

import "slices"

// Evidence is what is known about one workspace.
type Evidence struct {
	Connection int64
	Machine    int64
	// Path is the workspace path on Machine ("" when the harness has no
	// paths).
	Path string
	// Common identifies the Git common directory (a device:inode key) and
	// Subproject the directory under the checkout top; "" when unproven.
	// Comparable only on Machine.
	Common, Subproject string
	// Remote is the normalized origin remote and RepoPath the slash path
	// from the checkout top ("." at the top); "" when unknown.
	Remote, RepoPath string
}

// Binding is a project's active binding with its workspace's evidence.
type Binding struct {
	// ID is the binding's id (0 when the caller has none).
	ID      int64
	Project int64
	Evidence
}

// Candidate is a workspace to place.
type Candidate struct {
	Evidence
	// Bound is the project its active binding on Connection names (0:
	// none); Declined: the user unbound it, so nothing joins it again
	// automatically.
	Bound    int64
	Declined bool
}

// Rung is the step of the ladder that decided.
type Rung string

const (
	// RungBinding: an existing binding or a decline stands.
	RungBinding Rung = "binding"
	// RungPath: the same path on the same machine.
	RungPath Rung = "path"
	// RungCheckout: the same Git common directory and subproject on the
	// same machine.
	RungCheckout Rung = "checkout"
	// RungRemote: the same origin remote and subproject on another
	// machine.
	RungRemote Rung = "remote"
)

// Verdict is the ladder's decision for one candidate.
type Verdict struct {
	// Rung decided ("": no evidence names a project, so it is a new one).
	Rung Rung
	// Project is the project the workspace belongs to (0: none).
	Project int64
	// Via is a binding whose evidence proved Project (0 at RungBinding).
	Via int64
	// Matches are the projects Rung's evidence named.
	Matches []int64
	// Leave: the evidence names a project the workspace cannot join (several
	// projects, or one already bound on this connection or placed elsewhere
	// on this machine, or a decline). A user binds it by hand.
	Leave bool
	// Suggest are projects with an equal remote (and subproject, when both
	// are known) to offer for confirmation; never joined.
	Suggest []int64
}

// New: no evidence names a project, so the workspace starts its own.
func (v Verdict) New() bool { return v.Project == 0 && !v.Leave }

// Resolve walks the ladder for c against the active bindings:
//  1. c's existing binding stands, and so does the user's decline;
//  2. on c's machine, the same canonical path, or the same Git common
//     directory plus subproject, is the same project;
//  3. across machines, the same origin remote plus subproject joins only
//     when exactly one project matches and that project is not already
//     placed on c's machine; otherwise the workspace waits and the matches
//     are suggested;
//  4. otherwise it is a new project: a path matches on its own machine
//     only, no remote never merges across machines, and a workspace with
//     no path keeps its own binding only.
//
// A project gains at most one binding per connection: a match that is
// already bound on c's connection leaves c alone.
func Resolve(c Candidate, bindings []Binding) Verdict {
	if c.Bound != 0 {
		return Verdict{Rung: RungBinding, Project: c.Bound}
	}
	if c.Declined {
		return Verdict{Rung: RungBinding, Leave: true}
	}
	if c.Path == "" {
		return Verdict{}
	}
	v := Verdict{Suggest: projects(bindings, func(b Binding) bool { return sameRemote(c.Evidence, b.Evidence) })}
	byPath := func(b Binding) bool { return b.Machine == c.Machine && b.Path == c.Path }
	byCheckout := func(b Binding) bool {
		return b.Machine == c.Machine && c.Common != "" && b.Common == c.Common && b.Subproject == c.Subproject
	}
	if local := projects(bindings, func(b Binding) bool { return byPath(b) || byCheckout(b) }); len(local) > 0 {
		v.Rung, v.Matches = RungCheckout, local
		if slices.ContainsFunc(bindings, byPath) {
			v.Rung = RungPath
		}
		if len(local) == 1 && !slices.ContainsFunc(bindings, func(b Binding) bool { return b.Project == local[0] && b.Connection == c.Connection }) {
			v.Project, v.Via = local[0], via(bindings, local[0], func(b Binding) bool { return byPath(b) || byCheckout(b) })
		} else {
			v.Leave = true
		}
		return v
	}
	if c.Remote == "" || c.RepoPath == "" {
		return v
	}
	byRemote := func(b Binding) bool {
		return b.Machine != c.Machine && b.Remote == c.Remote && b.RepoPath == c.RepoPath
	}
	across := projects(bindings, byRemote)
	if len(across) == 0 {
		return v
	}
	v.Rung, v.Matches = RungRemote, across
	// A project already placed on this machine lives at another path here:
	// joining it would merge two local checkouts by remote alone.
	if len(across) == 1 && !slices.ContainsFunc(bindings, func(b Binding) bool { return b.Project == across[0] && b.Machine == c.Machine }) {
		v.Project, v.Via = across[0], via(bindings, across[0], byRemote)
	} else {
		v.Leave = true
	}
	return v
}

// sameRemote: equal remotes and, when both are known, equal subprojects.
func sameRemote(a, b Evidence) bool {
	return a.Remote != "" && a.Remote == b.Remote && (a.RepoPath == "" || b.RepoPath == "" || a.RepoPath == b.RepoPath)
}

// projects are the distinct projects of the bindings that match, sorted.
func projects(bindings []Binding, match func(Binding) bool) []int64 {
	var out []int64
	for _, b := range bindings {
		if match(b) && !slices.Contains(out, b.Project) {
			out = append(out, b.Project)
		}
	}
	slices.Sort(out)
	return out
}

func via(bindings []Binding, project int64, match func(Binding) bool) int64 {
	for _, b := range bindings {
		if b.Project == project && match(b) {
			return b.ID
		}
	}
	return 0
}
