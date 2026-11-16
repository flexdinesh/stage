package install

import (
	"archive/tar"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGitHub serves releases from asset dirs keyed by version, through
// the releases/latest redirect and download URLs, and through the API.
func fakeGitHub(t *testing.T, latest string, rels map[string]string, token string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /o/r/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/o/r/releases/tag/v"+latest, http.StatusFound)
	})
	mux.HandleFunc("GET /o/r/releases/download/{tag}/{name}", func(w http.ResponseWriter, r *http.Request) {
		dir, ok := rels[strings.TrimPrefix(r.PathValue("tag"), "v")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(dir, r.PathValue("name")))
	})
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.NotFound(w, r)
			return false
		}
		return true
	}
	mux.HandleFunc("GET /api/repos/o/r/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			_, _ = w.Write([]byte(`{"tag_name":"v` + latest + `"}`))
		}
	})
	var srv *httptest.Server
	mux.HandleFunc("GET /api/repos/o/r/releases/tags/{tag}", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		v := strings.TrimPrefix(r.PathValue("tag"), "v")
		dir, ok := rels[v]
		if !ok {
			http.NotFound(w, r)
			return
		}
		ents, _ := os.ReadDir(dir)
		var parts []string
		for _, e := range ents {
			parts = append(parts, `{"name":"`+e.Name()+`","url":"`+srv.URL+`/api/assets/`+v+`/`+e.Name()+`"}`)
		}
		_, _ = w.Write([]byte(`{"assets":[` + strings.Join(parts, ",") + `]}`))
	})
	mux.HandleFunc("GET /api/assets/{v}/{name}", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) || r.Header.Get("Accept") != "application/octet-stream" {
			return
		}
		http.ServeFile(w, r, filepath.Join(rels[r.PathValue("v")], r.PathValue("name")))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func source(srv *httptest.Server, token string) *GitHub {
	return &GitHub{Repo: "o/r", Web: srv.URL, API: srv.URL + "/api", Token: token, Client: srv.Client()}
}

func installedEnv(t *testing.T) (*env, *Record) {
	t.Helper()
	e := newEnv(t, "stage")
	mustOK(t, e.install("1.0.0", makeRelease(t, rel{identity: "stage", version: "1.0.0"}), Options{Mode: ModeHeadless}))
	rec, err := Installed(e.d, e.layout(ModeHeadless).Link)
	if err != nil {
		t.Fatal(err)
	}
	return e, rec
}

