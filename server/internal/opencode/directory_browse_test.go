package opencode

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/opencode/opencodetest"
	"github.com/flexdinesh/stage/server/internal/remote/remotetest"
)

// fsFake pins OpenCode 2.3.2's fs.list (verified on a throwaway opencode
// serve with temp HOME and XDG dirs): GET /api/fs/list?path= lists direct
// children of an absolute path, files and folders, hidden ones included,
// with entry paths relative to the request's location (folders end in /).
// A request without location[directory] resolves the default location and
// persists it as a project; one naming an existing project's folder
// persists nothing new. A missing path or a file is 404 FileNotFoundError; a
// missing location folder is 404 LocationNotFoundError.
type fsFake struct {
	t        *testing.T
	mu       sync.Mutex
	projects []map[string]any
	// folders are the host's folders; files are plain files.
	folders, files map[string]bool
	// defaultPersisted: a request named no location, so OpenCode would
	// have saved its working directory as a project.
	defaultPersisted bool
	locations        []string
}

func (f *fsFake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := r.URL.Query()
	notFound := func(tag, msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"_tag": tag, "message": msg})
	}
	switch r.URL.Path {
	case "/api/project":
		_ = json.NewEncoder(w).Encode(f.projects)
	case "/api/fs/list":
		location := q.Get("location[directory]")
		f.locations = append(f.locations, location)
		if location == "" {
			f.defaultPersisted = true
			location = "/srv/opencode"
		}
		if !f.folders[location] {
			notFound("LocationNotFoundError", "Location not found: "+location)
			return
		}
		dir := q.Get("path")
		if dir == "" {
			dir = location
		}
		if !path.IsAbs(dir) {
			dir = path.Join(location, dir)
		}
		if !f.folders[dir] {
			notFound("FileNotFoundError", "Directory not found: "+dir)
			return
		}
		data := []map[string]string{}
		for _, set := range []struct {
			all  map[string]bool
			kind string
		}{{f.folders, "directory"}, {f.files, "file"}} {
			for p := range set.all {
				if p != "/" && path.Dir(p) == dir {
					rel := relative(location, p)
					if set.kind == "directory" {
						rel += "/"
					}
					data = append(data, map[string]string{"path": rel, "type": set.kind})
				}
			}
		}
		slices.SortFunc(data, func(a, b map[string]string) int { return strings.Compare(a["path"], b["path"]) })
		_ = json.NewEncoder(w).Encode(map[string]any{"location": map[string]string{"directory": location}, "data": data})
	default:
		http.NotFound(w, r)
	}
}

// relative is to's path from the folder from, as OpenCode writes it.
func relative(from, to string) string {
	a, b := strings.Split(strings.Trim(from, "/"), "/"), strings.Split(strings.Trim(to, "/"), "/")
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	parts := []string{}
	for range a[i:] {
		parts = append(parts, "..")
	}
	return strings.Join(append(parts, b[i:]...), "/")
}

func project(id, canonical string, active, archived int64) map[string]any {
	return map[string]any{"id": id, "canonical": canonical, "time": map[string]int64{"created": 1, "updated": 2, "active": active, "archived": archived}, "sandboxes": []string{}}
}

