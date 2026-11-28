package t3

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/flexdinesh/stage/server/internal/execution"
)

func addRequest(t *testing.T, source string, input map[string]any) execution.WorkspaceAddRequest {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return execution.WorkspaceAddRequest{Source: source, Input: raw, RequestID: "req-1"}
}

func wantInvalid(t *testing.T, err error, contains ...string) {
	t.Helper()
	var inv *execution.Invalid
	if !errors.As(err, &inv) {
		t.Fatalf("want *execution.Invalid, got %T %v", err, err)
	}
	for _, c := range contains {
		if !strings.Contains(inv.Reason, c) {
			t.Fatalf("reason %q lacks %q", inv.Reason, c)
		}
	}
}

func wantUncertain(t *testing.T, err error) {
	t.Helper()
	var u *execution.OutcomeUncertain
	if !errors.As(err, &u) {
		t.Fatalf("want *execution.OutcomeUncertain, got %T %v", err, err)
	}
}

func TestWorkspaceSourcesDeclared(t *testing.T) {
	c, err := NewAdapter(NewFake()).Features(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ValidateWorkspaceSources(); err != nil {
		t.Fatal(err)
	}
	if !c.Operations[execution.OpWorkspaceAdd].Available || len(c.WorkspaceSources) != 3 {
		t.Fatalf("declaration %+v %+v", c.Operations[execution.OpWorkspaceAdd], c.WorkspaceSources)
	}
	var ids []string
	for _, s := range c.WorkspaceSources {
		ids = append(ids, s.ID)
		if s.Confirm != "" || !s.Availability.Available {
			t.Fatalf("source %+v", s)
		}
	}
	if strings.Join(ids, ",") != "folder,clone,new" {
		t.Fatalf("sources %v", ids)
	}
	folder := c.WorkspaceSources[0]
	if err := folder.ValidateInput(json.RawMessage(`{"path":"/w","createIfMissing":true}`)); err != nil {
		t.Fatal(err)
	}
	if folder.ValidateInput(json.RawMessage(`{"title":"x"}`)) == nil {
		t.Fatal("folder without path accepted")
	}
	if c.WorkspaceSources[1].ValidateInput(json.RawMessage(`{"remoteUrl":"git@host:o/r.git","destination":"/w/r"}`)) != nil {
		t.Fatal("ssh clone URL refused")
	}
}

func TestAddFolder(t *testing.T) {
	ctx := t.Context()
	f := NewFake()
	a := NewAdapter(f)

	added, err := a.AddWorkspace(ctx, addRequest(t, SourceFolder, map[string]any{"path": " /work/app/ "}))
	if err != nil {
		t.Fatal(err)
	}
	if added.Existing || added.Workspace.WorkspaceRoot != "/work/app" || added.Workspace.Title != "app" || added.Workspace.ID == "" ||
		!strings.Contains(added.Message, "/work/app") {
		t.Fatalf("added %+v", added)
	}
	if len(f.Created) != 1 || f.Created[0].WorkspaceRoot != "/work/app" || f.Created[0].Title != "app" || f.Created[0].CreateIfMissing {
		t.Fatalf("created %+v", f.Created)
	}

	// The same folder again (an uncleaned spelling) is the existing project.
	again, err := a.AddWorkspace(ctx, addRequest(t, SourceFolder, map[string]any{"path": "/work/./app", "title": "Other"}))
	if err != nil || !again.Existing || again.Workspace.ID != added.Workspace.ID || len(f.Created) != 1 {
		t.Fatalf("again %+v %v (creates %d)", again, err, len(f.Created))
	}

	// A title and create-if-missing are passed through.
	f.MissFolder("/work/fresh")
	added, err = a.AddWorkspace(ctx, addRequest(t, SourceFolder, map[string]any{"path": "/work/fresh", "title": " Fresh ", "createIfMissing": true}))
	if err != nil || added.Workspace.Title != "Fresh" || !f.Created[1].CreateIfMissing {
		t.Fatalf("create if missing %+v %v %+v", added, err, f.Created)
	}
}

func TestAddFolderRefusals(t *testing.T) {
	ctx := t.Context()
	f := NewFake()
	a := NewAdapter(f)

	for _, p := range []string{"~/work", "work/app", "./x"} {
		_, err := a.AddWorkspace(ctx, addRequest(t, SourceFolder, map[string]any{"path": p}))
		wantInvalid(t, err, "absolute path on the T3 host")
	}
	if len(f.Created) != 0 {
		t.Fatalf("relative paths reached T3: %+v", f.Created)
	}

	f.MissFolder("/gone")
	_, err := a.AddWorkspace(ctx, addRequest(t, SourceFolder, map[string]any{"path": "/gone"}))
	wantInvalid(t, err, msgOperationFailed, "Create folder if missing")

	f.AddWorktreeContainer("/work/trees")
	_, err = a.AddWorkspace(ctx, addRequest(t, SourceFolder, map[string]any{"path": "/work/trees"}))
	wantInvalid(t, err, "holds a repository's worktrees")

	archived := f.AddProject("Old", "/work/old")
	if err := f.SetProject(archived.ID, func(p *Project) { now := time.Now(); p.ArchivedAt = &now }); err != nil {
		t.Fatal(err)
	}
	creates := len(f.Created)
	_, err = a.AddWorkspace(ctx, addRequest(t, SourceFolder, map[string]any{"path": "/work/old"}))
	wantInvalid(t, err, `archived project "Old"`, "Restore it in T3")
	if len(f.Created) != creates {
		t.Fatal("an archived folder reached t3_project_create")
	}

	// A deleted project's folder can be registered again.
	deleted := f.AddProject("Deleted", "/work/deleted")
	if err := f.SetProject(deleted.ID, func(p *Project) { now := time.Now(); p.DeletedAt = &now }); err != nil {
		t.Fatal(err)
	}
	added, err := a.AddWorkspace(ctx, addRequest(t, SourceFolder, map[string]any{"path": "/work/deleted"}))
	if err != nil || added.Existing || added.Workspace.ID == deleted.ID {
		t.Fatalf("re-register deleted %+v %v", added, err)
	}
}

// racingClient registers the folder between the adapter's list and create,
// as another T3 client could.
type racingClient struct {
	*Fake
	once sync.Once
}

func (c *racingClient) CreateProject(ctx context.Context, in CreateProjectInput) (CreatedProject, error) {
	c.once.Do(func() { c.Fake.AddProject("Racer", in.WorkspaceRoot) })
	return c.Fake.CreateProject(ctx, in)
}

func TestAddFolderDuplicateReturnsTheRegisteredProject(t *testing.T) {
	f := NewFake()
	added, err := NewAdapter(&racingClient{Fake: f}).AddWorkspace(t.Context(), addRequest(t, SourceFolder, map[string]any{"path": "/work/race"}))
	if err != nil || !added.Existing || added.Workspace.Title != "Racer" {
		t.Fatalf("duplicate %+v %v", added, err)
	}
}

func TestAddFolderLostReply(t *testing.T) {
	ctx := t.Context()
	f := NewFake()
	a := NewAdapter(f)

	f.LoseCreate = 1
	added, err := a.AddWorkspace(ctx, addRequest(t, SourceFolder, map[string]any{"path": "/work/lost"}))
	if err != nil || added.Existing || added.Workspace.WorkspaceRoot != "/work/lost" {
		t.Fatalf("lost reply reconciled %+v %v", added, err)
	}

	// The call never reached T3: nothing to find.
	f.CreateErr = errors.New("t3 t3_project_create: connection reset")
	_, err = a.AddWorkspace(ctx, addRequest(t, SourceFolder, map[string]any{"path": "/work/unsent"}))
	wantUncertain(t, err)
	f.CreateErr = nil

	// A retry with the same request finds the earlier registration.
	again, err := a.AddWorkspace(ctx, addRequest(t, SourceFolder, map[string]any{"path": "/work/lost"}))
	if err != nil || !again.Existing || again.Workspace.ID != added.Workspace.ID {
		t.Fatalf("retry %+v %v", again, err)
	}

	// Not connected is definite: nothing was sent.
	f.CreateErr = ErrNotConnected
	_, err = a.AddWorkspace(ctx, addRequest(t, SourceFolder, map[string]any{"path": "/work/offline"}))
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("not connected %v", err)
	}
}

