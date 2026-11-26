package opencode

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/execution"
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
		cfg := Settings{Endpoint: url, Auth: config.OpenCodeAuthNone}
		if local {
			cfg.Auth = config.OpenCodeAuthLocal
		}
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
		want, wantPath := "", ""
		if local {
			want, wantPath = "github.com/o/r", "."
		}
		if ws[0].Repo != want || ws[0].RepoPath != wantPath {
			t.Fatalf("local=%v repo %q path %q", local, ws[0].Repo, ws[0].RepoPath)
		}
	}
}
