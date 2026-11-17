package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/filelock"
	"github.com/flexdinesh/stage/server/internal/listen"
)

// STAGE_URL takes the same unix syntax as STAGE_LISTEN: a relative socket
// path is an error, never dialed.
func TestNewUnixURL(t *testing.T) {
	for _, u := range []string{"unix://run/stage.sock", "unix:rel.sock", "unix:"} {
		_, err := New(u).Projects(context.Background())
		if err == nil || !strings.Contains(err.Error(), "absolute") {
			t.Errorf("%s: err %v, want absolute-path error", u, err)
		}
	}
	for _, u := range []string{"unix:///run/stage.sock", "unix:/run/stage.sock"} {
		if c := New(u); c.err != nil || c.base != socketBase {
			t.Errorf("%s: %+v", u, c)
		}
	}
}

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestResolve(t *testing.T) {
	short, err := os.MkdirTemp("", "res")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(short)
	env := map[string]string{"HOME": t.TempDir(), "XDG_RUNTIME_DIR": short, "STAGE_DATA_DIR": t.TempDir()}
	getenv := envOf(env)
	if r := Resolve("", getenv); r.URL != DefaultURL || r.Source != "default" {
		t.Fatalf("no server: %+v", r)
	}
	sock, err := config.DefaultSocket(getenv, env["STAGE_DATA_DIR"])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	// A stale socket and lock file (crashed server): not used.
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(listen.LockPath(sock), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if r := Resolve("", getenv); r.URL != DefaultURL {
		t.Fatalf("stale socket used: %+v", r)
	}
	l, err := filelock.Acquire(listen.LockPath(sock), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	if r := Resolve("", getenv); r.URL != "unix://"+sock || r.Source != "default socket" {
		t.Fatalf("live socket: %+v", r)
	}
	env["STAGE_URL"] = "http://stage.lan:7380"
	if r := Resolve("", getenv); r.URL != "http://stage.lan:7380" || r.Source != "STAGE_URL" {
		t.Fatalf("STAGE_URL: %+v", r)
	}
	if r := Resolve("unix:///x.sock", getenv); r.URL != "unix:///x.sock" || r.Source != "--url" {
		t.Fatalf("--url: %+v", r)
	}
}

func TestAPIVersionGate(t *testing.T) {
	version := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if version != "" {
			w.Header().Set(buildinfo.APIVersionHeader, version)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"projects":[]}`))
	}))
	defer srv.Close()
	c := New(srv.URL)
	for _, v := range []string{"", "3"} { // older server without the header; same version
		version = v
		if _, err := c.Projects(context.Background()); err != nil {
			t.Fatalf("version %q: %v", v, err)
		}
	}
	version = "1"
	_, err := c.Projects(context.Background())
	var ve *VersionError
	if !errors.As(err, &ve) || ve.Server != "1" || !strings.Contains(err.Error(), "API v1, this client v3") {
		t.Fatalf("mismatch: %v", err)
	}
}

func TestHealthRecognizesStage(t *testing.T) {
	body, status := "", 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	c := New(srv.URL)
	body = `{"status":"ok","db":"ok","service":"stage","apiVersion":1,"schema":4,"t3":{"connected":false,"lastError":"down"}}`
	h, err := c.Health(context.Background())
	if err != nil || h.Schema != 4 || h.T3.Connected || h.T3.LastError == nil || *h.T3.LastError != "down" {
		t.Fatalf("health %+v, %v", h, err)
	}
	body = `{"status":"ok","db":"ok"}` // server predating the metadata
	if _, err := c.Health(context.Background()); err != nil {
		t.Fatalf("old server: %v", err)
	}
	body, status = `{"status":"error","db":"error","service":"stage"}`, 503
	if h, err := c.Health(context.Background()); !IsStatus(err, 503) || h.DB != "error" {
		t.Fatalf("db down: %+v, %v", h, err)
	}
	body, status = `<html>not stage</html>`, 200
	if _, err := c.Health(context.Background()); err == nil {
		t.Fatal("non-stage answer accepted")
	}
	body = `{"hello":"world"}`
	if _, err := c.Health(context.Background()); err == nil {
		t.Fatal("non-stage JSON accepted")
	}
}