func TestAddClone(t *testing.T) {
	ctx := t.Context()
	f := NewFake()
	a := NewAdapter(f)
	in := map[string]any{"remoteUrl": " git@github.com:o/repo.git ", "destination": "/src/repo/"}

	added, err := a.AddWorkspace(ctx, addRequest(t, SourceClone, in))
	if err != nil || added.Existing || added.Workspace.WorkspaceRoot != "/src/repo" || added.Workspace.Title != "repo" {
		t.Fatalf("clone %+v %v", added, err)
	}
	if strings.Contains(added.Message, "github.com") {
		t.Fatalf("message names the remote: %q", added.Message)
	}
	if len(f.Cloned) != 1 || f.Cloned[0].RemoteURL != "git@github.com:o/repo.git" || f.Cloned[0].DestinationPath != "/src/repo" {
		t.Fatalf("cloned %+v", f.Cloned)
	}

	// A registered destination is the existing project: no second clone.
	again, err := a.AddWorkspace(ctx, addRequest(t, SourceClone, in))
	if err != nil || !again.Existing || again.Workspace.ID != added.Workspace.ID || len(f.Cloned) != 1 {
		t.Fatalf("again %+v %v", again, err)
	}

	added, err = a.AddWorkspace(ctx, addRequest(t, SourceClone, map[string]any{"remoteUrl": "https://h/o/x.git", "destination": "/src/x", "title": "X app"}))
	if err != nil || added.Workspace.Title != "X app" {
		t.Fatalf("titled clone %+v %v", added, err)
	}

	_, err = a.AddWorkspace(ctx, addRequest(t, SourceClone, map[string]any{"remoteUrl": "https://h/o/x.git", "destination": "src/x"}))
	wantInvalid(t, err, "absolute path")

	f.OccupyFolder("/src/busy")
	_, err = a.AddWorkspace(ctx, addRequest(t, SourceClone, map[string]any{"remoteUrl": "https://h/o/y.git", "destination": "/src/busy"}))
	wantInvalid(t, err, msgDestinationInUse, "add it with Existing folder")

	f.CloneErr = &ToolError{Tool: "t3_project_clone", Code: "orchestration_error", Message: "Repository not found."}
	_, err = a.AddWorkspace(ctx, addRequest(t, SourceClone, map[string]any{"remoteUrl": "https://h/o/none.git", "destination": "/src/none"}))
	wantInvalid(t, err, "Repository not found.")
	f.CloneErr = nil
}

