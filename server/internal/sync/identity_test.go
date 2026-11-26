package sync_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest"
	"github.com/flexdinesh/stage/server/internal/codex"
	"github.com/flexdinesh/stage/server/internal/codex/codextest"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/sqlite"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/opencode"
	coordinator "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// The project identity ladder end to end (ADR 0007 decision 11,
// docs/architecture.md "Project import"): real adapters over their fakes,
// real Git repositories in temp dirs, each machine's ops, then discovery
// refresh and import as the sync pass runs them.

const (
	acmeApp  = "git@github.com:acme/app.git"
	acmeMono = "https://github.com/acme/mono.git"
)

// gitRepo makes dir a Git repository with origin (none when "").
func gitRepo(t *testing.T, dir, origin string) string {
	t.Helper()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git("init", "-q")
	if origin != "" {
		git("remote", "add", "origin", origin)
	}
	return dir
}

// tempRoot is a temp dir with its symlinks resolved (macOS /var).
func tempRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func mkdir(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

type identityRig struct {
	t     *testing.T
	pool  *sqlite.DB
	store *db.Store
}

func newIdentityRig(t *testing.T) *identityRig {
	pool := testDB.Reset(t)
	return &identityRig{t: t, pool: pool, store: db.NewStore(pool)}
}

// connection is machine's active connection of kind, added when missing.
func (r *identityRig) connection(kind string, machine int64) int64 {
	r.t.Helper()
	ctx := r.t.Context()
	if _, err := r.pool.ExecContext(ctx, "INSERT OR IGNORE INTO machines(id,label) VALUES($1,'m')", machine); err != nil {
		r.t.Fatal(err)
	}
	var id int64
	err := r.pool.QueryRowContext(ctx, "SELECT id FROM harness_connections WHERE harness=$1 AND machine_id=$2 AND active=1", kind, machine).Scan(&id)
	if err == nil {
		return id
	}
	if err := r.pool.QueryRowContext(ctx, "INSERT INTO harness_connections(harness,machine_id) VALUES($1,$2) RETURNING id", kind, machine).Scan(&id); err != nil {
		r.t.Fatal(err)
	}
	return id
}

// sync runs connection id's discovery refresh and import, as the sync pass
// does.
func (r *identityRig) sync(svc *coordinator.Service, id int64) {
	r.t.Helper()
	ctx := r.t.Context()
	ws, err := svc.Inventory(ctx, id)
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err := r.store.RefreshWorkspaces(ctx, id, ws); err != nil {
		r.t.Fatal(err)
	}
	if _, err := r.store.ImportWorkspaces(ctx, id); err != nil {
		r.t.Fatal(err)
	}
}

type discovered struct {
	repo, repoPath string
	importable     bool
}

// discovered is connection id's stored discovery of workspace.
func (r *identityRig) discovered(id int64, workspace string) discovered {
	r.t.Helper()
	var d discovered
	if err := r.pool.QueryRowContext(r.t.Context(), "SELECT repo,repo_path,importable FROM external_projects WHERE connection_id=$1 AND workspace=$2", id, workspace).Scan(&d.repo, &d.repoPath, &d.importable); err != nil {
		r.t.Fatalf("discovered %d %s: %v", id, workspace, err)
	}
	return d
}

func (r *identityRig) projects() []db.Project {
	r.t.Helper()
	ps, err := r.store.ListProjects(r.t.Context(), "all")
	if err != nil {
		r.t.Fatal(err)
	}
	return ps
}

// owner is the project bound to workspace on connection id (nil: none).
func (r *identityRig) owner(id int64, workspace string) (*db.Project, db.ProjectBinding) {
	r.t.Helper()
	for _, p := range r.projects() {
		for _, b := range p.Bindings {
			if b.ConnectionID == id && b.Workspace == workspace {
				return &p, b
			}
		}
	}
	return nil, db.ProjectBinding{}
}

func t3Adapter(projects ...execution.WorkspaceInfo) execution.Adapter {
	f := t3.NewFake()
	for _, p := range projects {
		added := f.AddProject(p.Title, p.WorkspaceRoot)
		_ = f.SetProject(added.ID, func(q *t3.Project) { q.OriginRemote, q.Repo, q.RepoPath = p.OriginRemote, p.Repo, p.RepoPath })
	}
	return t3.NewAdapter(f)
}

type ocProject struct{ id, canonical, vcs string }

func openCodeAdapter(t *testing.T, connection int64, projects ...ocProject) execution.Adapter {
	t.Helper()
	rows := []map[string]any{}
	for _, p := range projects {
		rows = append(rows, map[string]any{"id": p.id, "canonical": p.canonical, "vcs": p.vcs, "time": map[string]int64{"created": 1, "updated": 2, "active": 3}})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/project" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(rows)
	}))
	t.Cleanup(srv.Close)
	a, err := opencode.New(connection, opencode.Settings{Endpoint: srv.URL, Local: true})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func claudeAdapter(t *testing.T, connection int64, dirs ...string) execution.Adapter {
	t.Helper()
	f := claudetest.New(t)
	f.WriteClaudeJSON(dirs...)
	return f.Adapter(connection)
}

