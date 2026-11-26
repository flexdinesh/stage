package projectmatch_test

import (
	"slices"
	"testing"

	"github.com/flexdinesh/stage/server/internal/projectmatch"
)

const repo = "github.com/acme/app"

func at(connection, machine int64, path string) projectmatch.Evidence {
	return projectmatch.Evidence{Connection: connection, Machine: machine, Path: path}
}

func withRemote(e projectmatch.Evidence, remote, repoPath string) projectmatch.Evidence {
	e.Remote, e.RepoPath = remote, repoPath
	return e
}

func withCheckout(e projectmatch.Evidence, common, sub string) projectmatch.Evidence {
	e.Common, e.Subproject = common, sub
	return e
}

func bound(id, project int64, e projectmatch.Evidence) projectmatch.Binding {
	return projectmatch.Binding{ID: id, Project: project, Evidence: e}
}

// Each rung of ADR 0007 decision 11, in order.
func TestResolveLadder(t *testing.T) {
	for _, tc := range []struct {
		name      string
		candidate projectmatch.Candidate
		bindings  []projectmatch.Binding
		want      projectmatch.Verdict
	}{
		{
			name:      "existing binding stands",
			candidate: projectmatch.Candidate{Evidence: at(1, 1, "/a"), Bound: 9},
			bindings:  []projectmatch.Binding{bound(1, 7, at(2, 1, "/a"))},
			want:      projectmatch.Verdict{Rung: projectmatch.RungBinding, Project: 9},
		},
		{
			name:      "decline sticks",
			candidate: projectmatch.Candidate{Evidence: at(1, 1, "/a"), Declined: true},
			bindings:  []projectmatch.Binding{bound(1, 7, at(2, 1, "/a"))},
			want:      projectmatch.Verdict{Rung: projectmatch.RungBinding, Leave: true},
		},
		{
			name:      "same path on the machine",
			candidate: projectmatch.Candidate{Evidence: at(1, 1, "/a")},
			bindings:  []projectmatch.Binding{bound(4, 7, at(2, 1, "/a")), bound(5, 7, at(3, 1, "/a"))},
			want:      projectmatch.Verdict{Rung: projectmatch.RungPath, Project: 7, Via: 4, Matches: []int64{7}},
		},
		{
			name:      "same path on another machine never matches",
			candidate: projectmatch.Candidate{Evidence: at(1, 1, "/a")},
			bindings:  []projectmatch.Binding{bound(4, 7, at(2, 2, "/a"))},
			want:      projectmatch.Verdict{},
		},
		{
			name:      "several owners at the path wait",
			candidate: projectmatch.Candidate{Evidence: at(1, 1, "/a")},
			bindings:  []projectmatch.Binding{bound(4, 7, at(2, 1, "/a")), bound(5, 8, at(3, 1, "/a"))},
			want:      projectmatch.Verdict{Rung: projectmatch.RungPath, Matches: []int64{7, 8}, Leave: true},
		},
		{
			name:      "owner already bound on this connection waits",
			candidate: projectmatch.Candidate{Evidence: at(1, 1, "/a")},
			bindings:  []projectmatch.Binding{bound(4, 7, at(2, 1, "/a")), bound(5, 7, at(1, 1, "/b"))},
			want:      projectmatch.Verdict{Rung: projectmatch.RungPath, Matches: []int64{7}, Leave: true},
		},
		{
			name:      "same checkout and subproject on the machine",
			candidate: projectmatch.Candidate{Evidence: withCheckout(at(1, 1, "/wt/app"), "1:2", ".")},
			bindings:  []projectmatch.Binding{bound(4, 7, withCheckout(at(2, 1, "/src/app"), "1:2", "."))},
			want:      projectmatch.Verdict{Rung: projectmatch.RungCheckout, Project: 7, Via: 4, Matches: []int64{7}},
		},
		{
			name:      "same checkout, another subproject",
			candidate: projectmatch.Candidate{Evidence: withCheckout(at(1, 1, "/wt/app/b"), "1:2", "b")},
			bindings:  []projectmatch.Binding{bound(4, 7, withCheckout(at(2, 1, "/src/app/a"), "1:2", "a"))},
			want:      projectmatch.Verdict{},
		},
		{
			name:      "checkout keys never compare across machines",
			candidate: projectmatch.Candidate{Evidence: withCheckout(at(1, 1, "/a"), "1:2", ".")},
			bindings:  []projectmatch.Binding{bound(4, 7, withCheckout(at(2, 2, "/b"), "1:2", "."))},
			want:      projectmatch.Verdict{},
		},
		{
			name:      "same remote and subproject on another machine joins",
			candidate: projectmatch.Candidate{Evidence: withRemote(at(1, 2, "/l/app"), repo, ".")},
			bindings:  []projectmatch.Binding{bound(4, 7, withRemote(at(2, 1, "/h/app"), repo, "."))},
			want:      projectmatch.Verdict{Rung: projectmatch.RungRemote, Project: 7, Via: 4, Matches: []int64{7}, Suggest: []int64{7}},
		},
		{
			name:      "ambiguous remote across machines suggests",
			candidate: projectmatch.Candidate{Evidence: withRemote(at(1, 3, "/x"), repo, ".")},
			bindings:  []projectmatch.Binding{bound(4, 7, withRemote(at(2, 1, "/a"), repo, ".")), bound(5, 8, withRemote(at(3, 2, "/b"), repo, "."))},
			want:      projectmatch.Verdict{Rung: projectmatch.RungRemote, Matches: []int64{7, 8}, Leave: true, Suggest: []int64{7, 8}},
		},
		{
			name:      "remote owner already placed on this machine suggests",
			candidate: projectmatch.Candidate{Evidence: withRemote(at(1, 2, "/l/app2"), repo, ".")},
			bindings:  []projectmatch.Binding{bound(4, 7, withRemote(at(2, 1, "/h/app"), repo, ".")), bound(5, 7, at(3, 2, "/l/app"))},
			want:      projectmatch.Verdict{Rung: projectmatch.RungRemote, Matches: []int64{7}, Leave: true, Suggest: []int64{7}},
		},
		{
			name:      "another subproject of the remote is another project",
			candidate: projectmatch.Candidate{Evidence: withRemote(at(1, 2, "/l/mono/b"), repo, "packages/b")},
			bindings:  []projectmatch.Binding{bound(4, 7, withRemote(at(2, 1, "/h/mono/a"), repo, "packages/a"))},
			want:      projectmatch.Verdict{},
		},
		{
			name:      "unknown subproject only suggests",
			candidate: projectmatch.Candidate{Evidence: withRemote(at(1, 2, "/l/app"), repo, "")},
			bindings:  []projectmatch.Binding{bound(4, 7, withRemote(at(2, 1, "/h/app"), repo, "."))},
			want:      projectmatch.Verdict{Suggest: []int64{7}},
		},
		{
			name:      "equal remote on the same machine only suggests",
			candidate: projectmatch.Candidate{Evidence: withRemote(at(1, 1, "/a"), repo, ".")},
			bindings:  []projectmatch.Binding{bound(4, 7, withRemote(at(2, 1, "/b"), repo, "."))},
			want:      projectmatch.Verdict{Suggest: []int64{7}},
		},
		{
			name:      "no remote never merges across machines",
			candidate: projectmatch.Candidate{Evidence: at(1, 2, "/l/app")},
			bindings:  []projectmatch.Binding{bound(4, 7, at(2, 1, "/h/app"))},
			want:      projectmatch.Verdict{},
		},
		{
			name:      "a workspace with no path keeps its own binding only",
			candidate: projectmatch.Candidate{Evidence: withRemote(at(1, 2, ""), repo, ".")},
			bindings:  []projectmatch.Binding{bound(4, 7, withRemote(at(2, 1, "/h/app"), repo, ".")), bound(5, 8, at(3, 2, ""))},
			want:      projectmatch.Verdict{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := projectmatch.Resolve(tc.candidate, tc.bindings)
			if got.Rung != tc.want.Rung || got.Project != tc.want.Project || got.Via != tc.want.Via || got.Leave != tc.want.Leave ||
				!slices.Equal(got.Matches, tc.want.Matches) || !slices.Equal(got.Suggest, tc.want.Suggest) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			if got.New() != (got.Project == 0 && !got.Leave) {
				t.Fatal("New")
			}
		})
	}
}
