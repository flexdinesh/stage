package storetest

import (
	"context"
	"slices"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

func init() {
	register("ImportIdentityLadder", caseImportIdentityLadder)
}

// importAt refreshes connection's discovery and imports it.
func importAt(t *testing.T, s *db.Store, ctx context.Context, connection int64, ws ...execution.WorkspaceInfo) {
	t.Helper()
	if _, err := s.RefreshWorkspaces(ctx, connection, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportWorkspaces(ctx, connection); err != nil {
		t.Fatal(err)
	}
}

// Import walks the project identity ladder over every engine's bindings:
// the same path on a machine binds by path, the same remote and subproject
// on another machine joins by remote, anything else is a new project.
func caseImportIdentityLadder(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	claude := harness.Descriptor{Kind: "claude", Hosting: harness.Hosted}
	oc, err := s.EnsureOpenCode(ctx, db.LocalMachine)
	if err != nil {
		t.Fatal(err)
	}
	local, err := s.SwitchInstance(ctx, db.LocalMachine, claude, "home-a", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	laptop := pairMachine(t, s, ctx, "laptop")
	away, err := s.SwitchInstance(ctx, laptop.MachineID, claude, "home-b", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	ws := func(id, dir, repo string) execution.WorkspaceInfo {
		return execution.WorkspaceInfo{ID: id, Title: id, WorkspaceRoot: dir, Importable: true, Repo: repo, RepoPath: "."}
	}
	importAt(t, s, ctx, oc, ws("app", "/w/app", "github.com/o/app"))
	importAt(t, s, ctx, local.New, ws("/w/app", "/w/app", "github.com/o/app"))
	importAt(t, s, ctx, away.New, ws("/l/app", "/l/app", "github.com/o/app"), ws("/l/other", "/l/other", "github.com/o/other"))
	ps, err := s.ListProjects(ctx, "all")
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(ps, func(p db.Project) bool { return p.Title == "app" })
	if len(ps) != 2 || i < 0 {
		t.Fatalf("projects %+v", ps)
	}
	origins := map[int64]string{}
	for _, b := range ps[i].Bindings {
		origins[b.ConnectionID] = b.Origin
	}
	if origins[oc] != db.BindingImported || origins[local.New] != db.BindingPath || origins[away.New] != db.BindingRemote {
		t.Fatalf("bindings %+v", ps[i].Bindings)
	}
}