func codexAdapter(t *testing.T, connection int64, dirs ...string) execution.Adapter {
	t.Helper()
	srv := codextest.New(t)
	for _, d := range dirs {
		srv.Trust(d, "trusted")
	}
	client := codex.NewClient(&codextest.Daemon{Srv: srv, T: t}, "test")
	t.Cleanup(client.Close)
	return codex.New(client, connection, "")
}

// One repository on one machine, known to all four harnesses: the T3
// project, the OpenCode checkout, the Claude folder and the Codex trusted
// project are one Stage project, bound by repository and subpath.
func TestIdentityOneRepoAcrossFourHarnesses(t *testing.T) {
	r := newIdentityRig(t)
	repo := gitRepo(t, filepath.Join(tempRoot(t), "app"), acmeApp)
	t3c, occ, ccc, cxc := r.connection(t3.Kind, 1), r.connection(opencode.Kind, 1), r.connection(claudecode.Kind, 1), r.connection(codex.Kind, 1)
	svc := coordinator.NewWithAdapters(r.store, map[int64]execution.Adapter{
		t3c: t3Adapter(execution.WorkspaceInfo{Title: "app", WorkspaceRoot: repo, OriginRemote: acmeApp, Repo: "github.com/acme/app", RepoPath: "."}),
		occ: openCodeAdapter(t, occ, ocProject{"oc-app", repo, "git"}),
		ccc: claudeAdapter(t, ccc, repo),
		cxc: codexAdapter(t, cxc, repo),
	}, nil)
	for _, id := range []int64{t3c, occ, ccc, cxc} {
		r.sync(svc, id)
	}
	ps := r.projects()
	if len(ps) != 1 || ps[0].Title != "app" || len(ps[0].Bindings) != 4 {
		t.Fatalf("projects %+v", ps)
	}
	for _, b := range ps[0].Bindings {
		want := "remote"
		if b.ConnectionID == t3c {
			want = "new"
		}
		if b.Workspace != repo || b.Rule != want {
			t.Fatalf("binding %+v", b)
		}
	}
	for _, id := range []int64{t3c, occ, ccc, cxc} {
		if d := r.discovered(id, repo); d != (discovered{"github.com/acme/app", ".", true}) {
			t.Fatalf("connection %d discovered %+v", id, d)
		}
	}
}

