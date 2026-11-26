package t3setup_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/t3setup"
)

var testDB dbtest.DB

// client is the fake with a switchable token and counted probes.
type client struct {
	*t3.Fake
	paired atomic.Bool // Status: a valid token
	probes atomic.Int32
}

func (c *client) Status(ctx context.Context) t3.ConnectionStatus {
	st := c.Fake.Status(ctx)
	st.Connected, st.HasToken = c.paired.Load(), c.paired.Load()
	return st
}

func (c *client) Probe(ctx context.Context) t3.Probe {
	c.probes.Add(1)
	return c.Fake.Probe(ctx)
}

func (c *client) Pair(ctx context.Context, code string) error {
	if err := c.Fake.Pair(ctx, code); err != nil {
		return err
	}
	c.paired.Store(true)
	return nil
}

// SetAdminToken: a non-empty admin token mints a token at once.
func (c *client) SetAdminToken(ctx context.Context, token string) error {
	if err := c.Fake.SetAdminToken(ctx, token); err != nil {
		return err
	}
	if token != "" {
		c.paired.Store(true)
	}
	return nil
}

func (c *client) Unpair(ctx context.Context) error {
	if err := c.Fake.Unpair(ctx); err != nil {
		return err
	}
	c.paired.Store(false)
	return nil
}

func reachable(env string) t3.Probe {
	return t3.Probe{Reachable: true, CheckedAt: time.Now(), Identity: &t3.Identity{EnvironmentID: env, Label: "box " + env, ServerVersion: "0.1.9"}}
}

type env struct {
	t      *testing.T
	store  *db.Store
	client *client
	svc    *t3setup.Service
	// candidates: what ProbeURL answers per URL (absent: refused).
	candidates map[string]t3.Probe
	runs       [][]string
	runOut     []byte
	runErr     error
	paths      map[string]bool // executables LookPath finds
	kicks      atomic.Int32
	switches   []db.Switch
}

func newEnv(t *testing.T, cfg config.Config, fake bool) *env {
	t.Helper()
	e := &env{
		t: t, store: db.NewStore(testDB.Reset(t)), client: &client{Fake: t3.NewFake()},
		candidates: map[string]t3.Probe{}, paths: map[string]bool{},
	}
	e.client.SetProbe(reachable("env-a"))
	ctx := context.Background()
	conn, err := e.store.GetConnection(ctx, db.LocalMachine)
	if err != nil {
		t.Fatal(err)
	}
	e.svc = t3setup.New(t3setup.Options{
		Config: cfg, Settings: e.store.OnMachine(db.LocalMachine), Identity: machine.NewStorePorts(e.store.OnMachine(db.LocalMachine), harness.MustRegistry(t3.Descriptor)), Client: e.client, Endpoint: t3setup.Resolve(conn), Fake: fake,
		AdminTokens: e.store.OnMachine(db.LocalMachine),
		OnPaired:    func() { e.kicks.Add(1) },
		OnSwitched:  func(sw db.Switch) { e.switches = append(e.switches, sw) },
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Local: &t3setup.LocalCLI{
			Bin:  cfg.T3Bin,
			Home: "/home/u",
			Exec: func(_ context.Context, bin string, args []string) ([]byte, error) {
				e.runs = append(e.runs, append([]string{bin}, args...))
				return e.runOut, e.runErr
			},
			LookPath: func(file string) (string, error) {
				if e.paths[file] {
					if !strings.Contains(file, "/") {
						return "/usr/bin/" + file, nil
					}
					return file, nil
				}
				return "", errors.New("not found")
			},
		},
		ProbeURL: func(_ context.Context, base string) t3.Probe {
			if p, ok := e.candidates[base]; ok {
				return p
			}
			return t3.Probe{CheckedAt: time.Now(), Err: "connection refused"}
		},
	})
	return e
}

func defaultCfg() config.Config {
	return config.Config{T3ClientName: "stage"}
}

func (e *env) bound() string {
	e.t.Helper()
	c, err := e.store.GetConnection(context.Background(), db.LocalMachine)
	if err != nil {
		e.t.Fatal(err)
	}
	if c.EnvironmentID == nil {
		return ""
	}
	return *c.EnvironmentID
}

func isConflict(err error, contains string) bool {
	var c *db.ConflictError
	return errors.As(err, &c) && strings.Contains(c.Msg, contains)
}

