package opencode

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/opencode/opencodetest"
	"github.com/flexdinesh/stage/server/internal/remote/remotetest"
)

// resolution is how the fake resolves one directory, following OpenCode
// 2.3.2's Project.resolve: the project, the checkout top it found and the
// project's canonical checkout.
type resolution struct{ id, directory, canonical, vcs string }

type fakeProject struct {
	canonical, vcs string
	archived       int64
}

// locationFake pins OpenCode 2.3.2's undocumented location side effect
// (@opencode/Location, @opencode/Project): GET /api/location for an existing
// directory persists its project, clears an archived time and, for a
// version-controlled checkout away from the canonical one, saves a worktree.
// A missing directory is 404 LocationNotFoundError. Verified once against a
// throwaway opencode serve (temp HOME and XDG dirs).
type locationFake struct {
	t        *testing.T
	mu       sync.Mutex
	projects map[string]*fakeProject
	order    []string
	trees    map[string][]string
	requests []string
}

var resolutions = map[string]resolution{
	"/w/plain":    {"dir-plain", "/w/plain", "/w/plain", ""},
	"/w/repo":     {"repo", "/w/repo", "/w/repo", "git"},
	"/w/repo/sub": {"repo", "/w/repo", "/w/repo", "git"},
	"/w/repo-wt":  {"repo", "/w/repo-wt", "/w/repo", "git"},
	"/w/old":      {"old", "/w/old", "/w/old", "git"},
	"/w/emptygit": {"global", "/w/emptygit", "/w/emptygit", "git"},
	"/w/link":     {"dir-plain", "/w/plain", "/w/plain", ""},
}

func (f *locationFake) persist(r resolution) {
	p := f.projects[r.id]
	if p == nil {
		p = &fakeProject{canonical: r.canonical, vcs: r.vcs}
		f.projects[r.id] = p
		f.order = append(f.order, r.id)
	}
	p.archived = 0
	if r.vcs == "" {
		return
	}
	for _, dir := range []string{r.canonical, r.directory} {
		if !slices.Contains(f.trees[r.id], dir) {
			f.trees[r.id] = append(f.trees[r.id], dir)
		}
	}
}

func (f *locationFake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.URL.Path+"?"+r.URL.RawQuery)
	q := r.URL.Query()
	switch r.URL.Path {
	case "/api/project":
		if q.Get("archived") != "true" {
			f.t.Errorf("project list without archived=true: %s", r.URL)
		}
		out := []map[string]any{}
		for _, id := range f.order {
			p := f.projects[id]
			row := map[string]any{"id": id, "canonical": p.canonical, "time": map[string]int64{"created": 1, "updated": 2, "archived": p.archived}, "sandboxes": []string{}}
			if p.vcs != "" {
				row["vcs"] = p.vcs
			}
			out = append(out, row)
		}
		_ = json.NewEncoder(w).Encode(out)
	case "/api/worktree":
		trees := []map[string]string{}
		for _, dir := range f.trees[q.Get("projectID")] {
			trees = append(trees, map[string]string{"directory": dir})
		}
		_ = json.NewEncoder(w).Encode(trees)
	case "/api/location":
		dir := q.Get("location[directory]")
		switch dir {
		case "/w/file":
			http.Error(w, `{"_tag":"UnknownError","message":"Server failed to handle the request."}`, http.StatusInternalServerError)
			return
		case "/w/bad":
			http.Error(w, `{"_tag":"InvalidRequestError","message":"bad"}`, http.StatusBadRequest)
			return
		case "/w/drop":
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		case "/w/garbled":
			_, _ = w.Write([]byte(`{"directory":"/w/garbled","project":{}}`))
			return
		}
		res, ok := resolutions[dir]
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"_tag": "LocationNotFoundError", "location": map[string]string{"directory": dir}, "message": "Location not found: " + dir})
			return
		}
		f.persist(res)
		_ = json.NewEncoder(w).Encode(map[string]any{"directory": dir, "project": map[string]string{"id": res.id, "directory": res.directory, "canonical": res.canonical}})
	default:
		http.NotFound(w, r)
	}
}

func (f *locationFake) take() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	got := f.requests
	f.requests = nil
	return got
}

