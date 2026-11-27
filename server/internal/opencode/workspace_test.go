package opencode

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/opencode/opencodetest"
)

// OpenCode declares every operation, and why it lacks one.
func TestOperationsDeclared(t *testing.T) {
	if err := (execution.Capabilities{Operations: operations()}).DeclaresOperations(); err != nil {
		t.Fatal(err)
	}
}

func TestTitle(t *testing.T) {
	for _, c := range []struct{ name, canonical, want string }{
		{"Named", "/w/x", "Named"},
		{"", "/home/u/workspace/stage", "stage"},
		{"", "/home/u/workspace/octo/main", "octo/main"},
		{"", "/home/u/workspace/octo/trunk/", "octo/trunk"},
		{"", "/main", "main"},
		{"", "/", "/"},
	} {
		if got := title(c.name, c.canonical); got != c.want {
			t.Errorf("title(%q,%q)=%q want %q", c.name, c.canonical, got, c.want)
		}
	}
}

func inventoryServer(t *testing.T, projects []map[string]any) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/project" || r.URL.Query().Get("archived") != "true" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(projects)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestWorkspacesImportableAndRepo(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "config"), []byte("[remote \"origin\"]\n\turl = git@github.com:o/r.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	times := func(archived int64) map[string]int64 {
		return map[string]int64{"created": 1, "updated": 2, "active": 3, "archived": archived}
	}
	url := inventoryServer(t, []map[string]any{
		{"id": "git", "canonical": repo, "vcs": "git", "time": times(0)},
		{"id": "plain", "canonical": "/home/u", "time": times(0)},
		{"id": "gone", "canonical": "/w/gone", "vcs": "git", "time": times(5)},
		{"id": "global", "canonical": "/", "vcs": "git", "time": times(0)},
	})
	for _, local := range []bool{true, false} {
		cfg := Settings{Endpoint: url, Local: local}
		a, err := New(2, cfg)
		if err != nil {
			t.Fatal(err)
		}
		ws, err := a.Workspaces(t.Context())
		if err != nil || len(ws) != 4 {
			t.Fatal(ws, err)
		}
		importable := map[string]bool{}
		for _, w := range ws {
			importable[w.ID] = w.Importable
		}
		if !importable["git"] || importable["plain"] || importable["gone"] || importable["global"] {
			t.Fatalf("importable %v", importable)
		}
		if ws[0].ActiveAt == nil || ws[0].Title != filepath.Base(repo) {
			t.Fatalf("workspace %+v", ws[0])
		}
		// Harness facts only: a local service's paths are the machine's,
		// whose ops read the Git evidence.
		if ws[0].Local != local || ws[0].Repo != "" || ws[0].ImportIf != "" || ws[0].Evidence != nil {
			t.Fatalf("local=%v workspace %+v", local, ws[0])
		}
		if ws, err = (machine.LocalOps{}).Inventory(t.Context(), ws); err != nil {
			t.Fatal(err)
		}
		w := ws[0]
		if !w.Importable || w.Repo != "" || w.RepoPath != "" {
			t.Fatalf("local=%v changed harness facts: %+v", local, w)
		}
		evidence := w.Evidence
		if local && (evidence == nil || evidence.Remote != "github.com/o/r" || evidence.Subpath != ".") {
			t.Fatalf("local checkout evidence %+v", evidence)
		}
		if !local && evidence != nil {
			t.Fatalf("network service checkout evidence %+v", evidence)
		}
	}
}