func TestResolve(t *testing.T) {
	saved, public, empty := "http://127.0.0.1:4000", "https://t3.example", ""
	for _, tc := range []struct {
		name string
		conn db.Connection
		want t3setup.Endpoint
	}{
		{"default", db.Connection{}, t3setup.Endpoint{URL: config.DefaultT3URL, PublicURL: config.DefaultT3URL, Source: "default"}},
		{"empty", db.Connection{T3URL: &empty, T3PublicURL: &empty}, t3setup.Endpoint{URL: config.DefaultT3URL, PublicURL: config.DefaultT3URL, Source: "default"}},
		{"settings", db.Connection{T3URL: &saved}, t3setup.Endpoint{URL: saved, PublicURL: saved, Source: "settings"}},
		{"browser URL", db.Connection{T3URL: &saved, T3PublicURL: &public}, t3setup.Endpoint{URL: saved, PublicURL: public, Source: "settings", PublicSet: true}},
		{"browser URL, default T3 URL", db.Connection{T3PublicURL: &public}, t3setup.Endpoint{URL: config.DefaultT3URL, PublicURL: public, Source: "default", PublicSet: true}},
	} {
		if got := t3setup.Resolve(tc.conn); got != tc.want {
			t.Errorf("%s: %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestNormalizeURL(t *testing.T) {
	for in, want := range map[string]string{
		" http://127.0.0.1:3773/ ": "http://127.0.0.1:3773",
		"HTTPS://t3.example.com":   "https://t3.example.com",
		"http://[::1]:3773":        "http://[::1]:3773",
	} {
		if got, err := t3setup.NormalizeURL(in); err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "127.0.0.1:3773", "ftp://h", "http://", "http://h/t3", "http://h?x=1", "http://u:p@h", "http://h#f", "localhost"} {
		if got, err := t3setup.NormalizeURL(in); err == nil {
			t.Errorf("%q accepted as %q", in, got)
		}
	}
}

func TestIsLoopbackURL(t *testing.T) {
	for u, want := range map[string]bool{
		"http://localhost:3773": true, "http://LOCALHOST": true, "http://127.0.0.1:3773": true, "http://127.8.9.10": true,
		"http://[::1]:3773": true, "http://192.168.1.5:3773": false, "http://t3.lan": false, "http://0.0.0.0:3773": false,
		"http://localhost.example.com": false,
	} {
		if got := t3setup.IsLoopbackURL(u); got != want {
			t.Errorf("%s: %v, want %v", u, got, want)
		}
	}
}

func TestAutoPairAvailability(t *testing.T) {
	ctx := context.Background()
	reason := func(e *env) (bool, string) {
		ap := e.svc.Status(ctx, false).AutoPair
		return ap.Available, ap.Reason
	}
	e := newEnv(t, defaultCfg(), true)
	e.paths["t3"] = true
	if ok, r := reason(e); ok || r != "fake" {
		t.Fatalf("fake: %v %q", ok, r)
	}

	e = newEnv(t, defaultCfg(), false)
	if err := e.svc.SetURL(ctx, ptr("http://192.168.1.5:3773")); err != nil {
		t.Fatal(err)
	}
	e.paths["t3"] = true
	if ok, r := reason(e); ok || r != "not_local" {
		t.Fatalf("remote: %v %q", ok, r)
	}

	e = newEnv(t, defaultCfg(), false)
	if ok, r := reason(e); ok || r != "cli_not_found" {
		t.Fatalf("no cli: %v %q", ok, r)
	}
	for _, p := range []string{"/home/u/.bun/bin/t3", "/opt/homebrew/bin/t3", "t3"} {
		e.paths = map[string]bool{p: true}
		if ok, r := reason(e); !ok || r != "" {
			t.Fatalf("cli at %s: %v %q", p, ok, r)
		}
	}

	// T3_BIN is the only candidate when set.
	cfg := defaultCfg()
	cfg.T3Bin = "/opt/t3/t3"
	e = newEnv(t, cfg, false)
	e.paths["t3"] = true
	if ok, r := reason(e); ok || r != "cli_not_found" {
		t.Fatalf("missing T3_BIN: %v %q", ok, r)
	}
	e.paths["/opt/t3/t3"] = true
	if ap := e.svc.Status(ctx, false).AutoPair; !ap.Available || ap.Bin != "/opt/t3/t3" {
		t.Fatalf("T3_BIN: %+v", ap)
	}
}

func TestAutoPairMintsAndRedeems(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, defaultCfg(), false)
	e.paths["/home/u/.local/bin/t3"] = true
	e.runOut = []byte(`{"id":"p1","credential":"PAIR-123","label":"stage mcp","scopes":["orchestration:read"],"expiresAt":"2026-11-17T10:05:00Z"}` + "\n")
	if _, err := e.svc.AutoPair(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{"/home/u/.local/bin/t3", "auth", "pairing", "create", "--json", "--label", "stage mcp", "--ttl", "5m",
		"--scope", "orchestration:read", "--scope", "orchestration:operate", "--scope", "settings:write", "--scope", "source-control:write"}
	if len(e.runs) != 1 || !slices.Equal(e.runs[0], want) {
		t.Fatalf("ran %q", e.runs)
	}
	if !slices.Equal(e.client.Pairs, []string{"PAIR-123"}) || e.bound() != "env-a" || e.kicks.Load() != 1 {
		t.Fatalf("pairs %v bound %q kicks %d", e.client.Pairs, e.bound(), e.kicks.Load())
	}
	if st := e.svc.Status(ctx, true); st.State != t3.StateConnected {
		t.Fatalf("state %s", st.State)
	}
}

func TestAutoPairFailures(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, defaultCfg(), false)
	if _, err := e.svc.AutoPair(ctx); !isConflict(err, "t3 CLI, which was not found") || len(e.runs) != 0 {
		t.Fatalf("unavailable: %v", err)
	}
	e.paths["t3"] = true
	// T3 not answering: nothing is minted.
	e.client.SetProbe(t3.Probe{CheckedAt: time.Now(), Err: "connection refused"})
	if _, err := e.svc.AutoPair(ctx); err == nil || !strings.Contains(err.Error(), "no T3 server answered at http://127.0.0.1:3773 (connection refused)") || len(e.runs) != 0 {
		t.Fatalf("unreachable: %v, ran %q", err, e.runs)
	}
	e.client.SetProbe(reachable("env-a"))
	for _, tc := range []struct {
		out  string
		err  error
		want string
	}{
		{"", errors.New("exit status 1: no T3 home"), "t3 auth pairing create failed: exit status 1: no T3 home"},
		{"Pairing token issued", nil, "unexpected output: Pairing token issued"},
		{`{"id":"p1"}`, nil, "returned no credential"},
	} {
		e.runOut, e.runErr = []byte(tc.out), tc.err
		_, err := e.svc.AutoPair(ctx)
		var te *db.T3Error
		if !errors.As(err, &te) || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%q/%v: %v", tc.out, tc.err, err)
		}
	}
	if len(e.client.Pairs) != 0 || e.bound() != "" || e.kicks.Load() != 0 {
		t.Fatalf("failed auto-pair paired: %v %q %d", e.client.Pairs, e.bound(), e.kicks.Load())
	}
}

// bind gives the active T3 connection a relation, so replacing its
// instance retires it instead of restamping it.
func (e *env) bind(workspace string) {
	e.t.Helper()
	_, err := e.store.InsertProject(context.Background(), db.Project{Title: workspace, Bindings: []db.ProjectBinding{{ConnectionID: e.store.ActiveID(db.LocalMachine, "t3"), ExternalProjectID: "p-" + workspace, Workspace: workspace}}})
	if err != nil {
		e.t.Fatal(err)
	}
}

func TestPairRecordsEnvironment(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, defaultCfg(), false)
	var te *db.T3Error
	if _, err := e.svc.Pair(ctx, " "); !errors.As(err, &te) || !strings.Contains(err.Error(), "pairing failed: empty pairing code") {
		t.Fatalf("bad code: %v", err)
	}
	first := e.store.ActiveID(db.LocalMachine, "t3")
	paired, err := e.svc.Pair(ctx, "CODE-1")
	if err != nil || paired.Replaced != nil {
		t.Fatalf("pair: %v %+v", err, paired)
	}
	if e.bound() != "env-a" || e.kicks.Load() != 1 || e.store.ActiveID(db.LocalMachine, "t3") != first || len(e.switches) != 0 {
		t.Fatalf("bound %q kicks %d id %d switches %v", e.bound(), e.kicks.Load(), e.store.ActiveID(db.LocalMachine, "t3"), e.switches)
	}
	// Same T3 again: same connection.
	if paired, err := e.svc.Pair(ctx, "CODE-2"); err != nil || paired.Replaced != nil || e.bound() != "env-a" || e.store.ActiveID(db.LocalMachine, "t3") != first {
		t.Fatalf("re-pair: %v %+v %q", err, paired, e.bound())
	}
}