// addFixture serves the fake contract, with location.get when withLocation.
func addFixture(t *testing.T, withLocation bool) (*Adapter, *locationFake) {
	t.Helper()
	base := opencodetest.New(t, "").Config.Handler
	rec := httptest.NewRecorder()
	base.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	if withLocation {
		spec.Paths["/api/location"] = map[string]json.RawMessage{"get": json.RawMessage(`{"operationId":"location.get"}`)}
	}
	fake := &locationFake{t: t, projects: map[string]*fakeProject{"old": {canonical: "/w/old", vcs: "git", archived: 3}}, order: []string{"old"}, trees: map[string][]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/info":
			base.ServeHTTP(w, r)
		case "/openapi.json":
			_ = json.NewEncoder(w).Encode(spec)
		default:
			fake.serve(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	a, err := New(17, Settings{Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return a, fake
}

func folder(dir string) execution.WorkspaceAddRequest {
	input, _ := json.Marshal(map[string]string{"path": dir})
	return execution.WorkspaceAddRequest{Source: sourceFolder, Input: input, RequestID: "r-" + dir}
}

func locationCalls(reqs []string) []string {
	var out []string
	for _, r := range reqs {
		if path, raw, _ := strings.Cut(r, "?"); path == "/api/location" {
			q, _ := url.ParseQuery(raw)
			out = append(out, q.Get("location[directory]"))
		}
	}
	return out
}

func TestAddWorkspaceFolder(t *testing.T) {
	a, fake := addFixture(t, true)
	ctx := t.Context()
	for _, c := range []struct {
		name, dir, id, root, message string
		existing                     bool
	}{
		{"a plain folder becomes its own project", "/w/plain", "dir-plain", "/w/plain", "OpenCode added plain at /w/plain.", false},
		{"adding it again changes nothing", "/w/plain", "dir-plain", "/w/plain", "OpenCode already lists plain at /w/plain.", true},
		{"the path is cleaned", "/w/plain/", "dir-plain", "/w/plain", "OpenCode already lists plain at /w/plain.", true},
		{"a symlink resolves to its target's project", "/w/link", "dir-plain", "/w/plain", "OpenCode already lists plain at /w/plain. OpenCode keeps /w/link under that project.", true},
		{"a subfolder joins its repository's project", "/w/repo/sub", "repo", "/w/repo", "OpenCode added repo at /w/repo. OpenCode keeps /w/repo/sub under that project.", false},
		{"a linked worktree joins the listed project", "/w/repo-wt", "repo", "/w/repo", "OpenCode already lists repo at /w/repo. OpenCode keeps /w/repo-wt under that project.", true},
		{"an archived project is restored", "/w/old", "old", "/w/old", "OpenCode restored the archived project old at /w/old.", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := a.AddWorkspace(ctx, folder(c.dir))
			if err != nil {
				t.Fatal(err)
			}
			if got.Workspace.ID != c.id || got.Workspace.WorkspaceRoot != c.root || got.Existing != c.existing || got.Message != c.message {
				t.Fatalf("added %+v", got)
			}
			if calls := locationCalls(fake.take()); !slices.Equal(calls, []string{strings.TrimSuffix(c.dir, "/")}) {
				t.Fatalf("location calls %v", calls)
			}
		})
	}
	// Inventory lists every added project; the plain folder has no VCS, so it
	// is a row of its own that discovery does not import.
	ws, err := a.Workspaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]execution.WorkspaceInfo{}
	for _, w := range ws {
		rows[w.ID] = w
	}
	if len(rows) != 3 || rows["dir-plain"].Importable || rows["dir-plain"].WorkspaceRoot != "/w/plain" || !rows["repo"].Importable || rows["old"].ArchivedAt != nil {
		t.Fatalf("inventory after adds %+v", ws)
	}
	if trees := fake.trees["repo"]; !slices.Equal(trees, []string{"/w/repo", "/w/repo-wt"}) {
		t.Fatalf("repo worktrees %v", trees)
	}
}

func TestAddWorkspaceRefusals(t *testing.T) {
	a, fake := addFixture(t, true)
	ctx := t.Context()
	for _, c := range []struct {
		name   string
		req    execution.WorkspaceAddRequest
		reason string
		sent   bool
	}{
		{"missing folder", folder("/w/missing"), folderNotFound, true},
		{"global project", folder("/w/emptygit"), folderInGlobal, true},
		{"refused path", folder("/w/bad"), "OpenCode refused this folder path.", true},
		{"relative path", folder("w/plain"), "Enter an absolute path on the OpenCode host, like /home/me/project.", false},
		{"invisible character", folder("/w/pla​in"), "Path contains an invisible character (U+200B); retype it", false},
		{"unknown source", execution.WorkspaceAddRequest{Source: "clone", Input: json.RawMessage(`{}`), RequestID: "r"}, `OpenCode has no workspace source "clone".`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := a.AddWorkspace(ctx, c.req)
			var invalid *execution.Invalid
			if !errors.As(err, &invalid) || invalid.Reason != c.reason {
				t.Fatalf("got %v, want Invalid %q", err, c.reason)
			}
			if calls := locationCalls(fake.take()); (len(calls) == 1) != c.sent {
				t.Fatalf("location calls %v", calls)
			}
		})
	}
	// An HTTP failure is an error, not a refusal or an uncertain outcome.
	_, err := a.AddWorkspace(ctx, folder("/w/file"))
	var invalid *execution.Invalid
	var uncertain *execution.OutcomeUncertain
	if err == nil || errors.As(err, &invalid) || errors.As(err, &uncertain) || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("file path: %v", err)
	}
	// No answer or an unreadable one: OpenCode may have saved the project.
	for _, dir := range []string{"/w/drop", "/w/garbled"} {
		if _, err := a.AddWorkspace(ctx, folder(dir)); !errors.As(err, &uncertain) {
			t.Fatalf("%s: %v, want OutcomeUncertain", dir, err)
		}
	}
}