func TestAddCloneReconciliation(t *testing.T) {
	ctx := t.Context()
	f := NewFake()
	a := NewAdapter(f)

	// The clone's reply is lost: Stage cannot tell whether it cloned.
	f.LoseClone = 1
	_, err := a.AddWorkspace(ctx, addRequest(t, SourceClone, map[string]any{"remoteUrl": "https://h/o/a.git", "destination": "/src/a"}))
	wantUncertain(t, err)
	if len(f.Created) != 0 {
		t.Fatal("registered after an unconfirmed clone")
	}

	// The clone succeeded and the register reply is lost: it is listed.
	f.LoseCreate = 1
	added, err := a.AddWorkspace(ctx, addRequest(t, SourceClone, map[string]any{"remoteUrl": "https://h/o/b.git", "destination": "/src/b"}))
	if err != nil || added.Workspace.WorkspaceRoot != "/src/b" || len(f.Created) != 1 {
		t.Fatalf("lost register %+v %v %+v", added, err, f.Created)
	}

	// The register never reached T3 once: it is retried on the clone.
	f.CreateErr = errors.New("t3 t3_project_create: connection reset")
	retrying := &failOnce{Fake: f}
	added, err = NewAdapter(retrying).AddWorkspace(ctx, addRequest(t, SourceClone, map[string]any{"remoteUrl": "https://h/o/c.git", "destination": "/src/c"}))
	if err != nil || added.Workspace.WorkspaceRoot != "/src/c" || retrying.creates != 2 {
		t.Fatalf("retried register %+v %v creates=%d", added, err, retrying.creates)
	}

	// Both registers fail to reach T3: uncertain.
	_, err = a.AddWorkspace(ctx, addRequest(t, SourceClone, map[string]any{"remoteUrl": "https://h/o/d.git", "destination": "/src/d"}))
	wantUncertain(t, err)
	f.CreateErr = nil

	// T3 refuses the register: the clone is kept and named.
	f.AddWorktreeContainer("/src/e")
	_, err = a.AddWorkspace(ctx, addRequest(t, SourceClone, map[string]any{"remoteUrl": "https://h/o/e.git", "destination": "/src/e"}))
	wantInvalid(t, err, "cloned the repository into /src/e but could not register it", "Existing folder")

	// The retry after the uncertain clone points at Existing folder.
	_, err = a.AddWorkspace(ctx, addRequest(t, SourceClone, map[string]any{"remoteUrl": "https://h/o/a.git", "destination": "/src/a"}))
	wantInvalid(t, err, msgDestinationInUse, "Existing folder")
}