// Plain directories (no Git) stay manual-bind only and carry no remote.
func TestIdentityPlainDirectoryStaysManual(t *testing.T) {
	r := newIdentityRig(t)
	plain := mkdir(t, filepath.Join(tempRoot(t), "notes"))
	occ, ccc, cxc := r.connection(opencode.Kind, 1), r.connection(claudecode.Kind, 1), r.connection(codex.Kind, 1)
	svc := coordinator.NewWithAdapters(r.store, map[int64]execution.Adapter{
		occ: openCodeAdapter(t, occ, ocProject{"oc-notes", plain, ""}),
		ccc: claudeAdapter(t, ccc, plain),
		cxc: codexAdapter(t, cxc, plain),
	}, nil)
	for _, id := range []int64{occ, ccc, cxc} {
		r.sync(svc, id)
		if d := r.discovered(id, plain); d != (discovered{}) {
			t.Fatalf("connection %d discovered %+v", id, d)
		}
	}
	if ps := r.projects(); len(ps) != 0 {
		t.Fatalf("projects %+v", ps)
	}
}

// Two machines, two clones with the same origin remote at different paths:
// one project. A clone without a remote never merges across machines.
func TestIdentityTwoMachinesSameRemote(t *testing.T) {
	r := newIdentityRig(t)
	root := tempRoot(t)
	one := gitRepo(t, filepath.Join(root, "m1", "app"), acmeApp)
	two := gitRepo(t, filepath.Join(root, "m2", "src", "app"), "https://github.com/acme/app")
	bare := gitRepo(t, filepath.Join(root, "m2", "scratch"), "")
	c1, c2 := r.connection(claudecode.Kind, 1), r.connection(claudecode.Kind, 2)
	svc := coordinator.NewWithAdapters(r.store, map[int64]execution.Adapter{
		c1: claudeAdapter(t, c1, one),
		c2: claudeAdapter(t, c2, two, bare),
	}, nil)
	r.sync(svc, c1)
	r.sync(svc, c2)
	p1, _ := r.owner(c1, one)
	p2, b2 := r.owner(c2, two)
	if p1 == nil || p2 == nil || p1.ID != p2.ID || b2.Rule != "remote" {
		t.Fatalf("same remote: %+v %+v", p1, p2)
	}
	p3, b3 := r.owner(c2, bare)
	if p3 == nil || p3.ID == p1.ID || b3.Rule != "new" {
		t.Fatalf("no remote: %+v", p3)
	}
	if d := r.discovered(c2, two); d != (discovered{"github.com/acme/app", ".", true}) {
		t.Fatalf("discovered %+v", d)
	}
}

// Two projects on two machines already hold the remote: a third machine's
// clone joins neither and waits for a manual bind.
func TestIdentityAmbiguousRemoteWaits(t *testing.T) {
	r := newIdentityRig(t)
	root := tempRoot(t)
	dirs := []string{
		gitRepo(t, filepath.Join(root, "m1", "app"), acmeApp),
		gitRepo(t, filepath.Join(root, "m2", "app"), acmeApp),
		gitRepo(t, filepath.Join(root, "m3", "app"), acmeApp),
	}
	conns := []int64{r.connection(claudecode.Kind, 1), r.connection(claudecode.Kind, 2), r.connection(claudecode.Kind, 3)}
	adapters := map[int64]execution.Adapter{}
	for i, c := range conns {
		adapters[c] = claudeAdapter(t, c, dirs[i])
	}
	svc := coordinator.NewWithAdapters(r.store, adapters, nil)
	ctx := t.Context()
	for i, title := range []string{"App one", "App two"} {
		if _, err := r.store.InsertProject(ctx, db.Project{Title: title, Bindings: []db.ProjectBinding{{ConnectionID: conns[i], ExternalProjectID: dirs[i], Workspace: dirs[i]}}}); err != nil {
			t.Fatal(err)
		}
		r.sync(svc, conns[i])
	}
	r.sync(svc, conns[2])
	if p, _ := r.owner(conns[2], dirs[2]); p != nil || len(r.projects()) != 2 {
		t.Fatalf("ambiguous remote bound %+v", r.projects())
	}
}

