package projectmatch_test

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	pm "github.com/flexdinesh/stage/server/internal/projectmatch"
)

const repo = "github.com/acme/app"

func local(connection, host int64, id, path string) pm.Workspace {
	return pm.Workspace{
		Connection: connection, Machine: host, FileHost: host,
		ExternalID: id, Path: path, CanonicalPath: path, Available: true,
	}
}

func checkout(w pm.Workspace, key, subpath, remote string) pm.Workspace {
	w.CheckoutKey, w.Subpath, w.Remote = key, subpath, remote
	return w
}

func changeRemote(w pm.Workspace, remote string) pm.Workspace {
	w.Remote = remote
	return w
}

func unavailable(w pm.Workspace) pm.Workspace {
	w.Available = false
	return w
}

func candidate(w pm.Workspace) pm.Candidate {
	return pm.Candidate{Workspace: w, Importable: true}
}

func binding(id, project int64, w pm.Workspace) pm.Binding {
	return pm.Binding{ID: id, Project: project, Workspace: w}
}

func TestResolveIdentity(t *testing.T) {
	root := checkout(local(1, 1, "root", "/src/app"), "/src/app/.git", ".", repo)
	samePath := checkout(local(2, 1, "same", "/src/app"), root.CheckoutKey, ".", repo)
	worktree := checkout(local(1, 1, "worktree", "/src/wt"), root.CheckoutKey, ".", repo)
	clone := checkout(local(1, 1, "clone", "/src/clone"), "/src/clone/.git", ".", repo)
	otherHost := checkout(local(2, 2, "remote-host", "/src/app"), root.CheckoutKey, ".", repo)
	fork := changeRemote(clone, "github.com/me/app")
	pkgA := checkout(local(1, 1, "a", "/src/app/a"), root.CheckoutKey, "a", repo)
	pkgB := checkout(local(1, 1, "b", "/src/app/b"), root.CheckoutKey, "b", repo)
	moved := checkout(local(1, 1, "moved", "/src/moved"), "/src/moved/.git", ".", repo)
	pathless := pm.Workspace{Connection: 1, ExternalID: "cloud", Remote: repo, Subpath: ".", Available: true}
	pathlessOld := unavailable(pathless)
	pathlessOld.Path = "/former/path"
	pathlessOld.Remote = ""
	service := pm.Workspace{Connection: 2, Machine: 1, ExternalID: "service", Path: root.Path, Available: true}
	serviceWithRemote := checkout(service, "", ".", repo)
	plainDir := local(1, 1, "plain", "/dir")
	otherPlainDir := local(2, 1, "other-plain", plainDir.Path)
	legacy := pm.Workspace{Connection: 1, Machine: 1, ExternalID: "legacy", Path: root.Path, Legacy: true}
	inodeOld, inodeNew := unavailable(root), changeRemote(moved, "")
	inodeOld.InodeHint, inodeNew.InodeHint = "7:42", "7:42"
	inodeOtherHost := inodeNew
	inodeOtherHost.FileHost = 2

	remoteJoin := pm.Verdict{Outcome: pm.OutcomeJoin, Rule: pm.RuleRemote, Project: 7}
	localJoin := pm.Verdict{Outcome: pm.OutcomeJoin, Rule: pm.RuleLocal, Project: 7}
	movedJoin := pm.Verdict{Outcome: pm.OutcomeJoin, Rule: pm.RuleMoved, Project: 7, Via: 4}
	newProject := pm.Verdict{Outcome: pm.OutcomeNew, Rule: pm.RuleNone}
	newExternal := pm.Verdict{Outcome: pm.OutcomeNew, Rule: pm.RuleExternal}
	movedReview := pm.Verdict{Outcome: pm.OutcomeReview, Reason: pm.ReasonMoved, Rule: pm.RuleMoved, Candidates: []int64{7}}

	for _, tc := range []struct {
		name      string
		candidate pm.Candidate
		bindings  []pm.Binding
		otherRows []pm.Candidate
		want      pm.Verdict
	}{
		{
			name:      "1 same path across harnesses with remote",
			candidate: candidate(samePath),
			bindings:  []pm.Binding{binding(4, 7, root)},
			want:      remoteJoin,
		},
		{
			name:      "1 same path across harnesses without remote",
			candidate: candidate(changeRemote(samePath, "")),
			bindings:  []pm.Binding{binding(4, 7, changeRemote(root, ""))},
			want:      localJoin,
		},
		{
			name:      "1 same plain directory across harnesses",
			candidate: candidate(otherPlainDir),
			bindings:  []pm.Binding{binding(4, 7, plainDir)},
			want:      localJoin,
		},
		{
			name:      "2 root and worktree with remote on same connection",
			candidate: candidate(worktree),
			bindings:  []pm.Binding{binding(4, 7, root)},
			want:      remoteJoin,
		},
		{
			name:      "2 root and worktree without remote",
			candidate: candidate(changeRemote(worktree, "")),
			bindings:  []pm.Binding{binding(4, 7, changeRemote(root, ""))},
			want:      localJoin,
		},
		{
			name:      "3 independent clones with remote",
			candidate: candidate(clone),
			bindings:  []pm.Binding{binding(4, 7, root)},
			want:      remoteJoin,
		},
		{
			name:      "3 independent clones without remote",
			candidate: candidate(changeRemote(clone, "")),
			bindings:  []pm.Binding{binding(4, 7, changeRemote(root, ""))},
			want:      newProject,
		},
		{
			name:      "4 equal remote across machines",
			candidate: candidate(otherHost),
			bindings:  []pm.Binding{binding(4, 7, root)},
			want:      remoteJoin,
		},
		{
			name:      "5 checkout key is scoped to the file host",
			candidate: candidate(changeRemote(otherHost, "")),
			bindings:  []pm.Binding{binding(4, 7, changeRemote(root, ""))},
			want:      newProject,
		},
		{
			name:      "6 fork and upstream",
			candidate: candidate(fork),
			bindings:  []pm.Binding{binding(4, 7, root)},
			want:      newProject,
		},
		{
			name:      "7 monorepo subdirectories",
			candidate: candidate(pkgB),
			bindings:  []pm.Binding{binding(4, 7, pkgA)},
			want:      newProject,
		},
		{
			name:      "8 unique move by key",
			candidate: candidate(moved),
			bindings:  []pm.Binding{binding(4, 7, unavailable(root))},
			want:      movedJoin,
		},
		{
			name:      "8 unique move by inode ignores importability",
			candidate: pm.Candidate{Workspace: inodeNew},
			bindings:  []pm.Binding{binding(4, 7, inodeOld)},
			want:      movedJoin,
		},
		{
			name:      "8 inode scope is the file host",
			candidate: candidate(inodeOtherHost),
			bindings:  []pm.Binding{binding(4, 7, inodeOld)},
			want:      newProject,
		},
		{
			name:      "8 two source bindings for same project",
			candidate: candidate(moved),
			bindings:  []pm.Binding{binding(4, 7, unavailable(root)), binding(5, 7, unavailable(worktree))},
			want:      movedReview,
		},
		{
			name:      "8 same source and destination via both indexes",
			candidate: candidate(checkout(inodeNew, moved.CheckoutKey, ".", repo)),
			bindings:  []pm.Binding{binding(4, 7, inodeOld)},
			want:      movedJoin,
		},
		{
			name:      "8 competing destinations across key and inode indexes",
			candidate: candidate(inodeNew),
			bindings:  []pm.Binding{binding(4, 7, inodeOld)},
			otherRows: []pm.Candidate{candidate(clone)},
			want:      movedReview,
		},
		{
			name:      "8 declined, unavailable and bound alternatives do not block move",
			candidate: candidate(moved),
			bindings:  []pm.Binding{binding(4, 7, unavailable(root))},
			otherRows: []pm.Candidate{{Workspace: clone, Declined: true}, {Workspace: unavailable(worktree)}, {Workspace: checkout(local(1, 1, "bound", "/bound"), "/bound/.git", ".", repo), Bound: 9}},
			want:      movedJoin,
		},
		{
			name:      "9 bound workspace keeps project and suggests other exact owners",
			candidate: pm.Candidate{Workspace: fork, Bound: 7, Declined: true},
			bindings:  []pm.Binding{binding(4, 7, fork), binding(5, 9, changeRemote(clone, fork.Remote)), binding(6, 8, changeRemote(otherHost, fork.Remote)), binding(7, 9, changeRemote(worktree, fork.Remote))},
			want:      pm.Verdict{Outcome: pm.OutcomeBound, Rule: pm.RuleExisting, Project: 7, Candidates: []int64{8, 9}},
		},
		{
			name:      "9 old key still joins while claimed",
			candidate: candidate(clone),
			bindings:  []pm.Binding{binding(4, 7, fork), binding(5, 7, root)},
			want:      remoteJoin,
		},
		{
			name:      "9 changed key joins while claimed",
			candidate: candidate(changeRemote(otherHost, fork.Remote)),
			bindings:  []pm.Binding{binding(4, 7, fork), binding(5, 7, root)},
			want:      remoteJoin,
		},
		{
			name:      "10 pathless workspace ignores reported remote",
			candidate: candidate(pathless),
			bindings:  []pm.Binding{binding(4, 7, root)},
			want:      newExternal,
		},
		{
			name:      "10 pathless bound workspace",
			candidate: pm.Candidate{Workspace: pathless, Bound: 7},
			want:      pm.Verdict{Outcome: pm.OutcomeBound, Rule: pm.RuleExisting, Project: 7},
		},
		{
			name:      "10 pathless replacement moves by external key",
			candidate: candidate(pathless),
			bindings:  []pm.Binding{binding(4, 7, pathlessOld)},
			want:      movedJoin,
		},
		{
			name:      "11 service without file host cannot use local path",
			candidate: candidate(service),
			bindings:  []pm.Binding{binding(4, 7, plainDir), binding(5, 7, changeRemote(root, ""))},
			want:      newExternal,
		},
		{
			name:      "11 service without file host can use reported origin",
			candidate: candidate(serviceWithRemote),
			bindings:  []pm.Binding{binding(4, 7, root)},
			want:      remoteJoin,
		},
		{
			name:      "12 decline beats matching key and move",
			candidate: pm.Candidate{Workspace: moved, Declined: true},
			bindings:  []pm.Binding{binding(4, 7, unavailable(root))},
			want:      pm.Verdict{Outcome: pm.OutcomeReview, Rule: pm.RuleDeclined, Reason: pm.ReasonDeclined, Candidates: []int64{7}},
		},
		{
			name:      "13 retired connection counts for ordinary claims only",
			candidate: candidate(moved),
			bindings:  []pm.Binding{{ID: 4, Project: 7, Retired: true, Workspace: unavailable(root)}},
			want:      remoteJoin,
		},
		{
			name:      "13 legacy retired binding joins by old path",
			candidate: candidate(changeRemote(samePath, "")),
			bindings:  []pm.Binding{{ID: 4, Project: 7, Retired: true, Workspace: legacy}},
			want:      localJoin,
		},
		{
			name:      "13 legacy path cannot supply remote evidence",
			candidate: candidate(samePath),
			bindings:  []pm.Binding{binding(4, 7, legacy)},
			want:      newProject,
		},
		{
			name:      "key owners are sorted and deduplicated",
			candidate: candidate(otherHost),
			bindings:  []pm.Binding{binding(4, 10, clone), binding(5, 7, root), binding(6, 10, worktree)},
			want:      pm.Verdict{Outcome: pm.OutcomeReview, Rule: pm.RuleRemote, Reason: pm.ReasonAmbiguous, Candidates: []int64{7, 10}},
		},
		{
			name:      "ordinary match for a manual-only candidate",
			candidate: pm.Candidate{Workspace: otherHost},
			bindings:  []pm.Binding{binding(4, 7, root)},
			want:      pm.Verdict{Outcome: pm.OutcomeReview, Rule: pm.RuleRemote, Reason: pm.ReasonSuggested, Candidates: []int64{7}},
		},
		{
			name:      "manual-only candidate can still start a project by hand",
			candidate: pm.Candidate{Workspace: clone},
			want:      newProject,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := pm.Input{Bindings: tc.bindings, Workspaces: append(tc.otherRows, tc.candidate)}
			if got := pm.Resolve(in, tc.candidate); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Resolve(%+v) = %+v, want %+v", tc.candidate.Workspace, got, tc.want)
			}
		})
	}
}

