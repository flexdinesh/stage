package storetest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

func init() {
	register("ProjectGroupsUseStrongEvidence", caseProjectGroupsStrongEvidence)
	register("ProjectGroupsReuseSyncAnchor", caseProjectGroupsSyncAnchor)
	register("ProjectGroupsKeepChoicesAndHistory", caseProjectGroupsKeepChoices)
	register("ProjectGroupsSeparateAmbiguousMoves", caseProjectGroupsAmbiguousMoves)
	register("ProjectGroupsConfirmConcurrentEdits", caseProjectGroupsConcurrentEdits)
}

func refreshGroupWorkspaces(t *testing.T, s *db.Store, ctx context.Context, connection int64, rows ...execution.WorkspaceInfo) map[string]int64 {
	t.Helper()
	if _, err := s.RefreshWorkspaces(ctx, connection, rows); err != nil {
		t.Fatal(err)
	}
	workspaces, err := s.Workspaces(ctx, connection)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]int64)
	for _, w := range workspaces {
		if w.GroupProjectID == nil {
			t.Fatalf("workspace was not grouped: %+v", w)
		}
		out[w.ExternalID] = *w.GroupProjectID
	}
	return out
}

func groupConnection(t *testing.T, s *db.Store, ctx context.Context, machine int64, kind string) int64 {
	t.Helper()
	c, err := s.SwitchInstance(ctx, machine, harness.Descriptor{Kind: kind}, "groups-"+kind, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	return c.New
}

func caseProjectGroupsStrongEvidence(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	const desk = dbtest.T3Connection
	lab := groupConnection(t, s, ctx, pairMachine(t, s, ctx, "Lab").MachineID, "t3")
	local := groupConnection(t, s, ctx, db.LocalMachine, "opencode")
	root := identityWorkspace("repo", "/desk/checkout", nil)
	clone := identityWorkspace("clone", "/desk/clone", nil)
	remote := identityWorkspace("remote", "/lab/clone", nil)
	pathless := identityWorkspace("pathless", "", nil)
	fork := identityWorkspace("fork", "/lab/fork", nil)
	fork.Repo = "example.test/Fork/Repo"

	plain := execution.WorkspaceInfo{ID: "plain", Title: "App", WorkspaceRoot: "/same", Evidence: &execution.WorkspaceEvidence{CanonicalPath: "/same"}}
	weak := execution.WorkspaceInfo{ID: "weak", Title: "App", WorkspaceRoot: "/unverified"}
	gitUnknown := identityWorkspace("unknown", "/w/nested", nil)
	gitUnknown.Evidence = &execution.WorkspaceEvidence{CanonicalPath: "/w/nested", CheckoutKey: "/w/.git", Remote: root.Repo}
	gitNested := identityWorkspace("nested", "/w/nested", nil)
	gitNested.RepoPath = "packages/nested"
	localGit := execution.WorkspaceInfo{
		ID: "worktree", Title: "App", WorkspaceRoot: "/desk/worktree",
		Evidence: &execution.WorkspaceEvidence{CanonicalPath: "/desk/worktree", CheckoutKey: "/desk/git-common", Subpath: "."},
	}
	linked := localGit
	linked.WorkspaceRoot = "/desk/linked"

	deskGroups := refreshGroupWorkspaces(t, s, ctx, desk, root, clone, plain, weak, gitUnknown, gitNested, localGit)
	labGroups := refreshGroupWorkspaces(t, s, ctx, lab, remote, pathless, fork, plain, weak, gitUnknown, localGit)
	localGroups := refreshGroupWorkspaces(t, s, ctx, local, plain, weak, gitUnknown, linked)

	if deskGroups[root.ID] != deskGroups[clone.ID] || deskGroups[root.ID] != labGroups[remote.ID] || labGroups[pathless.ID] != labGroups[remote.ID] {
		t.Fatalf("known origin and subpath did not group across clones: %v %v", deskGroups, labGroups)
	}
	if labGroups[fork.ID] == labGroups[remote.ID] {
		t.Fatal("fork was grouped with its upstream")
	}
	if deskGroups[plain.ID] != localGroups[plain.ID] || deskGroups[plain.ID] == labGroups[plain.ID] {
		t.Fatalf("attested directory did not stay scoped to its host: %v %v %v", deskGroups, labGroups, localGroups)
	}
	if deskGroups[weak.ID] == localGroups[weak.ID] || deskGroups[weak.ID] == labGroups[weak.ID] {
		t.Fatal("unverified label and path were used as proof")
	}
	if deskGroups[gitUnknown.ID] == localGroups[gitUnknown.ID] || deskGroups[gitUnknown.ID] == labGroups[gitUnknown.ID] ||
		deskGroups[gitUnknown.ID] == deskGroups[gitNested.ID] || deskGroups[gitNested.ID] == deskGroups[root.ID] {
		t.Fatal("unknown or distinct Git subpaths collapsed")
	}
	if deskGroups[localGit.ID] != localGroups[linked.ID] || deskGroups[localGit.ID] == labGroups[localGit.ID] {
		t.Fatal("local checkout evidence did not distinguish a worktree from another host")
	}
}

func caseProjectGroupsSyncAnchor(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	const connection = dbtest.T3Connection
	activeAt := time.Now()
	clone := identityWorkspace("a-clone", "/repos/clone", nil)
	root := identityWorkspace("z-root", "/repos/root", &activeAt)
	n, imported, err := s.SyncWorkspaceInventory(ctx, connection, []execution.WorkspaceInfo{clone, root})
	if err != nil || n != 2 || imported.Created != 1 || imported.Bound != 1 {
		t.Fatalf("sync counted %d workspaces and imported %+v: %v", n, imported, err)
	}
	project, err := s.BoundProject(ctx, db.ProjectBinding{ConnectionID: connection, ExternalProjectID: root.ID, Workspace: root.WorkspaceRoot})
	if err != nil {
		t.Fatal(err)
	}
	groups, err := s.ListProjectGroups(ctx, "all")
	if err != nil || len(groups) != 1 || groups[0].ID != project.ID || len(groups[0].Members) != 2 {
		t.Fatalf("grouped clones should reuse the legacy origin project %d: %+v: %v", project.ID, groups, err)
	}

	// A catalog-only group established on Desk can already own the key
	// when another connection makes its first legacy import.
	private := identityWorkspace("private", "/repos/private", nil)
	private.Repo, private.Importable = "other.test/Org/Repo", false
	catalogGroup := refreshGroupWorkspaces(t, s, ctx, connection, clone, root, private)[private.ID]
	other := groupConnection(t, s, ctx, db.LocalMachine, "opencode")
	newClone := private
	newClone.ID, newClone.WorkspaceRoot, newClone.Importable = "new-clone", "/repos/new-clone", true
	if _, imported, err := s.SyncWorkspaceInventory(ctx, other, []execution.WorkspaceInfo{newClone}); err != nil || imported.Created != 1 {
		t.Fatalf("import %+v: %v", imported, err)
	}
	group, err := s.GetProjectGroup(ctx, catalogGroup)
	if err != nil || len(group.Members) != 2 {
		t.Fatalf("legacy import diverted the new clone away from its existing logical group: %+v: %v", group, err)
	}
}

func caseProjectGroupsKeepChoices(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	const connection = dbtest.T3Connection
	root := identityWorkspace("root", "/repos/app", nil)
	clone := identityWorkspace("clone", "/repos/clone", nil)
	groups := refreshGroupWorkspaces(t, s, ctx, connection, root, clone)
	original := groups[root.ID]
	selector := db.WorkspaceID(connection, clone.ID, clone.WorkspaceRoot)
	edit := db.GroupEditInput{ConnectionID: connection, WorkspaceID: selector}
	preview, err := s.EditProjectGroup(ctx, edit)
	if err != nil || preview.Source.ID != original {
		t.Fatalf("preview %+v: %v", preview, err)
	}
	edit.PreviewToken = preview.PreviewToken
	separated, err := s.EditProjectGroup(ctx, edit)
	if err != nil || !separated.Applied || separated.Target == nil || separated.Target.ID == original {
		t.Fatalf("separate %+v: %v", separated, err)
	}
	if _, err := s.EditProjectGroup(ctx, edit); !errors.As(err, new(*db.PreviewChangedError)) {
		t.Fatalf("replayed confirmation: %v", err)
	}

	newClone := identityWorkspace("new-clone", "/repos/new", nil)
	weak := execution.WorkspaceInfo{ID: "weak", Title: "App", WorkspaceRoot: "/repos/unknown"}
	before := refreshGroupWorkspaces(t, s, ctx, connection, root, clone, newClone, weak)
	if before[clone.ID] != separated.Target.ID || before[newClone.ID] == original || before[newClone.ID] == separated.Target.ID {
		t.Fatalf("explicit separation or ambiguity lost: %v", before)
	}
	weak.OriginRemote, weak.RepoPath = "https://other.test/Team/Repo.git", "."
	fresh := weak
	fresh.ID, fresh.WorkspaceRoot = "fresh", "/repos/fresh"
	after := refreshGroupWorkspaces(t, s, ctx, connection, root, clone, newClone, weak, fresh)
	if after[weak.ID] != before[weak.ID] || after[fresh.ID] != before[weak.ID] {
		t.Fatalf("fresh evidence did not help a new exact row without moving the existing row: %v -> %v", before, after)
	}
	group, err := s.GetProjectGroup(ctx, original)
	if err != nil || !slices.Contains(group.RelatedProjectIDs, separated.Target.ID) {
		t.Fatalf("duplicate evidence suggestions: %+v, %v", group, err)
	}

	edit.TargetProjectID = &original
	edit.PreviewToken = ""
	preview, err = s.EditProjectGroup(ctx, edit)
	if err != nil {
		t.Fatal(err)
	}
	edit.PreviewToken = preview.PreviewToken
	if restored, err := s.EditProjectGroup(ctx, edit); err != nil || !restored.Applied {
		t.Fatalf("restore %+v: %v", restored, err)
	}
	empty, err := s.GetProjectGroup(ctx, separated.Target.ID)
	if err != nil || empty.State != "empty" || len(empty.Members) != 0 {
		t.Fatalf("empty historic group %+v: %v", empty, err)
	}
	if err := s.RetireAndForget(ctx, connection); err != nil {
		t.Fatal(err)
	}
	historical, err := s.GetProjectGroup(ctx, original)
	if err != nil || historical.State != "retired" || len(historical.Members) != 2 {
		t.Fatalf("retired group %+v: %v", historical, err)
	}
	for _, member := range historical.Members {
		if member.CatalogState != "retired" {
			t.Fatalf("retired member %+v", member)
		}
	}
	if destinations, err := s.DestinationWorkspaces(ctx); err != nil || len(destinations) != 0 {
		t.Fatalf("forgotten rows authorize selection: %+v: %v", destinations, err)
	}
}

func caseProjectGroupsAmbiguousMoves(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	const connection = dbtest.T3Connection
	old := identityWorkspace("old", "/old", nil)
	previous := refreshGroupWorkspaces(t, s, ctx, connection, old)[old.ID]
	a := identityWorkspace("a", "/a", nil)
	b := identityWorkspace("b", "/b", nil)
	groups := refreshGroupWorkspaces(t, s, ctx, connection, a, b)
	if groups[a.ID] == previous || groups[b.ID] == previous || groups[a.ID] == groups[b.ID] {
		t.Fatalf("ambiguous replacements collapsed: old %d, new %v", previous, groups)
	}
	oldGroup, err := s.GetProjectGroup(ctx, previous)
	if err != nil || len(oldGroup.Members) != 1 || oldGroup.Members[0].CatalogState != "missing" {
		t.Fatalf("missing group member changed: %+v: %v", oldGroup, err)
	}
}

func caseProjectGroupsConcurrentEdits(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	const connection = dbtest.T3Connection
	row := identityWorkspace("racing", "/repos/racing", nil)
	group := refreshGroupWorkspaces(t, s, ctx, connection, row)[row.ID]
	target := mustProject(t, s, ctx, "Existing target")
	selector := db.WorkspaceID(connection, row.ID, row.WorkspaceRoot)
	inputs := []db.GroupEditInput{
		{ConnectionID: connection, WorkspaceID: selector, TargetProjectID: &target.ID},
		{ConnectionID: connection, WorkspaceID: selector},
	}
	for i, in := range inputs {
		preview, err := s.EditProjectGroup(ctx, in)
		if err != nil || preview.Source.ID != group {
			t.Fatalf("preview %+v: %v", preview, err)
		}
		inputs[i].PreviewToken = preview.PreviewToken
	}
	applied, stale := 0, 0
	for _, err := range race(len(inputs), func(i int) error {
		_, err := s.EditProjectGroup(ctx, inputs[i])
		return err
	}) {
		var changed *db.PreviewChangedError
		switch {
		case err == nil:
			applied++
		case errors.As(err, &changed):
			stale++
		default:
			t.Fatal(err)
		}
	}
	if applied != 1 || stale != 1 {
		t.Fatalf("concurrent edit applied %d, stale %d", applied, stale)
	}
}