// failOnce lets the second create through after the fake's CreateErr.
type failOnce struct {
	*Fake
	creates int
}

func (c *failOnce) CreateProject(ctx context.Context, in CreateProjectInput) (CreatedProject, error) {
	c.creates++
	if c.creates == 2 {
		c.Fake.mu.Lock()
		saved := c.Fake.CreateErr
		c.Fake.CreateErr = nil
		c.Fake.mu.Unlock()
		defer func() { c.Fake.mu.Lock(); c.Fake.CreateErr = saved; c.Fake.mu.Unlock() }()
	}
	return c.Fake.CreateProject(ctx, in)
}

func TestAddNew(t *testing.T) {
	ctx := t.Context()
	f := NewFake()
	a := NewAdapter(f)

	added, err := a.AddWorkspace(ctx, addRequest(t, SourceNew, map[string]any{"title": " Billing API "}))
	if err != nil || added.Existing || added.Workspace.Title != "Billing API" || added.Workspace.WorkspaceRoot != FakeHome+"/.t3/projects/billing-api" || len(added.Facts) != 0 {
		t.Fatalf("new %+v %v", added, err)
	}
	if len(f.Created) != 1 || f.Created[0].WorkspaceRoot != "" || f.Created[0].CreateIfMissing {
		t.Fatalf("created %+v", f.Created)
	}

	// T3 takes the next free folder for the same title; a commit failure
	// is a warning on a project that exists.
	f.CommitError = "git user.email is not set"
	added, err = a.AddWorkspace(ctx, addRequest(t, SourceNew, map[string]any{"title": "Billing API"}))
	if err != nil || added.Workspace.WorkspaceRoot != FakeHome+"/.t3/projects/billing-api-2" ||
		len(added.Facts) != 1 || added.Facts[0].Tone != execution.ToneWarn || added.Facts[0].Value != f.CommitError {
		t.Fatalf("commit error %+v %v", added, err)
	}
	if err := execution.ValidateFacts(added.Facts); err != nil {
		t.Fatal(err)
	}
	f.CommitError = ""

	f.CreateErr = &ToolError{Tool: "t3_project_create", Code: "orchestration_error", Message: "No free project folder."}
	_, err = a.AddWorkspace(ctx, addRequest(t, SourceNew, map[string]any{"title": "X"}))
	wantInvalid(t, err, "No free project folder.")
	f.CreateErr = nil

	_, err = a.AddWorkspace(ctx, addRequest(t, SourceNew, map[string]any{"title": "  "}))
	wantInvalid(t, err, "title")
}

func TestAddNewLostReply(t *testing.T) {
	ctx := t.Context()
	f := NewFake()
	a := NewAdapter(f)
	f.AddProject("Docs", "/work/docs") // an older project with the same title

	f.LoseCreate = 1
	added, err := a.AddWorkspace(ctx, addRequest(t, SourceNew, map[string]any{"title": "Docs"}))
	if err != nil || added.Existing || added.Workspace.WorkspaceRoot != FakeHome+"/.t3/projects/docs" {
		t.Fatalf("one new match %+v %v", added, err)
	}

	// Nothing new: uncertain.
	f.CreateErr = errors.New("t3 t3_project_create: connection reset")
	_, err = a.AddWorkspace(ctx, addRequest(t, SourceNew, map[string]any{"title": "Docs"}))
	wantUncertain(t, err)
	f.CreateErr = nil

	// Two new projects with the title (another client raced): uncertain.
	racer := &racingNew{Fake: f}
	f.LoseCreate = 1
	_, err = NewAdapter(racer).AddWorkspace(ctx, addRequest(t, SourceNew, map[string]any{"title": "Docs"}))
	wantUncertain(t, err)
}

// racingNew makes another project with the same title during the create.
type racingNew struct{ *Fake }