func TestPairingAnotherEnvironmentReplacesTheConnection(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, defaultCfg(), false)
	if _, err := e.svc.Pair(ctx, "CODE-1"); err != nil {
		t.Fatal(err)
	}
	first := e.store.ActiveID(db.LocalMachine, "t3")
	e.bind("/w/a")
	if err := e.svc.Unpair(ctx); err != nil {
		t.Fatal(err)
	}
	// Disconnect keeps the environment: the connection is unchanged.
	if e.bound() != "env-a" || e.store.ActiveID(db.LocalMachine, "t3") != first {
		t.Fatalf("unpair changed the connection: %q %d", e.bound(), e.store.ActiveID(db.LocalMachine, "t3"))
	}
	e.client.SetProbe(reachable("env-b"))
	paired, err := e.svc.Pair(ctx, "CODE-2")
	if err != nil {
		t.Fatal(err)
	}
	second := e.store.ActiveID(db.LocalMachine, "t3")
	if second == first || e.bound() != "env-b" {
		t.Fatalf("not replaced: %d -> %d bound %q", first, second, e.bound())
	}
	if paired.Replaced == nil || paired.Replaced.ID != first || paired.Replaced.Active || paired.Replaced.Label != "box env-a" || paired.Replaced.Bindings != 1 {
		t.Fatalf("replaced %+v", paired.Replaced)
	}
	if len(e.switches) != 1 || e.switches[0] != (db.Switch{Old: first, New: second, Changed: true}) {
		t.Fatalf("switches %+v", e.switches)
	}
	// The old binding stays on the old connection; nothing is bound to env-b.
	p, err := e.store.ListProjects(ctx, "all")
	if err != nil || len(p) != 1 || len(p[0].Bindings) != 1 || p[0].Bindings[0].ConnectionID != first || p[0].Bindings[0].ConnectionActive {
		t.Fatalf("bindings %+v %v", p, err)
	}
	// env-a paired again: its connection comes back with its binding.
	if err := e.svc.Unpair(ctx); err != nil {
		t.Fatal(err)
	}
	e.client.SetProbe(reachable("env-a"))
	paired, err = e.svc.Pair(ctx, "CODE-3")
	if err != nil {
		t.Fatal(err)
	}
	if e.store.ActiveID(db.LocalMachine, "t3") != first || len(e.switches) != 2 || !e.switches[1].Revived {
		t.Fatalf("not revived: %d %+v", e.store.ActiveID(db.LocalMachine, "t3"), e.switches)
	}
	// env-b held nothing: deleted rather than kept as a replaced connection.
	if paired.Replaced != nil {
		t.Fatalf("empty connection reported as replaced: %+v", paired.Replaced)
	}
	if p, _ := e.store.ListProjects(ctx, "all"); !p[0].Bindings[0].ConnectionActive {
		t.Fatal("revived binding still inactive")
	}
}