func TestUpdateFromGitHub(t *testing.T) {
	for _, token := range []string{"", "tok"} {
		t.Run("token="+token, func(t *testing.T) {
			e, rec := installedEnv(t)
			srv := fakeGitHub(t, "1.1.0", map[string]string{"1.1.0": makeRelease(t, rel{identity: "stage", version: "1.1.0"})}, "tok")
			src := source(srv, token)
			v, newer, err := Target(context.Background(), src, rec, UpdateOptions{})
			if err != nil || v != "1.1.0" || !newer {
				t.Fatalf("target %s %v %v", v, newer, err)
			}
			prep, err := Prepare(context.Background(), e.d, src, rec, v, UpdateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer prep.Cleanup()
			if cliVersion(t, prep.CLI) != "1.1.0" || prep.Mode != ModeHeadless {
				t.Fatal("prepared the wrong CLI")
			}
			// The new CLI installs what was prepared.
			mustOK(t, e.install("1.1.0", prep.Dir, Options{}))
			if cliVersion(t, e.layout(ModeHeadless).Link) != "1.1.0" {
				t.Fatal("not updated")
			}
		})
	}
}

func TestUpdateUpToDateAndOlderLatest(t *testing.T) {
	_, rec := installedEnv(t)
	srv := fakeGitHub(t, "1.0.0", nil, "")
	if _, newer, err := Target(context.Background(), source(srv, ""), rec, UpdateOptions{}); err != nil || newer {
		t.Fatalf("newer %v err %v", newer, err)
	}
	srv = fakeGitHub(t, "0.9.0", nil, "")
	v, newer, err := Target(context.Background(), source(srv, ""), rec, UpdateOptions{})
	if err != nil || newer {
		t.Fatalf("newer %v err %v", newer, err)
	}
	if !strings.Contains(UpToDate(rec, v).Message, "newer than the latest") {
		t.Fatal("message")
	}
	// A pin may go anywhere, including pre-releases.
	if v, newer, err := Target(context.Background(), source(srv, ""), rec, UpdateOptions{Version: "v1.2.0-rc.1"}); err != nil || v != "1.2.0-rc.1" || !newer {
		t.Fatalf("pin %s %v %v", v, newer, err)
	}
}

func TestUpdateTamperedDownloadRejected(t *testing.T) {
	e, rec := installedEnv(t)
	dir := makeRelease(t, rel{identity: "stage", version: "1.1.0"})
	p := headless(t, dir)
	b, _ := os.ReadFile(p)
	_ = os.WriteFile(p, b[:len(b)-10], 0o644)
	srv := fakeGitHub(t, "1.1.0", map[string]string{"1.1.0": dir}, "")
	_, err := Prepare(context.Background(), e.d, source(srv, ""), rec, "1.1.0", UpdateOptions{})
	if CodeOf(err) != CodeVerify {
		t.Fatalf("err %v", err)
	}
	if got := tree(t, e.layout(ModeHeadless).Staging); len(got) != 0 {
		t.Fatalf("staging left %v", got)
	}
}

func TestGitHubErrors(t *testing.T) {
	_, rec := installedEnv(t)
	rate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1700000000")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer rate.Close()
	_, _, err := Target(context.Background(), source(rate, "tok"), rec, UpdateOptions{})
	if CodeOf(err) != CodeNetwork || !strings.Contains(err.Error(), "rate limit") || !strings.Contains(FixOf(err), "gh auth login") {
		t.Fatalf("rate limit: %v (%s)", err, FixOf(err))
	}
	missing := httptest.NewServer(http.NotFoundHandler())
	defer missing.Close()
	_, _, err = Target(context.Background(), source(missing, ""), rec, UpdateOptions{})
	if CodeOf(err) != CodeNetwork || !strings.Contains(FixOf(err), "private") {
		t.Fatalf("not found: %v", err)
	}
	offline := httptest.NewServer(http.NotFoundHandler())
	offline.Close()
	_, _, err = Target(context.Background(), source(offline, ""), rec, UpdateOptions{})
	if CodeOf(err) != CodeNetwork || !strings.Contains(err.Error(), "cannot reach GitHub") {
		t.Fatalf("offline: %v", err)
	}
	pre := fakeGitHub(t, "2.0.0-rc.1", nil, "")
	if _, _, err := Target(context.Background(), source(pre, ""), rec, UpdateOptions{}); CodeOf(err) != CodeNetwork {
		t.Fatalf("pre-release as latest: %v", err)
	}
}

func TestInstalledRefusals(t *testing.T) {
	e, _ := installedEnv(t)
	d := e.d
	if _, err := Installed(d, "/opt/stage-desktop/bin/stage"); CodeOf(err) != CodeUnsupported || !strings.Contains(FixOf(err), "package manager") {
		t.Fatalf("package: %v", err)
	}
	d.Identity = "stage-dev"
	if _, err := Installed(d, e.layout(ModeHeadless).Link); CodeOf(err) != CodeUnsupported || !strings.Contains(FixOf(err), "install-dev.sh") {
		t.Fatalf("dev: %v", err)
	}
}

func TestExtractRefusesLinkEscapes(t *testing.T) {
	for _, h := range []*tar.Header{
		{Name: "stage-1.0.0-x/evil", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		{Name: "stage-1.0.0-x/evil", Typeflag: tar.TypeSymlink, Linkname: "../../x"},
		{Name: "stage-1.0.0-x/dev", Typeflag: tar.TypeChar},
	} {
		dir := t.TempDir()
		p := filepath.Join(dir, "a.tar.gz")
		writeTarGz(t, p, []member{{name: "stage-1.0.0-x/stage", body: []byte("x"), mode: 0o755}, {name: h.Name, typ: h.Typeflag, link: h.Linkname}})
		err := extractApp(p, appTree{strip: "stage-1.0.0-x/", cli: "bin/stage", rename: map[string]string{"stage": "bin/stage"}}, filepath.Join(dir, "out"))
		if err == nil {
			t.Fatalf("accepted %s -> %s", h.Name, h.Linkname)
		}
	}
	// A chain of in-tree links cannot be used to write outside.
	dir := t.TempDir()
	p := filepath.Join(dir, "a.tar.gz")
	writeTarGz(t, p, []member{
		{name: "d/b", typ: tar.TypeSymlink, link: ".."},
		{name: "d/b/b2", typ: tar.TypeSymlink, link: ".."},
		{name: "d/b/b2/evil", body: []byte("x")},
	})
	if err := extractApp(p, appTree{cli: "x"}, filepath.Join(dir, "out")); err == nil {
		t.Fatal("followed a symlink chain")
	}
	if _, err := os.Stat(filepath.Join(dir, "evil")); err == nil {
		t.Fatal("wrote outside the tree")
	}
}