// A monorepo: Codex trusts a subproject on one machine; T3 on another
// reports the same remote with two subprojects. The same subproject joins,
// a sibling is its own project, and the checkout's top (a Claude folder)
// is another project.
func TestIdentityMonorepoSubprojects(t *testing.T) {
	r := newIdentityRig(t)
	mono := gitRepo(t, filepath.Join(tempRoot(t), "mono"), acmeMono)
	a := mkdir(t, filepath.Join(mono, "packages", "a"))
	cx, cc, t3c := r.connection(codex.Kind, 1), r.connection(claudecode.Kind, 1), r.connection(t3.Kind, 2)
	remoteA, remoteB := "/m2/mono/packages/a", "/m2/mono/packages/b"
	svc := coordinator.NewWithAdapters(r.store, map[int64]execution.Adapter{
		cx: codexAdapter(t, cx, a),
		cc: claudeAdapter(t, cc, mono, a),
		t3c: t3Adapter(
			execution.WorkspaceInfo{Title: "a", WorkspaceRoot: remoteA, OriginRemote: acmeMono, Repo: "github.com/acme/mono", RepoPath: "packages/a"},
			execution.WorkspaceInfo{Title: "b", WorkspaceRoot: remoteB, OriginRemote: acmeMono, Repo: "github.com/acme/mono", RepoPath: "packages/b"},
		),
	}, nil)
	for _, id := range []int64{cx, cc, t3c} {
		r.sync(svc, id)
	}
	if d := r.discovered(cx, a); d != (discovered{"github.com/acme/mono", "packages/a", true}) {
		t.Fatalf("codex subproject %+v", d)
	}
	pa, _ := r.owner(cx, a)
	pra, bra := r.owner(t3c, remoteA)
	prb, _ := r.owner(t3c, remoteB)
	ptop, _ := r.owner(cc, mono)
	if pa == nil || pra == nil || pra.ID != pa.ID || bra.Rule != "remote" {
		t.Fatalf("same subproject: %+v %+v", pa, pra)
	}
	if prb == nil || prb.ID == pa.ID || ptop == nil || ptop.ID == pa.ID || ptop.ID == prb.ID {
		t.Fatalf("sibling or top merged: %+v %+v %+v", pa, prb, ptop)
	}
	// Claude imports a checkout's top only; its subfolder stays manual.
	if p, _ := r.owner(cc, a); p != nil && p.ID != pa.ID {
		t.Fatalf("claude subfolder %+v", p)
	}
	if d := r.discovered(cc, a); d.importable {
		t.Fatalf("claude subfolder importable %+v", d)
	}
}

// A Claude folder on a worker: the worker's machine reads its Git evidence,
// in process and behind a real worker channel.
func TestIdentityInventoryWhereTheFilesLive(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		r := newIdentityRig(t)
		repo := gitRepo(t, filepath.Join(tempRoot(t), "app"), acmeApp)
		plain := mkdir(t, filepath.Join(tempRoot(t), "notes"))
		c := r.connection(claudecode.Kind, p.machine(t, r.pool))
		svc := p.coordinator(t, r.store, map[int64]execution.Adapter{c: claudeAdapter(t, c, repo, plain)}, nil)
		ws, err := svc.Inventory(t.Context(), c)
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(ws, func(w execution.WorkspaceInfo) bool { return w.WorkspaceRoot == repo })
		j := slices.IndexFunc(ws, func(w execution.WorkspaceInfo) bool { return w.WorkspaceRoot == plain })
		if i < 0 || j < 0 || !ws[i].Importable || ws[i].Evidence == nil || ws[i].Evidence.Remote != "github.com/acme/app" || ws[i].Evidence.Subpath != "." || ws[j].Importable || ws[j].Evidence == nil || ws[j].Evidence.Remote != "" {
			t.Fatalf("inventory %+v", ws)
		}
	})
}
