package target

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeURL(t *testing.T) {
	ok := map[string]string{
		"http://192.168.1.20:7380":     "http://192.168.1.20:7380",
		" HTTP://Box.LAN:7380/ ":       "http://box.lan:7380",
		"https://stage.example.ts.net": "https://stage.example.ts.net",
		"https://h.example:443":        "https://h.example",
		"http://h.example:80/":         "http://h.example",
		"http://[::1]:7380":            "http://[::1]:7380",
		"http://[fe80::1]":             "http://[fe80::1]",
	}
	for in, want := range ok {
		got, err := NormalizeURL(in)
		if err != nil || got != want {
			t.Errorf("NormalizeURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "box.lan:7380", "ftp://h", "http://", "http://u:p@h", "http://h/stage", "http://h/?a=1", "http://h/#x", "mygo://localhost"} {
		if got, err := NormalizeURL(in); err == nil {
			t.Errorf("NormalizeURL(%q) = %q, want an error", in, got)
		}
	}
}

func TestTargetHostAndBase(t *testing.T) {
	l := Local("/run/s.sock")
	if l.Host() != SocketHost || l.BaseURL() != "http://stage.invalid" || l.Remote() {
		t.Errorf("local %q %q", l.Host(), l.BaseURL())
	}
	r := Target{ID: "s-1", Name: "Box", Kind: KindRemote, URL: "https://box.example:8443"}
	if r.Host() != "box.example:8443" || r.BaseURL() != r.URL || !r.Remote() {
		t.Errorf("remote %q %q", r.Host(), r.BaseURL())
	}
	if !r.Same(Target{ID: "s-1", Name: "Other", Kind: KindRemote, URL: r.URL}) || r.Same(Target{ID: "s-1", Kind: KindRemote, URL: "http://x"}) {
		t.Error("Same ignores names only")
	}
}

// TestTransportRemoteTCP: a remote target reaches its host over TCP with
// its Host header and never through HTTP_PROXY.
func TestTransportRemoteTCP(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:9")
	var host string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host = r.Host
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()
	u, _ := NormalizeURL(srv.URL)
	tg := Target{ID: "s-1", Kind: KindRemote, URL: u}
	req, _ := http.NewRequest("GET", tg.BaseURL()+"/api/health", nil)
	res, err := (&http.Client{Transport: tg.Transport()}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(b) != "ok" || host != tg.Host() {
		t.Fatalf("body %q host %q", b, host)
	}
	var de *DialError
	if _, err := Local("").Dial(context.Background()); !errors.As(err, &de) || !errors.Is(err, ErrNoSocket) {
		t.Errorf("unset socket: %v", err)
	}
	dead := Target{ID: "s-2", Kind: KindRemote, URL: "http://127.0.0.1:1"}
	if _, err := dead.Dial(context.Background()); !errors.As(err, &de) {
		t.Errorf("dead remote: %v", err)
	}
}

func TestStoreMissingAndRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "servers.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if l := s.List(); len(l) != 1 || l[0].ID != LocalID || l[0].Name != "This machine" || l[0].Kind != KindLocal {
		t.Fatalf("fresh list %+v", l)
	}
	if a := s.Active(); a.ID != LocalID {
		t.Fatalf("fresh active %+v", a)
	}
	box, err := s.Add(" Box ", "http://Box.lan:7380/")
	if err != nil {
		t.Fatal(err)
	}
	if box.Name != "Box" || box.URL != "http://box.lan:7380" || box.Kind != KindRemote || !strings.HasPrefix(box.ID, "s-") {
		t.Fatalf("added %+v", box)
	}
	if err := s.SetActive(box.ID); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file %v %v", fi, err)
	}
	b, _ := os.ReadFile(path)
	if want := `{"version":1,"active":"` + box.ID + `","servers":[{"id":"` + box.ID + `","name":"Box","url":"http://box.lan:7380"}]}`; string(b) != want {
		t.Errorf("file %s\nwant %s", b, want)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if a := s2.Active(); a.ID != box.ID || a.URL != box.URL {
		t.Fatalf("reopened active %+v", a)
	}
	ents, _ := os.ReadDir(filepath.Dir(path))
	if len(ents) != 1 {
		t.Errorf("leftover files: %v", ents)
	}
}

func TestStoreEdits(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "servers.json"))
	a, err := s.Add("A", "http://10.0.0.1:7380")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, url string }{
		{"", "http://10.0.0.2:7380"},
		{"a", "http://10.0.0.2:7380"},                // name taken (case-insensitive)
		{"This Machine", "http://10.0.0.2:7380"},     // the local label
		{"B", "http://10.0.0.1:7380/"},               // URL taken
		{"B", "10.0.0.2:7380"},                       // no scheme
		{strings.Repeat("x", 61), "http://10.0.0.2"}, // too long
	} {
		if _, err := s.Add(c.name, c.url); err == nil {
			t.Errorf("Add(%q, %q) accepted", c.name, c.url)
		}
	}
	if _, err := s.Update(a.ID, "a", "http://10.0.0.1:7380"); err != nil {
		t.Errorf("rename to its own name in another case: %v", err)
	}
	u, err := s.Update(a.ID, "Alpha", "http://10.0.0.9:7380")
	if err != nil || u.Name != "Alpha" || u.URL != "http://10.0.0.9:7380" || u.ID != a.ID {
		t.Fatalf("Update = %+v, %v", u, err)
	}
	if _, err := s.Update(LocalID, "x", "http://10.0.0.3"); !errors.Is(err, ErrLocal) {
		t.Errorf("Update local = %v", err)
	}
	if err := s.Remove(LocalID); !errors.Is(err, ErrLocal) {
		t.Errorf("Remove local = %v", err)
	}
	if err := s.SetActive("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetActive unknown = %v", err)
	}
	if err := s.SetActive(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(a.ID); err != nil {
		t.Fatal(err)
	}
	if s.Active().ID != LocalID || len(s.List()) != 1 {
		t.Errorf("after removing the active server: active %+v list %+v", s.Active(), s.List())
	}
	if err := s.Remove(a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Remove twice = %v", err)
	}
}

func TestStoreCorrupt(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"garbage": "{nope",
		"version": `{"version":2,"active":"local","servers":[]}`,
	} {
		path := filepath.Join(dir, name+".json")
		_ = os.WriteFile(path, []byte(body), 0o600)
		s, err := Open(path)
		if err == nil {
			t.Errorf("%s: no error", name)
		}
		if l := s.List(); len(l) != 1 || s.Active().ID != LocalID {
			t.Errorf("%s: %+v", name, l)
		}
	}
	// Invalid entries are dropped, the rest kept; an unknown active falls
	// back to local.
	path := filepath.Join(dir, "partial.json")
	_ = os.WriteFile(path, []byte(`{"version":1,"active":"s-gone","servers":[
		{"id":"s-1","name":"One","url":"http://10.0.0.1:7380"},
		{"id":"local","name":"Dup","url":"http://10.0.0.2"},
		{"id":"s/2","name":"Slash","url":"http://10.0.0.3"},
		{"id":"s-3","name":"Bad","url":"nope"}]}`), 0o600)
	s, err := Open(path)
	if err == nil {
		t.Error("no error for dropped entries")
	}
	if l := s.List(); len(l) != 2 || l[1].ID != "s-1" || s.Active().ID != LocalID {
		t.Errorf("partial: %+v active %+v", l, s.Active())
	}
}