// locationServer is a fake OpenCode that records every request. Its
// contract routes come from opencodetest; it serves the project and
// worktree inventories and the location-scoped catalog.
func locationServer(t *testing.T) (*Adapter, func() []string) {
	t.Helper()
	contract, err := url.Parse(opencodetest.New(t, "").URL)
	if err != nil {
		t.Fatal(err)
	}
	fallback := httputil.NewSingleHostReverseProxy(contract)
	stamp := map[string]int64{"created": 1, "updated": 2}
	archived := map[string]int64{"created": 1, "updated": 2, "archived": 3}
	var mu sync.Mutex
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.Path+"?"+r.URL.RawQuery)
		mu.Unlock()
		q := r.URL.Query()
		switch r.URL.Path {
		case "/api/project":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": "repo", "canonical": "/w/repo", "vcs": "git", "time": stamp},
				{"id": "gone", "canonical": "/w/gone", "vcs": "git", "time": archived},
			})
		case "/api/worktree":
			trees := []map[string]string{}
			switch q.Get("projectID") {
			case "repo":
				trees = []map[string]string{{"directory": "/w/repo"}, {"directory": "/w/repo-wt", "strategy": "git"}}
			case "gone":
				trees = []map[string]string{{"directory": "/w/gone"}, {"directory": "/w/gone-wt"}}
			}
			_ = json.NewEncoder(w).Encode(trees)
		case "/api/model":
			_ = json.NewEncoder(w).Encode(map[string]any{"location": map[string]string{"directory": q.Get("location[directory]")}, "data": []map[string]any{{"id": "m", "providerID": "p", "name": "M", "enabled": true}}})
		case "/api/agent":
			_ = json.NewEncoder(w).Encode(map[string]any{"location": map[string]string{"directory": q.Get("location[directory]")}, "data": []map[string]any{{"id": "build", "name": "Build", "mode": "primary"}}})
		case "/api/location":
			t.Errorf("location request %s persists the project", r.URL)
			http.NotFound(w, r)
		default:
			fallback.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	a, err := New(2, Settings{Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return a, func() []string {
		mu.Lock()
		defer mu.Unlock()
		got := requests
		requests = nil
		return got
	}
}

// Verifying a binding uses only pure inventory reads; location-scoped reads
// upsert and unarchive the project, so they wait until it verifies.
func TestVerifyLocationUsesPureReads(t *testing.T) {
	a, requests := locationServer(t)
	ctx := t.Context()
	// scoped lists requests to /api/location or carrying a location.
	scoped := func(reqs []string) []string {
		var out []string
		for _, r := range reqs {
			path, raw, _ := strings.Cut(r, "?")
			q, err := url.ParseQuery(raw)
			if err != nil || path == "/api/location" || q.Has("location[directory]") {
				out = append(out, r)
			}
		}
		return out
	}
	if _, err := a.Workspaces(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Features(ctx); err != nil {
		t.Fatal(err)
	}
	if got := scoped(requests()); got != nil {
		t.Fatalf("inventory made location requests: %v", got)
	}
	for _, dir := range []string{"/w/repo", "/w/repo-wt"} {
		c, err := a.FeaturesForWorkspace(ctx, "repo", dir)
		if err != nil || len(c.Models) != 1 || len(c.Modes) != 1 {
			t.Fatalf("%s: %+v %v", dir, c, err)
		}
		// Only the catalog reads, after verification, carry the location.
		got := requests()
		want := []string{"/api/model?" + queryDirectory(dir)[1:], "/api/agent?" + queryDirectory(dir)[1:]}
		if s := scoped(got); !slices.Equal(s, want) {
			t.Fatalf("%s: location-scoped requests %v, want %v", dir, s, want)
		}
	}
	for _, tc := range []struct{ project, dir, want string }{
		{"gone", "/w/gone", "Archived in OpenCode. Unarchive it there, then refresh."},
		{"unknown", "/w/unknown", "OpenCode no longer lists this project. Refresh workspaces."},
		{"repo", "/w/other", "OpenCode resolved a different checkout or project. Verify its binding."},
	} {
		_, err := a.FeaturesForWorkspace(ctx, tc.project, tc.dir)
		if err == nil || err.Error() != tc.want {
			t.Fatalf("%s %s: %v, want %q", tc.project, tc.dir, err, tc.want)
		}
		if got := scoped(requests()); got != nil {
			t.Fatalf("%s %s: location-scoped requests %v", tc.project, tc.dir, got)
		}
	}
	if err := a.verifyLocation(ctx, "", "/w/repo"); err == nil {
		t.Fatal("accepted an unbound project")
	}
	if err := a.verifyWorktree(ctx, "repo", "/w/repo-wt"); err != nil {
		t.Fatal(err)
	}
	if got := requests(); len(scoped(got)) != 0 || len(got) != 2 {
		t.Fatalf("worktree verification requests %v", got)
	}
	for _, tc := range []struct{ project, dir, want string }{
		{"repo", "/w/other", "created workspace is missing from the OpenCode project inventory"},
		{"gone", "/w/gone-wt", "Archived in OpenCode. Unarchive it there, then refresh."},
	} {
		if err := a.verifyWorktree(ctx, tc.project, tc.dir); err == nil || err.Error() != tc.want {
			t.Fatalf("%s %s: %v, want %q", tc.project, tc.dir, err, tc.want)
		}
		if got := scoped(requests()); got != nil {
			t.Fatalf("%s %s: location-scoped requests %v", tc.project, tc.dir, got)
		}
	}
}