func TestKey(t *testing.T) {
	w := checkout(local(7, 11, "external:42#/foo", "/a"), "/git%#x", ".", "example/Repo")
	for _, tc := range []struct {
		name string
		w    pm.Workspace
		want string
	}{
		{name: "remote takes precedence", w: w, want: "remote:example/Repo#."},
		{name: "external when pathless even with full evidence", w: pm.Workspace{Connection: 7, ExternalID: w.ExternalID, FileHost: w.FileHost, CanonicalPath: w.CanonicalPath, CheckoutKey: w.CheckoutKey, Subpath: ".", Remote: repo}, want: "external:7:external:42#/foo"},
		{name: "local checkout with root", w: changeRemote(w, ""), want: "local:11:/git%25%23x#."},
		{name: "unknown subpath distinct from root", w: checkout(w, w.CheckoutKey, "", repo), want: "local:11:/git%25%23x#"},
		{name: "directory path is final component", w: local(7, 11, "dir", "/a:#%/b"), want: "dir:11:/a:#%/b"},
		{name: "remote tuple escapes separator in remote", w: checkout(w, "", "b", "git/x#a"), want: "remote:git/x%23a#b"},
		{name: "remote tuple escapes separator in subpath", w: checkout(w, "", "a#b", "git/x"), want: "remote:git/x#a%23b"},
		{name: "remote tuple escapes literal percent sequence", w: checkout(w, "", "b", "git/x%23a"), want: "remote:git/x%2523a#b"},
		{name: "local tuple escapes separator in subpath", w: checkout(w, "/git", "x#y", ""), want: "local:11:/git#x%23y"},
		{name: "local tuple escapes separator in checkout", w: checkout(w, "/git#x", "y", ""), want: "local:11:/git%23x#y"},
		{name: "unproven host has no local key", w: checkout(pm.Workspace{Connection: 7, ExternalID: w.ExternalID, Path: "/a", CanonicalPath: "/a"}, w.CheckoutKey, ".", ""), want: "external:7:external:42#/foo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pm.Key(tc.w); got != tc.want {
				t.Errorf("Key = %q, want %q", got, tc.want)
			}
		})
	}
	if got := pm.ExternalKey(w); got != "external:7:external:42#/foo" {
		t.Errorf("ExternalKey = %q", got)
	}
	if pm.TitleKey("a:b", "c") == pm.TitleKey("a", "b:c") ||
		pm.TitleKey("a\x00b", "c") == pm.TitleKey("a", "b\x00c") {
		t.Error("TitleKey does not preserve tuple boundaries")
	}
}

