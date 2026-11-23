package api_test

import (
	"context"
	"encoding/json"
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
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/t3setup"
)

type t3Status struct {
	Connected   bool   `json:"connected"`
	State       string `json:"state"`
	T3URL       string `json:"t3Url"`
	PublicURL   string `json:"publicUrl"`
	T3URLSource string `json:"t3UrlSource"`
	// AdminTokenConfigured: an admin token is saved.
	AdminTokenConfigured bool    `json:"adminTokenConfigured"`
	Reachable            bool    `json:"reachable"`
	CheckedAt            *string `json:"checkedAt"`
	ProbeError           *string `json:"probeError"`
	T3                   *struct {
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

// UnmarshalJSON reads the details of GET /api/harnesses/t3/status.
func (s *t3Status) UnmarshalJSON(b []byte) error {
	var w struct {
		Details json.RawMessage `json:"details"`
	}
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	type plain t3Status
	return json.Unmarshal(w.Details, (*plain)(s))
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
		a, _ := d.Sync.Adapter(d.Store.LegacyT3ID())
		client := a.(*t3.Adapter).Client
		d.Setup = t3setup.New(t3setup.Options{
			Config: cfg, Settings: d.Store.OnMachine(db.LocalMachine), Identity: machine.NewStorePorts(d.Store.OnMachine(db.LocalMachine), harness.MustRegistry(t3.Descriptor)), Client: client, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			AdminTokens: d.Store.OnMachine(db.LocalMachine),
			OnPaired:    func() { s.paired.Add(1) },
			Local: &t3setup.LocalCLI{
				Bin: cfg.T3Bin,
				Exec: func(context.Context, string, []string) ([]byte, error) {
					return []byte(s.runOut), s.runErr
				},
				LookPath: func(file string) (string, error) {
					if s.cli {
						return "/usr/bin/t3", nil
					}
					return "", errors.New("not found")
				},
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
	e.must("GET", "/api/harnesses/t3/status", nil, 200, &st)
	if !st.Connected || st.State != "connected" || st.T3URL != "http://127.0.0.1:3773" || st.PublicURL != st.T3URL ||
		st.T3URLSource != "default" || !st.Reachable || st.CheckedAt == nil || st.ProbeError != nil ||
		st.T3 == nil || st.T3.EnvironmentID != "fake-env" || st.T3.Label != "fake" || st.T3.ServerVersion != "fake" ||
		st.AutoPair.Available || st.AutoPair.Reason == nil || *st.AutoPair.Reason != "fake" ||
		st.Sync == nil || st.Sync.Running || st.Sync.StartedAt != nil {
		t.Fatalf("status %+v", st)
	}
	e.fake.SetProbe(t3.Probe{CheckedAt: time.Now(), Err: "connection refused"})
	e.must("GET", "/api/harnesses/t3/status", nil, 200, &st)
	if st.State != "unreachable" || st.Reachable || st.T3 != nil || st.ProbeError == nil || *st.ProbeError != "connection refused" {
		t.Fatalf("unreachable %+v", st)
	}
	// Health keeps its meaning: a valid token, regardless of the probe.
	var h api.Health
	e.must("GET", "/api/health", nil, 200, &h)
	if len(h.Harnesses) != 1 || !h.Harnesses[0].Connected {
		t.Fatalf("health %+v", h)
	}
}

func TestT3AutoPairEndpoint(t *testing.T) {
	s := &setupT3{}
	e := newEnv(t, s.opt(config.Config{}))
	var st t3Status
	e.must("GET", "/api/harnesses/t3/status", nil, 200, &st)
	if st.AutoPair.Available || st.AutoPair.Reason == nil || *st.AutoPair.Reason != "cli_not_found" {
		t.Fatalf("no cli %+v", st.AutoPair)
	}
	e.must("POST", "/api/harnesses/t3/actions/auto-pair", nil, http.StatusConflict, nil)

	s.cli = true
	s.runErr = errors.New("exit status 1: boom")
	var eb struct {
		Error string `json:"error"`
	}
	e.must("POST", "/api/harnesses/t3/actions/auto-pair", nil, http.StatusBadGateway, &eb)
	if !strings.Contains(eb.Error, "boom") {
		t.Fatalf("502 body %q", eb.Error)
	}
	s.runErr, s.runOut = nil, `{"id":"1","credential":"PAIR-9","scopes":[],"expiresAt":"2026-11-17T10:00:00Z"}`
	e.must("POST", "/api/harnesses/t3/actions/auto-pair", nil, 200, &st)
	if st.State != "connected" || s.paired.Load() != 1 || len(e.fake.Pairs) != 1 || e.fake.Pairs[0] != "PAIR-9" {
		t.Fatalf("auto pair %+v, kicks %d, pairs %v", st, s.paired.Load(), e.fake.Pairs)
	}

	remote := &setupT3{cli: true}
	e = newEnv(t, remote.opt(config.Config{}))
	e.must("PUT", "/api/harnesses/t3/settings", map[string]any{"url": "http://t3.lan:3773"}, 200, nil)
	e.must("POST", "/api/harnesses/t3/actions/auto-pair", nil, http.StatusConflict, &eb)
	if !strings.Contains(eb.Error, "not a loopback address") {
		t.Fatalf("remote 409 %q", eb.Error)
	}
}

func TestT3PairKicksSync(t *testing.T) {
	s := &setupT3{}
	e := newEnv(t, s.opt(config.Config{}))
	e.must("POST", "/api/harnesses/t3/actions/pair", map[string]any{"code": " "}, 400, nil)
	e.must("POST", "/api/harnesses/t3/actions/pair", map[string]any{"code": "ABCD-1234"}, 200, nil)
	if s.paired.Load() != 1 {
		t.Fatalf("pair kicked %d syncs", s.paired.Load())
	}
}

func TestT3UnpairEndpoint(t *testing.T) {
	s := &setupT3{}
	e := newEnv(t, s.opt(config.Config{}))
	var st t3Status
	e.must("GET", "/api/harnesses/t3/status", nil, 200, &st) // binds the connected fake
	s.envOther = true
	e.must("PUT", "/api/harnesses/t3/settings", map[string]any{"url": "http://127.0.0.1:5000"}, http.StatusConflict, nil)

	e.must("DELETE", "/api/harnesses/t3/connection", nil, 200, &st)
	if st.State != "unpaired" || st.Connected {
		t.Fatalf("after disconnect %+v", st)
	}
	// Unbound: a T3 serving another environment is accepted.
	e.must("PUT", "/api/harnesses/t3/settings", map[string]any{"url": "http://127.0.0.1:5000"}, 200, nil)
	e.must("DELETE", "/api/harnesses/t3/connection", nil, 200, &st)

	// A saved admin token pairs T3 (the fake mints a token at once);
	// Disconnect forgets it with the token and the saved URLs, and the T3
	// connection is no longer configured.
	admin := &setupT3{}
	e = newEnv(t, admin.opt(config.Config{}))
	e.must("DELETE", "/api/harnesses/t3/connection", nil, 200, &st)
	if configured(t, e, "t3") {
		t.Fatal("unpaired T3 is configured")
	}
	e.must("PUT", "/api/harnesses/t3/settings", map[string]any{"adminToken": "admin", "publicUrl": "http://t3.example"}, 200, &st)
	if st.State != "connected" || !st.AdminTokenConfigured || st.PublicURL != "http://t3.example" || e.fake.AdminToken != "admin" || !configured(t, e, "t3") {
		t.Fatalf("admin token %+v (fake %q)", st, e.fake.AdminToken)
	}
	e.must("DELETE", "/api/harnesses/t3/connection", nil, 200, &st)
	if st.State != "unpaired" || st.AdminTokenConfigured || st.PublicURL != st.T3URL || st.T3URLSource != "default" || e.fake.AdminToken != "" || configured(t, e, "t3") {
		t.Fatalf("after disconnect %+v", st)
	}
}

// configured reports GET /api/connections' configured for the local
// machine's active connection of kind.
func configured(t *testing.T, e *env, kind string) bool {
	t.Helper()
	var out struct {
		Connections []struct {
			Harness    string `json:"harness"`
			MachineID  int64  `json:"machineId"`
			Active     bool   `json:"active"`
			Configured bool   `json:"configured"`
		} `json:"connections"`
	}
	e.must("GET", "/api/connections", nil, 200, &out)
	for _, c := range out.Connections {
		if c.Harness == kind && c.MachineID == db.LocalMachine && c.Active {
			return c.Configured
		}
	}
	return false
}

func TestT3URLEndpoint(t *testing.T) {
	s := &setupT3{}
	e := newEnv(t, s.opt(config.Config{}))
	var eb struct {
		Error string `json:"error"`
	}
	for _, body := range []any{map[string]any{}, map[string]any{"url": "nope"}, map[string]any{"url": "http://h/x"}, map[string]any{"url": 3}, map[string]any{"url": "http://h", "extra": 1}} {
		e.must("PUT", "/api/harnesses/t3/settings", body, 400, nil)
	}
	var st t3Status
	e.must("PUT", "/api/harnesses/t3/settings", map[string]any{"url": "http://localhost:4000/"}, 200, &st)
	if st.T3URL != "http://localhost:4000" || st.PublicURL != "http://localhost:4000" || st.T3URLSource != "settings" || e.fake.BaseURL != "http://localhost:4000" {
		t.Fatalf("set %+v (client %q)", st, e.fake.BaseURL)
	}
	// Links follow the new public URL.
	e.must("GET", "/api/refs/resolve?ref=http://localhost:4000/fake-env/thread-x", nil, 404, nil)

	// The fake is paired with an identity: while paired, a different
	// environment is refused until Disconnect.
	s.envOther = true
	e.must("PUT", "/api/harnesses/t3/settings", map[string]any{"url": "http://127.0.0.1:5000"}, http.StatusConflict, &eb)
	if eb.Error != "This stage server is paired with T3 'fake'. Disconnect it first, then change the URL and pair the other T3; its tasks stay here, read-only." {
		t.Fatalf("409 %q", eb.Error)
	}
	s.envOther = false
	e.must("PUT", "/api/harnesses/t3/settings", map[string]any{"url": nil}, 200, &st)
	if st.T3URL != "http://127.0.0.1:3773" || st.T3URLSource != "default" {
		t.Fatalf("reset %+v", st)
	}

	// The browser URL: set, invalid, cleared (it follows the T3 URL).
	e.must("PUT", "/api/harnesses/t3/settings", map[string]any{"publicUrl": "http://h/x"}, 400, nil)
	e.must("PUT", "/api/harnesses/t3/settings", map[string]any{"publicUrl": "https://t3.example/"}, 200, &st)
	if st.PublicURL != "https://t3.example" || st.T3URL != "http://127.0.0.1:3773" || e.fake.PublicURL != "https://t3.example" {
		t.Fatalf("browser URL %+v", st)
	}
	e.must("PUT", "/api/harnesses/t3/settings", map[string]any{"publicUrl": nil}, 200, &st)
	if st.PublicURL != st.T3URL {
		t.Fatalf("cleared browser URL %+v", st)
	}
}

func TestT3StatusReportsSyncProgress(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/p")
	e.task(p.ID, "sync target", map[string]any{"start": true})
	gate, reached := make(chan struct{}), make(chan struct{}, 1)
	e.t3.listGate.Store(&listGate{gate: gate, reached: reached})
	done := make(chan error, 1)
	go func() { _, err := e.sync.FullSync(context.Background()); done <- err }()
	<-reached
	var st t3Status
	e.must("GET", "/api/harnesses/t3/status", nil, 200, &st)
	if st.Sync == nil || !st.Sync.Running || st.Sync.StartedAt == nil || st.Sync.Projects != 1 || st.Sync.Threads != 0 {
		t.Fatalf("running sync %+v", st.Sync)
	}
	close(gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	e.must("GET", "/api/harnesses/t3/status", nil, 200, &st)
	if st.Sync.Running {
		t.Fatalf("finished sync %+v", st.Sync)
	}
}
