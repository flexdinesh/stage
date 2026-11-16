package api_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/t3setup"
)

type t3Status struct {
	Connected   bool    `json:"connected"`
	State       string  `json:"state"`
	T3URL       string  `json:"t3Url"`
	PublicURL   string  `json:"publicUrl"`
	T3URLSource string  `json:"t3UrlSource"`
	Reachable   bool    `json:"reachable"`
	CheckedAt   *string `json:"checkedAt"`
	ProbeError  *string `json:"probeError"`
	T3          *struct {
		EnvironmentID string `json:"environmentId"`
		Label         string `json:"label"`
		ServerVersion string `json:"serverVersion"`
	} `json:"t3"`
	AutoPair struct {
		Available bool    `json:"available"`
		Reason    *string `json:"reason"`
	} `json:"autoPair"`
	Sync *struct {
		Running   bool    `json:"running"`
		StartedAt *string `json:"startedAt"`
		Projects  int     `json:"projects"`
		Threads   int     `json:"threads"`
	} `json:"sync"`
}

// setupT3 replaces the T3 setup with one whose CLI, PATH and candidate
// probes are scripted.
type setupT3 struct {
	runOut   string
	runErr   error
	cli      bool
	paired   atomic.Int32
	envOther bool // candidate URLs serve another environment
}

func (s *setupT3) opt(cfg config.Config) func(*api.Deps) {
	return func(d *api.Deps) {
		d.Config = cfg
		d.Setup = t3setup.New(t3setup.Options{
			Config: cfg, Store: d.Store, Client: d.T3, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			OnPaired: func() { s.paired.Add(1) },
			Run: func(context.Context, string, []string) ([]byte, error) {
				return []byte(s.runOut), s.runErr
			},
			LookPath: func(file string) (string, error) {
				if s.cli {
					return "/usr/bin/t3", nil
				}
				return "", errors.New("not found")
			},
			ProbeURL: func(context.Context, string) t3.Probe {
				env := t3.FakeEnvironmentID
				if s.envOther {
					env = "other-env"
				}
				return t3.Probe{Reachable: true, CheckedAt: time.Now(), Identity: &t3.Identity{EnvironmentID: env, Label: env}}
			},
		})
	}
}

func TestT3StatusShape(t *testing.T) {
	e := newEnv(t)
	var st t3Status
	e.must("GET", "/api/t3/status", nil, 200, &st)
	if !st.Connected || st.State != "connected" || st.T3URL != "http://127.0.0.1:3773" || st.PublicURL != publicURL ||
		st.T3URLSource != "default" || !st.Reachable || st.CheckedAt == nil || st.ProbeError != nil ||
		st.T3 == nil || st.T3.EnvironmentID != "fake-env" || st.T3.Label != "fake" || st.T3.ServerVersion != "fake" ||
		st.AutoPair.Available || st.AutoPair.Reason == nil || *st.AutoPair.Reason != "fake" ||
		st.Sync == nil || st.Sync.Running || st.Sync.StartedAt != nil {
		t.Fatalf("status %+v", st)
	}
	e.fake.SetProbe(t3.Probe{CheckedAt: time.Now(), Err: "connection refused"})
	e.must("GET", "/api/t3/status", nil, 200, &st)
	if st.State != "unreachable" || st.Reachable || st.T3 != nil || st.ProbeError == nil || *st.ProbeError != "connection refused" {
		t.Fatalf("unreachable %+v", st)
	}
	// Health keeps its meaning: a valid token, regardless of the probe.
	var h struct {
		T3 struct {
			Connected bool `json:"connected"`
		} `json:"t3"`
	}
	e.must("GET", "/api/health", nil, 200, &h)
	if !h.T3.Connected {
		t.Fatalf("health %+v", h)
	}
}

func TestT3AutoPairEndpoint(t *testing.T) {
	s := &setupT3{}
	e := newEnv(t, s.opt(config.Config{T3URL: "http://127.0.0.1:3773", T3PublicURL: publicURL}))
	var st t3Status
	e.must("GET", "/api/t3/status", nil, 200, &st)
	if st.AutoPair.Available || st.AutoPair.Reason == nil || *st.AutoPair.Reason != "cli_not_found" {
		t.Fatalf("no cli %+v", st.AutoPair)
	}
	e.must("POST", "/api/t3/pair/auto", nil, http.StatusConflict, nil)

	s.cli = true
	s.runErr = errors.New("exit status 1: boom")
	var eb struct {
		Error string `json:"error"`
	}
	e.must("POST", "/api/t3/pair/auto", nil, http.StatusBadGateway, &eb)
	if !strings.Contains(eb.Error, "boom") {
		t.Fatalf("502 body %q", eb.Error)
	}
	s.runErr, s.runOut = nil, `{"id":"1","credential":"PAIR-9","scopes":[],"expiresAt":"2026-11-17T10:00:00Z"}`
	e.must("POST", "/api/t3/pair/auto", nil, 200, &st)
	if st.State != "connected" || s.paired.Load() != 1 || len(e.fake.Pairs) != 1 || e.fake.Pairs[0] != "PAIR-9" {
		t.Fatalf("auto pair %+v, kicks %d, pairs %v", st, s.paired.Load(), e.fake.Pairs)
	}

	remote := &setupT3{cli: true}
	e = newEnv(t, remote.opt(config.Config{T3URL: "http://t3.lan:3773", T3URLFromEnv: true}))
	e.must("POST", "/api/t3/pair/auto", nil, http.StatusConflict, &eb)
	if !strings.Contains(eb.Error, "not a loopback address") {
		t.Fatalf("remote 409 %q", eb.Error)
	}
}