func TestAddWorkspaceFollowsTheContract(t *testing.T) {
	for _, withLocation := range []bool{true, false} {
		a, fake := addFixture(t, withLocation)
		c, err := a.Features(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if err := c.DeclaresOperations(); err != nil {
			t.Fatal(err)
		}
		if err := c.ValidateWorkspaceSources(); err != nil {
			t.Fatal(err)
		}
		op := c.Operations[execution.OpWorkspaceAdd]
		if len(c.WorkspaceSources) != 1 {
			t.Fatalf("sources %+v", c.WorkspaceSources)
		}
		src := c.WorkspaceSources[0]
		field := src.Input[0]
		if src.ID != "folder" || src.Label != "Existing folder" || len(src.Input) != 1 || field.ID != "path" || field.Kind != execution.FieldPath || !field.Required || field.Help != "Absolute path on the OpenCode host" {
			t.Fatalf("folder source %+v", src)
		}
		if withLocation {
			if !op.Available || !src.Availability.Available {
				t.Fatalf("with location.get: op %+v source %+v", op, src.Availability)
			}
			continue
		}
		if op.Available || op.Reason != workspaceAddUnavailable || src.Availability.Available || src.Availability.Reason != workspaceAddUnavailable {
			t.Fatalf("without location.get: op %+v source %+v", op, src.Availability)
		}
		if _, err := a.AddWorkspace(t.Context(), folder("/w/plain")); !errors.Is(err, execution.ErrUnavailable) {
			t.Fatalf("add without location.get: %v", err)
		}
		if calls := locationCalls(fake.take()); calls != nil {
			t.Fatalf("location calls without the contract: %v", calls)
		}
	}
}

// A worker relays the same declaration, result and refusal.
func TestAddWorkspaceThroughWorker(t *testing.T) {
	local, _ := addFixture(t, true)
	remote, _ := addFixture(t, true)
	rig := remotetest.New(t, 2, map[int64]execution.Adapter{17: remote})
	proxy := rig.Adapter(17)
	ctx := t.Context()
	lc, err := local.Features(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := proxy.Features(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !rc.Operations[execution.OpWorkspaceAdd].Available || !jsonEqual(t, lc.WorkspaceSources, rc.WorkspaceSources) {
		t.Fatalf("worker declaration %+v %+v", rc.Operations[execution.OpWorkspaceAdd], rc.WorkspaceSources)
	}
	adder, ok := execution.As[execution.WorkspaceAdder](proxy)
	if !ok {
		t.Fatal("worker hides the OpenCode workspace adder")
	}
	for _, dir := range []string{"/w/repo/sub", "/w/repo"} {
		want, err := local.AddWorkspace(ctx, folder(dir))
		if err != nil {
			t.Fatal(err)
		}
		got, err := adder.AddWorkspace(ctx, folder(dir))
		if err != nil || !jsonEqual(t, want, got) {
			t.Fatalf("%s: worker %+v %v, local %+v", dir, got, err, want)
		}
	}
	_, err = adder.AddWorkspace(ctx, folder("/w/missing"))
	var invalid *execution.Invalid
	if !errors.As(err, &invalid) || invalid.Reason != folderNotFound {
		t.Fatalf("worker refusal %v", err)
	}
}

func jsonEqual(t *testing.T, a, b any) bool {
	t.Helper()
	x, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	y, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return string(x) == string(y)
}