func TestPairingAnotherEnvironmentRestampsAnEmptyConnection(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, defaultCfg(), false)
	if _, err := e.svc.Pair(ctx, "CODE-1"); err != nil {
		t.Fatal(err)
	}
	first := e.store.ActiveID(db.LocalMachine, "t3")
	if err := e.svc.Unpair(ctx); err != nil {
		t.Fatal(err)
	}
	e.client.SetProbe(reachable("env-b"))
	paired, err := e.svc.Pair(ctx, "CODE-2")
	if err != nil || paired.Replaced != nil || e.bound() != "env-b" || e.store.ActiveID(db.LocalMachine, "t3") != first || len(e.switches) != 0 {
		t.Fatalf("restamp: %v %+v %q %d %v", err, paired, e.bound(), e.store.ActiveID(db.LocalMachine, "t3"), e.switches)
	}
	if err := e.svc.Unpair(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Unpair(ctx); err != nil {
		t.Fatalf("idempotent unpair: %v", err)
	}
}

// A saved admin token pairs T3 (it mints a token) and Disconnect forgets it
// with the token and the saved URLs, back to the defaults.
func TestAdminTokenAndDisconnect(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, defaultCfg(), false)
	conn := t3setup.NewConnector(e.svc, nil)
	if conn.Summary(ctx).Configured {
		t.Fatal("a fresh T3 is configured")
	}
	if err := e.svc.SetURL(ctx, ptr("http://127.0.0.1:4000")); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetPublicURL(ctx, ptr("https://t3.example")); err != nil {
		t.Fatal(err)
	}
	if conn.Summary(ctx).Configured {
		t.Fatal("a saved URL alone made T3 configured")
	}
	if err := e.svc.SetAdminToken(ctx, " admin "); err != nil {
		t.Fatal(err)
	}
	if tok, _ := e.store.LoadT3AdminToken(ctx, db.LocalMachine); tok != "admin" || e.client.AdminToken != "admin" || !e.svc.AdminTokenSet() {
		t.Fatalf("admin token saved %q, client %q", tok, e.client.AdminToken)
	}
	if !conn.Summary(ctx).Configured || e.bound() != "env-a" || e.kicks.Load() != 1 {
		t.Fatalf("admin token did not pair: %+v bound %q kicks %d", conn.Summary(ctx), e.bound(), e.kicks.Load())
	}
	st, err := conn.Status(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range st.Fields {
		if f.ID == "adminToken" && (f.Value != "" || !f.Set) {
			t.Fatalf("admin token field %+v", f)
		}
	}
	if err := conn.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if conn.Summary(ctx).Configured || e.svc.AdminTokenSet() || e.client.AdminToken != "" {
		t.Fatalf("disconnected T3 still configured: %+v", conn.Summary(ctx))
	}
	if tok, _ := e.store.LoadT3AdminToken(ctx, db.LocalMachine); tok != "" {
		t.Fatalf("admin token kept %q", tok)
	}
	c, _ := e.store.GetConnection(ctx, db.LocalMachine)
	want := t3setup.Endpoint{URL: config.DefaultT3URL, PublicURL: config.DefaultT3URL, Source: "default"}
	if c.T3URL != nil || c.T3PublicURL != nil || e.svc.Endpoint() != want || e.client.BaseURL != want.URL || e.client.PublicURL != want.PublicURL {
		t.Fatalf("settings kept: %v %v %+v", c.T3URL, c.T3PublicURL, e.svc.Endpoint())
	}
	// The connection keeps its environment: pairing it again resumes it.
	if e.bound() != "env-a" {
		t.Fatalf("environment lost: %q", e.bound())
	}
}