func (c *racingNew) CreateProject(ctx context.Context, in CreateProjectInput) (CreatedProject, error) {
	c.Fake.AddProject(in.Title, "/elsewhere/"+in.Title)
	return c.Fake.CreateProject(ctx, in)
}

// TestWorkspaceAddOverMCP drives the adapter through the MCP client against
// T3-shaped tool results, including T3's failure encoding.
func TestWorkspaceAddOverMCP(t *testing.T) {
	f := newFakeT3(t)
	var mu sync.Mutex
	calls := map[string][]map[string]any{}
	listed := `[]`
	record := func(name string, args map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		calls[name] = append(calls[name], args)
	}
	f.tool("t3_project_list", func(args map[string]any) (*mcp.CallToolResult, error) {
		record("t3_project_list", args)
		mu.Lock()
		defer mu.Unlock()
		return structured(`{"projects":` + listed + `,"nextCursor":null}`), nil
	})
	f.tool("t3_project_create", func(args map[string]any) (*mcp.CallToolResult, error) {
		record("t3_project_create", args)
		switch args["workspaceRoot"] {
		case "/dup":
			mu.Lock()
			listed = `[{"id":"p-dup","title":"Dup","workspaceRoot":"/dup","createdAt":"2026-11-08T00:00:00.000Z","updatedAt":"2026-11-08T00:00:00.000Z"}]`
			mu.Unlock()
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{
				Text: `{"_tag":"OrchestratorMcpFailure","code":"invalid_request","message":"The workspace is already registered to a project."}`}}}, nil
		case nil:
			return structured(`{"id":"p-new","title":"Fresh","workspaceRoot":"/home/u/.t3/projects/fresh","createdAt":"2026-11-08T00:00:00.000Z","updatedAt":"2026-11-08T00:00:00.000Z","commitError":"no git identity"}`), nil
		}
		return structured(`{"id":"p-c","title":"repo","workspaceRoot":"/src/repo","createdAt":"2026-11-08T00:00:00.000Z","updatedAt":"2026-11-08T00:00:00.000Z"}`), nil
	})
	f.tool("t3_project_clone", func(args map[string]any) (*mcp.CallToolResult, error) {
		record("t3_project_clone", args)
		return structured(`{"cwd":"/src/repo","remoteUrl":"https://h/o/repo.git","repository":null}`), nil
	})
	a := NewAdapter(newTestClient(t, f, f.adminToken, NewMemoryTokenStore()))
	ctx := ctxT(t)

	added, err := a.AddWorkspace(ctx, addRequest(t, SourceFolder, map[string]any{"path": "/dup", "createIfMissing": true}))
	if err != nil || !added.Existing || added.Workspace.ID != "p-dup" {
		t.Fatalf("duplicate over MCP %+v %v", added, err)
	}
	if c := calls["t3_project_create"][0]; c["title"] != "dup" || c["workspaceRoot"] != "/dup" || c["createWorkspaceRootIfMissing"] != true {
		t.Fatalf("folder create args %v", c)
	}

	added, err = a.AddWorkspace(ctx, addRequest(t, SourceNew, map[string]any{"title": "Fresh"}))
	if err != nil || added.Workspace.ID != "p-new" || len(added.Facts) != 1 || added.Facts[0].Value != "no git identity" {
		t.Fatalf("new over MCP %+v %v", added, err)
	}
	if c := calls["t3_project_create"][1]; len(c) != 1 || c["title"] != "Fresh" {
		t.Fatalf("title-only create args %v", c)
	}

	added, err = a.AddWorkspace(ctx, addRequest(t, SourceClone, map[string]any{"remoteUrl": "https://h/o/repo.git", "destination": "/src/repo"}))
	if err != nil || added.Workspace.ID != "p-c" {
		t.Fatalf("clone over MCP %+v %v", added, err)
	}
	if c := calls["t3_project_clone"][0]; len(c) != 2 || c["remoteUrl"] != "https://h/o/repo.git" || c["destinationPath"] != "/src/repo" {
		t.Fatalf("clone args %v", c)
	}
	if c := calls["t3_project_create"][2]; c["workspaceRoot"] != "/src/repo" || c["title"] != "repo" || c["createWorkspaceRootIfMissing"] != nil {
		t.Fatalf("clone register args %v", c)
	}
}
