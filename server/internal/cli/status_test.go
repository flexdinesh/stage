package cli_test

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestStatusReady(t *testing.T) {
	h := newHarness(t)
	code, out, errOut := h.run("", "status")
	if code != 0 || !strings.Contains(out, "ready at "+h.url+" (STAGE_URL)") || !strings.Contains(out, "t3        connected") || !strings.Contains(out, "api       v4") {
		t.Fatalf("status: %d %q %q", code, out, errOut)
	}
	code, out, _ = h.run("", "status", "--json")
	var s struct {
		URL    string `json:"url"`
		Source string `json:"source"`
		Ready  bool   `json:"ready"`
		Health struct {
			Service    string `json:"service"`
			APIVersion int    `json:"apiVersion"`
		} `json:"health"`
		ClientAPIVersion int `json:"clientApiVersion"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &s) != nil || !s.Ready || s.Source != "STAGE_URL" || s.Health.Service != "stage" || s.Health.APIVersion != 4 || s.ClientAPIVersion != 4 {
		t.Fatalf("status --json: %d %s", code, out)
	}
}

func TestStatusUnreachableAndWait(t *testing.T) {
	h := newHarness(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	real := h.url
	h.url = "http://" + addr
	if code, out, _ := h.run("", "status"); code != 1 || !strings.Contains(out, "not reachable at http://"+addr) {
		t.Fatalf("unreachable: %d %q", code, out)
	}
	start := time.Now()
	if code, _, errOut := h.run("", "status", "--wait", "400ms"); code != 3 || !strings.Contains(errOut, "not ready after 400ms") {
		t.Fatalf("wait timeout: %d %q", code, errOut)
	}
	if d := time.Since(start); d < 400*time.Millisecond {
		t.Fatalf("returned after %v", d)
	}
	// The server comes up while status waits.
	go func() {
		time.Sleep(300 * time.Millisecond)
		l, err := net.Listen("tcp", addr)
		if err != nil {
			return
		}
		srv := &http.Server{Handler: h.handler}
		t.Cleanup(func() { _ = srv.Close() })
		_ = srv.Serve(l)
	}()
	if code, out, errOut := h.run("", "status", "--wait", "10s"); code != 0 || !strings.Contains(out, "ready at") {
		t.Fatalf("wait for start: %d %q %q", code, out, errOut)
	}
	h.url = real
}

func TestStatusAPIMismatch(t *testing.T) {
	h := newHarness(t)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Stage-Api-Version", "1")
		_, _ = w.Write([]byte(`{"status":"ok","db":"ok","service":"stage","version":"9.9.9","apiVersion":1,"t3":{"connected":true}}`))
	})}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(l) }()
	defer srv.Close()
	h.url = "http://" + l.Addr().String()
	code, out, errOut := h.run("", "status", "--wait", "5s")
	if code != 1 || !strings.Contains(errOut, "API v1, this client v4") || !strings.Contains(out, "server version 9.9.9 differs") {
		t.Fatalf("mismatch: %d %q %q", code, out, errOut)
	}
	// Other commands refuse too.
	if code, _, errOut := h.run("", "projects", "ls"); code != 1 || !strings.Contains(errOut, "API v1") {
		t.Fatalf("projects ls on mismatch: %d %q", code, errOut)
	}
}