func TestSetPublicURL(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, defaultCfg(), false)
	var inv *db.InvalidError
	if err := e.svc.SetPublicURL(ctx, ptr("http://h/path")); !errors.As(err, &inv) {
		t.Fatalf("invalid url: %v", err)
	}
	if err := e.svc.SetPublicURL(ctx, ptr("https://t3.example/")); err != nil {
		t.Fatal(err)
	}
	if ep := e.svc.Endpoint(); ep.PublicURL != "https://t3.example" || !ep.PublicSet || e.client.PublicURL != ep.PublicURL || ep.URL != config.DefaultT3URL {
		t.Fatalf("endpoint %+v", ep)
	}
	// A URL change keeps the saved browser URL.
	if err := e.svc.SetURL(ctx, ptr("http://127.0.0.1:4000")); err != nil {
		t.Fatal(err)
	}
	if ep := e.svc.Endpoint(); ep.PublicURL != "https://t3.example" || e.client.PublicURL != ep.PublicURL {
		t.Fatalf("browser URL lost: %+v", ep)
	}
	if c, _ := e.store.GetConnection(ctx, db.LocalMachine); t3setup.Resolve(c) != e.svc.Endpoint() {
		t.Fatalf("restart resolves %+v, want %+v", t3setup.Resolve(c), e.svc.Endpoint())
	}
	// Cleared: it follows the T3 URL again.
	if err := e.svc.SetPublicURL(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if ep := e.svc.Endpoint(); ep.PublicURL != "http://127.0.0.1:4000" || ep.PublicSet || e.client.PublicURL != ep.PublicURL {
		t.Fatalf("cleared %+v", ep)
	}
}