func TestPlanUsesEarlierPlacements(t *testing.T) {
	now := time.Unix(20, 0)
	older := time.Unix(10, 0)
	first := checkout(local(1, 1, "z", "/first"), "/first/.git", ".", repo)
	sameKey := checkout(local(1, 1, "a", "/clone"), "/clone/.git", ".", repo)
	tieA := local(1, 1, "a", "/new-a")
	tieB := local(1, 1, "a", "/new-b")
	later := local(1, 1, "0", "/later")
	manual := local(1, 1, "manual", "/manual")
	declined := local(1, 1, "declined", "/declined")
	in := pm.Input{Workspaces: []pm.Candidate{
		{Workspace: declined, Declined: true, Importable: true},
		{Workspace: manual},
		{Workspace: tieB, ActiveAt: &older, Importable: true},
		{Workspace: later, Importable: true},
		{Workspace: tieA, ActiveAt: &older, Importable: true},
		{Workspace: first, ActiveAt: &now, Importable: true},
		{Workspace: sameKey, ActiveAt: &older, Importable: true},
		{Workspace: unavailable(first), Importable: true},
		{Workspace: local(2, 1, "other-connection", "/other")},
		{Workspace: local(1, 1, "bound", "/bound"), Bound: 70},
	}}
	titles := map[string]string{
		pm.TitleKey(first.ExternalID, first.Path):     "First",
		pm.TitleKey(sameKey.ExternalID, sameKey.Path): "Clone",
		pm.TitleKey(tieA.ExternalID, tieA.Path):       "A",
		pm.TitleKey(tieB.ExternalID, tieB.Path):       "B",
		pm.TitleKey(later.ExternalID, later.Path):     "Later",
		pm.TitleKey(manual.ExternalID, manual.Path):   "Manual",
	}
	want := []pm.Placement{
		{Workspace: first, Verdict: pm.Verdict{Outcome: pm.OutcomeNew, Rule: pm.RuleNone, Project: -1}, Acts: true, NewTitle: "First"},
		{Workspace: sameKey, Verdict: pm.Verdict{Outcome: pm.OutcomeJoin, Rule: pm.RuleRemote, Project: -1}, Acts: true},
		{Workspace: tieA, Verdict: pm.Verdict{Outcome: pm.OutcomeNew, Rule: pm.RuleNone, Project: -2}, Acts: true, NewTitle: "A"},
		{Workspace: tieB, Verdict: pm.Verdict{Outcome: pm.OutcomeNew, Rule: pm.RuleNone, Project: -3}, Acts: true, NewTitle: "B"},
		{Workspace: later, Verdict: pm.Verdict{Outcome: pm.OutcomeNew, Rule: pm.RuleNone, Project: -4}, Acts: true, NewTitle: "Later"},
		{Workspace: declined, Verdict: pm.Verdict{Outcome: pm.OutcomeReview, Rule: pm.RuleDeclined, Reason: pm.ReasonDeclined}},
		{Workspace: manual, Verdict: pm.Verdict{Outcome: pm.OutcomeNew, Rule: pm.RuleNone}},
	}
	if got := pm.Plan(in, 1, titles); !reflect.DeepEqual(got, want) {
		t.Errorf("Plan = %+v, want %+v", got, want)
	}
}