func TestT3PairKicksSync(t *testing.T) {
	s := &setupT3{}
	e := newEnv(t, s.opt(config.Config{T3URL: "http://127.0.0.1:3773", T3PublicURL: publicURL}))
	e.must("POST", "/api/t3/pair", map[string]any{"code": " "}, 400, nil)
	e.must("POST", "/api/t3/pair", map[string]any{"code": "ABCD-1234"}, 200, nil)
	if s.paired.Load() != 1 {
		t.Fatalf("pair kicked %d syncs", s.paired.Load())
	}
}

func TestT3URLEndpoint(t *testing.T) {
	s := &setupT3{}
	e := newEnv(t, s.opt(config.Config{T3URL: "http://127.0.0.1:3773", T3PublicURL: "http://127.0.0.1:3773"}))
	var eb struct {
		Error string `json:"error"`
	}
	for _, body := range []any{map[string]any{}, map[string]any{"url": "nope"}, map[string]any{"url": "http://h/x"}, map[string]any{"url": 3}, map[string]any{"url": "http://h", "extra": 1}} {
		e.must("PUT", "/api/t3/url", body, 400, nil)
	}
	var st t3Status
	e.must("PUT", "/api/t3/url", map[string]any{"url": "http://localhost:4000/"}, 200, &st)
	if st.T3URL != "http://localhost:4000" || st.PublicURL != "http://localhost:4000" || st.T3URLSource != "settings" || e.fake.BaseURL != "http://localhost:4000" {
		t.Fatalf("set %+v (client %q)", st, e.fake.BaseURL)
	}
	// Links follow the new public URL.
	e.must("GET", "/api/refs/resolve?ref=http://localhost:4000/fake-env/thread-x", nil, 404, nil)

	// The fake is connected with an identity: the server binds to it, so a
	// different environment is refused.
	s.envOther = true
	e.must("PUT", "/api/t3/url", map[string]any{"url": "http://127.0.0.1:5000"}, http.StatusConflict, &eb)
	if eb.Error != "This stage server is bound to T3 'fake'. Run another stage server for a different T3." {
		t.Fatalf("409 %q", eb.Error)
	}
	s.envOther = false
	e.must("PUT", "/api/t3/url", map[string]any{"url": nil}, 200, &st)
	if st.T3URL != "http://127.0.0.1:3773" || st.T3URLSource != "default" {
		t.Fatalf("reset %+v", st)
	}

	locked := &setupT3{}
	e = newEnv(t, locked.opt(config.Config{T3URL: "http://t3.lan:3773", T3URLFromEnv: true}))
	e.must("PUT", "/api/t3/url", map[string]any{"url": "http://localhost:4000"}, http.StatusConflict, &eb)
	if !strings.Contains(eb.Error, "T3_URL") {
		t.Fatalf("locked 409 %q", eb.Error)
	}
	e.must("GET", "/api/t3/status", nil, 200, &st)
	if st.T3URLSource != "env" || st.T3URL != "http://t3.lan:3773" {
		t.Fatalf("env status %+v", st)
	}
}

func TestT3StatusReportsSyncProgress(t *testing.T) {
	e := newEnv(t)
	e.fake.AddProject("p", "/w/p")
	gate, reached := make(chan struct{}), make(chan struct{}, 1)
	e.t3.listGate.Store(&listGate{gate: gate, reached: reached})
	done := make(chan error, 1)
	go func() { _, err := e.sync.FullSync(context.Background()); done <- err }()
	<-reached
	var st t3Status
	e.must("GET", "/api/t3/status", nil, 200, &st)
	if st.Sync == nil || !st.Sync.Running || st.Sync.StartedAt == nil || st.Sync.Projects != 1 || st.Sync.Threads != 0 {
		t.Fatalf("running sync %+v", st.Sync)
	}
	close(gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	e.must("GET", "/api/t3/status", nil, 200, &st)
	if st.Sync.Running {
		t.Fatalf("finished sync %+v", st.Sync)
	}
}