func TestSetURL(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, defaultCfg(), false)
	var inv *db.InvalidError
	if err := e.svc.SetURL(ctx, ptr("http://h/path")); !errors.As(err, &inv) {
		t.Fatalf("invalid url: %v", err)
	}
	// Unbound: any valid URL, even one nothing answers at yet.
	if err := e.svc.SetURL(ctx, ptr("http://127.0.0.1:4000/")); err != nil {
		t.Fatal(err)
	}
	want := t3setup.Endpoint{URL: "http://127.0.0.1:4000", PublicURL: "http://127.0.0.1:4000", Source: "settings"}
	if ep := e.svc.Endpoint(); ep != want || e.client.BaseURL != want.URL || e.client.PublicURL != want.PublicURL {
		t.Fatalf("endpoint %+v, client %q %q", ep, e.client.BaseURL, e.client.PublicURL)
	}
	if c, _ := e.store.GetConnection(ctx, db.LocalMachine); c.T3URL == nil || *c.T3URL != want.URL {
		t.Fatalf("saved %v", c.T3URL)
	}
	// A restart resolves the saved URL.
	if c, _ := e.store.GetConnection(ctx, db.LocalMachine); t3setup.Resolve(c) != want {
		t.Fatal("saved URL not resolved at startup")
	}

	if _, err := e.svc.Pair(ctx, "CODE"); err != nil {
		t.Fatal(err)
	}
	// Bound to env-a: same environment elsewhere is fine.
	e.candidates["http://localhost:4001"] = reachable("env-a")
	if err := e.svc.SetURL(ctx, ptr("http://localhost:4001")); err != nil {
		t.Fatal(err)
	}
	// A different T3 is refused while paired, and nothing changes.
	e.candidates["http://127.0.0.1:5000"] = reachable("env-b")
	if err := e.svc.SetURL(ctx, ptr("http://127.0.0.1:5000")); !isConflict(err, "This stage server is paired with T3 'box env-a'. Disconnect it first, then change the URL and pair the other T3") {
		t.Fatalf("other env: %v", err)
	}
	// Nothing answering cannot be checked.
	if err := e.svc.SetURL(ctx, ptr("http://127.0.0.1:5001")); !isConflict(err, "No T3 answered at http://127.0.0.1:5001 (connection refused)") {
		t.Fatalf("unreachable: %v", err)
	}
	if ep := e.svc.Endpoint(); ep.URL != "http://localhost:4001" || e.client.BaseURL != ep.URL {
		t.Fatalf("refused change applied: %+v %q", ep, e.client.BaseURL)
	}
	// Reset: the default URL, if it serves the bound T3.
	e.candidates[config.DefaultT3URL] = reachable("env-a")
	if err := e.svc.SetURL(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if ep := e.svc.Endpoint(); ep.URL != config.DefaultT3URL || ep.Source != "default" || e.client.BaseURL != config.DefaultT3URL {
		t.Fatalf("reset %+v", ep)
	}
	if c, _ := e.store.GetConnection(ctx, db.LocalMachine); c.T3URL != nil {
		t.Fatalf("reset kept %q", *c.T3URL)
	}
	// Disconnected: the URL may point at another T3 (pairing it replaces).
	if err := e.svc.Unpair(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetURL(ctx, ptr("http://127.0.0.1:5000")); err != nil {
		t.Fatalf("disconnected switch: %v", err)
	}
}

func TestLegacyPairingIsBound(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, defaultCfg(), false)
	e.client.paired.Store(true) // paired before binding existed
	e.candidates["http://127.0.0.1:5000"] = reachable("env-b")
	if err := e.svc.SetURL(ctx, ptr("http://127.0.0.1:5000")); !isConflict(err, "paired with T3 'box env-a'") {
		t.Fatalf("legacy switch: %v", err)
	}
	if e.bound() != "env-a" {
		t.Fatalf("bound %q", e.bound())
	}
}

func TestStatusRefreshesOnlyWhenAsked(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, defaultCfg(), false)
	e.svc.Status(ctx, false)
	if n := e.client.probes.Load(); n != 0 {
		t.Fatalf("memory-only status probed %d times", n)
	}
	st := e.svc.Status(ctx, true)
	if n := e.client.probes.Load(); n != 1 {
		t.Fatalf("refresh probed %d times", n)
	}
	if st.State != t3.StateUnpaired || st.Endpoint.Source != "default" {
		t.Fatalf("status %+v", st)
	}
	e.client.SetProbe(t3.Probe{Err: "connection refused"})
	if st := e.svc.Status(ctx, true); st.State != t3.StateUnreachable || st.Conn.Probe.Err != "connection refused" {
		t.Fatalf("unreachable %+v", st)
	}
}

func TestExecRunnerReportsStderr(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "t3")
	script := "#!/bin/sh\nif [ \"$1\" = ok ]; then echo '{\"credential\":\"C\"}'; else echo 'boom' >&2; exit 3; fi\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := t3setup.ExecRunner(context.Background(), bin, []string{"ok"})
	if err != nil || strings.TrimSpace(string(out)) != `{"credential":"C"}` {
		t.Fatalf("ok: %q %v", out, err)
	}
	if _, err := t3setup.ExecRunner(context.Background(), bin, []string{"fail"}); err == nil || !strings.Contains(err.Error(), "exit status 3: boom") {
		t.Fatalf("fail: %v", err)
	}
}

func ptr(s string) *string { return &s }