func TestPlanReplacesMoveSource(t *testing.T) {
	source := checkout(unavailable(local(1, 1, "source", "/source")), "/source/.git", ".", repo)
	destination := checkout(local(1, 1, "a", "/destination"), "/destination/.git", ".", "example.com/new")
	source.InodeHint, destination.InodeHint = "7:9", "7:9"
	oldKey := checkout(local(1, 1, "b", "/old-remote"), "/old/.git", ".", repo)
	newKey := checkout(local(1, 1, "c", "/new-remote"), "/new/.git", ".", destination.Remote)
	in := pm.Input{
		Bindings: []pm.Binding{{ID: 4, Project: 7, Primary: true, Workspace: source}},
		Workspaces: []pm.Candidate{
			{Workspace: newKey, Declined: true},
			{Workspace: oldKey, Declined: true},
			{Workspace: destination},
		},
	}
	want := []pm.Placement{
		{Workspace: destination, Verdict: pm.Verdict{Outcome: pm.OutcomeJoin, Rule: pm.RuleMoved, Project: 7, Via: 4}, Acts: true},
		{Workspace: oldKey, Verdict: pm.Verdict{Outcome: pm.OutcomeReview, Rule: pm.RuleDeclined, Reason: pm.ReasonDeclined}},
		{Workspace: newKey, Verdict: pm.Verdict{Outcome: pm.OutcomeReview, Rule: pm.RuleDeclined, Reason: pm.ReasonDeclined, Candidates: []int64{7}}},
	}
	if got := pm.Plan(in, 1, nil); !reflect.DeepEqual(got, want) {
		t.Errorf("Plan = %+v, want %+v", got, want)
	}
	if got := pm.Resolve(in, in.Workspaces[1]); !reflect.DeepEqual(got.Candidates, []int64{7}) {
		t.Errorf("Plan modified the input's source binding: %+v", got)
	}
}

func BenchmarkResolveBatch(b *testing.B) {
	const rows = 10000
	in := pm.Input{
		Bindings:   make([]pm.Binding, 0, rows),
		Workspaces: make([]pm.Candidate, 0, rows),
	}
	for i := 1; i <= rows; i++ {
		path := fmt.Sprintf("/src/%d", i)
		w := local(1, 1, path, path)
		in.Bindings = append(in.Bindings, binding(int64(i), int64(i), w))
		w.Connection = 2
		in.Workspaces = append(in.Workspaces, candidate(w))
	}
	for b.Loop() {
		r := pm.NewResolver(in)
		for _, c := range in.Workspaces {
			if got := r.Resolve(c); got.Outcome != pm.OutcomeJoin {
				b.Fatal(got)
			}
		}
	}
}