// browseFixture serves the contract (fs.list when withFS) and the fake host.
func browseFixture(t *testing.T, withFS bool) (*Adapter, *fsFake) {
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
	if withFS {
		spec.Paths["/api/fs/list"] = map[string]json.RawMessage{"get": json.RawMessage(`{"operationId":"fs.list"}`)}
	}
	fake := &fsFake{t: t,
		projects: []map[string]any{
			project("global", "/", 90, 0),
			project("old", "/w/old", 80, 5),
			project("gone", "/w/gone", 70, 0),
			project("repo", "/w/repo", 60, 0),
			project("plain", "/w/plain", 10, 0),
		},
		folders: map[string]bool{"/": true, "/w": true, "/w/repo": true, "/w/repo/src": true, "/w/repo/.git": true, "/w/plain": true, "/w/old": true, "/w/Zeta": true, "/w/.cache": true, "/srv": true, "/srv/opencode": true},
		files:   map[string]bool{"/w/notes.txt": true, "/w/repo/go.mod": true},
	}
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

func names(r execution.BrowseResult) []string {
	out := []string{}
	for _, e := range r.Entries {
		out = append(out, e.Name)
	}
	return out
}

func TestBrowseAnchorsToAListedProject(t *testing.T) {
	a, fake := browseFixture(t, true)
	ctx := t.Context()
	// Default start: the parent of the most recently active usable project.
	// global and the archived project are skipped; gone's folder is missing,
	// so repo anchors.
	got, err := a.Browse(ctx, execution.BrowseRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != "/w" || got.Parent != "/" || got.Truncated || !slices.Equal(names(got), []string{"old", "plain", "repo", "Zeta"}) {
		t.Fatalf("default start %+v", got)
	}
	if got.Entries[2] != (execution.DirEntry{Name: "repo", Path: "/w/repo"}) {
		t.Fatalf("entry %+v", got.Entries[2])
	}
	got, err = a.Browse(ctx, execution.BrowseRequest{Path: "/w/repo/", ShowHidden: true})
	if err != nil || got.Path != "/w/repo" || got.Parent != "/w" || !slices.Equal(names(got), []string{".git", "src"}) {
		t.Fatalf("repo with hidden %+v %v", got, err)
	}
	got, err = a.Browse(ctx, execution.BrowseRequest{Path: "/"})
	if err != nil || got.Parent != "" || !slices.Equal(names(got), []string{"srv", "w"}) || got.Entries[1].Path != "/w" {
		t.Fatalf("root %+v %v", got, err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.defaultPersisted {
		t.Fatal("a listing named no location, so OpenCode would persist its working directory")
	}
	for _, loc := range fake.locations {
		if loc != "/w/gone" && loc != "/w/repo" {
			t.Fatalf("location %q is not a usable listed project", loc)
		}
	}
}

func TestBrowseRefusals(t *testing.T) {
	a, _ := browseFixture(t, true)
	ctx := t.Context()
	for path, reason := range map[string]string{
		"/w/missing":   "Folder not found on the OpenCode host: /w/missing.",
		"/w/notes.txt": "Folder not found on the OpenCode host: /w/notes.txt.",
		"w/repo":       "Enter an absolute path on the OpenCode host, like /home/me/project.",
		"/w/re​po":     "Path contains an invisible character (U+200B); retype it",
	} {
		_, err := a.Browse(ctx, execution.BrowseRequest{Path: path})
		var invalid *execution.Invalid
		if !errors.As(err, &invalid) || invalid.Reason != reason {
			t.Errorf("%q: %v, want Invalid %q", path, err, reason)
		}
	}
}

func TestBrowseWithoutAProject(t *testing.T) {
	a, fake := browseFixture(t, true)
	fake.projects = []map[string]any{project("global", "/", 9, 0), project("old", "/w/old", 8, 3)}
	_, err := a.Browse(t.Context(), execution.BrowseRequest{})
	var unavailable *execution.BrowseUnavailable
	if !errors.As(err, &unavailable) || unavailable.Reason != browseNoProject {
		t.Fatalf("no project: %v", err)
	}
	if len(fake.locations) != 0 {
		t.Fatalf("listed without an anchor: %v", fake.locations)
	}
}

func TestBrowseTruncates(t *testing.T) {
	a, fake := browseFixture(t, true)
	for i := range execution.MaxDirectoryEntries + 1 {
		fake.folders[fmt.Sprintf("/w/repo/src/d%03d", i)] = true
	}
	got, err := a.Browse(t.Context(), execution.BrowseRequest{Path: "/w/repo/src"})
	if err != nil || len(got.Entries) != execution.MaxDirectoryEntries || !got.Truncated {
		t.Fatalf("big folder: %d %v %v", len(got.Entries), got.Truncated, err)
	}
}

func TestBrowseFollowsTheContract(t *testing.T) {
	for _, withFS := range []bool{true, false} {
		a, fake := browseFixture(t, withFS)
		c, err := a.Features(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if err := c.DeclaresOperations(); err != nil {
			t.Fatal(err)
		}
		op := c.Operations[execution.OpDirectoryBrowse]
		if withFS {
			if !op.Available {
				t.Fatalf("with fs.list: %+v", op)
			}
			continue
		}
		if op.Available || op.Reason != directoryBrowseUnavailable {
			t.Fatalf("without fs.list: %+v", op)
		}
		var unavailable *execution.BrowseUnavailable
		if _, err := a.Browse(t.Context(), execution.BrowseRequest{}); !errors.As(err, &unavailable) || unavailable.Reason != directoryBrowseUnavailable {
			t.Fatalf("browse without fs.list: %v", err)
		}
		if len(fake.locations) != 0 {
			t.Fatalf("listed without the contract: %v", fake.locations)
		}
	}
}

// A worker relays the same declaration, listing and refusals.
func TestBrowseThroughWorker(t *testing.T) {
	local, _ := browseFixture(t, true)
	remote, fake := browseFixture(t, true)
	rig := remotetest.New(t, 2, map[int64]execution.Adapter{17: remote})
	proxy := rig.Adapter(17)
	ctx := t.Context()
	rc, err := proxy.Features(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !rc.Operations[execution.OpDirectoryBrowse].Available {
		t.Fatalf("worker declaration %+v", rc.Operations[execution.OpDirectoryBrowse])
	}
	browser, ok := execution.As[execution.DirectoryBrowser](proxy)
	if !ok {
		t.Fatal("worker hides the OpenCode folder browser")
	}
	for _, r := range []execution.BrowseRequest{{}, {Path: "/w/repo", ShowHidden: true}} {
		want, err := local.Browse(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		got, err := browser.Browse(ctx, r)
		if err != nil || !jsonEqual(t, want, got) {
			t.Fatalf("%+v: worker %+v %v, local %+v", r, got, err, want)
		}
	}
	var invalid *execution.Invalid
	if _, err := browser.Browse(ctx, execution.BrowseRequest{Path: "/w/missing"}); !errors.As(err, &invalid) || invalid.Reason != "Folder not found on the OpenCode host: /w/missing." {
		t.Fatalf("worker refusal %v", err)
	}
	if _, err := browser.Browse(ctx, execution.BrowseRequest{Path: "relative"}); !errors.As(err, &invalid) {
		t.Fatalf("worker relative path %v", err)
	}
	// Unavailable crosses the channel with the adapter's reason.
	fake.mu.Lock()
	fake.projects = []map[string]any{}
	fake.mu.Unlock()
	if _, err := browser.Browse(ctx, execution.BrowseRequest{}); err == nil || err.Error() != browseNoProject {
		t.Fatalf("worker unavailable %v", err)
	}
}
