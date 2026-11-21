package opencodesetup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/opencode/opencodetest"
)

type setupHarness struct {
	s         *Service
	store     *db.Store
	installed execution.Adapter
	installs  int
}

func newHarness(t *testing.T, env config.OpenCodeSettings) *setupHarness {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var temp dbtest.DB
	h := &setupHarness{store: db.NewStore(temp.Reset(t))}
	s, err := New(t.Context(), h.store.OnMachine(db.LocalMachine), env, func(_, _ int64, a execution.Adapter) { h.installed = a; h.installs++ }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h.s = s
	return h
}

func register(t *testing.T, url, password string) {
	t.Helper()
	path := filepath.Join(os.Getenv("XDG_STATE_HOME"), "opencode", "service.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"id":"svc","version":"2.3.0","url":%q,"pid":42,"password":%q}`, url, password)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (h *setupHarness) saved(t *testing.T) (config.OpenCodeSettings, string) {
	t.Helper()
	c, verified, err := h.store.OpenCodeSettings(t.Context(), db.LocalMachine)
	if err != nil {
		t.Fatal(err)
	}
	return c, verified
}

func (h *setupHarness) status(t *testing.T) config.OpenCodeStatus {
	t.Helper()
	h.s.forgetLocalStatus()
	return h.s.Status(t.Context())
}

// idle waits for a background refresh or renewal to finish.
func (h *setupHarness) idle() {
	for h.s.busy.Load() {
		time.Sleep(time.Millisecond)
	}
}

func conflictCode(err error) string {
	var c *db.ConflictError
	if errors.As(err, &c) {
		return c.Code
	}
	return ""
}

func TestConnectLocal(t *testing.T) {
	h := newHarness(t, config.OpenCodeSettings{})
	err := h.s.ConnectLocal(t.Context(), false)
	if conflictCode(err) != CodeLocalUnavailable || !strings.Contains(err.Error(), "No OpenCode service file found at") {
		t.Fatalf("missing registration: %v", err)
	}
	if st := h.status(t); st.Local.Reason != "missing" || st.Local.Available || st.Auth != "none" || st.State != "unconfigured" {
		t.Fatalf("%+v", st)
	}
	register(t, "ftp://nope", "pw")
	if err := h.s.ConnectLocal(t.Context(), false); conflictCode(err) != CodeLocalUnavailable || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("invalid registration: %v", err)
	}
	if st := h.status(t); st.Local.Reason != "invalid" {
		t.Fatalf("%+v", st)
	}
	register(t, "http://127.0.0.1:1", "pw")
	if err := h.s.ConnectLocal(t.Context(), false); conflictCode(err) != CodeLocalUnavailable || !strings.Contains(err.Error(), "not answering") {
		t.Fatalf("unreachable service: %v", err)
	}
	if st := h.status(t); st.Local.Reason != "unreachable" || st.Local.URL != "http://127.0.0.1:1" {
		t.Fatalf("%+v", st)
	}

	fake := opencodetest.New(t, "local-secret")
	register(t, fake.URL, "local-secret")
	if err := h.s.ConnectLocal(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	c, verified := h.saved(t)
	if c.Endpoint != fake.URL || verified != fake.URL || c.Auth != "local" || c.Username != "" || c.Password != "" || h.installed == nil {
		t.Fatalf("saved %+v %q", c, verified)
	}
	if fake.Users[len(fake.Users)-1] != "opencode" {
		t.Fatal("local auth username", fake.Users)
	}
	st := h.status(t)
	body, _ := json.Marshal(st)
	if st.State != "connected" || st.Auth != "local" || st.HasCredentials || st.ExpiresAt != nil || !st.Local.Available ||
		st.Local.Version != "2.3.0" || st.Local.Reason != "" || strings.Contains(string(body), "local-secret") {
		t.Fatalf("status %s", body)
	}

	// The service restarted with a new password: Status sees the rejection
	// and a refresh re-reads the registration.
	fake.SetPassword("rotated")
	if st := h.s.Status(t.Context()); st.State != "rejected" {
		t.Fatalf("stale password: %+v", st)
	}
	register(t, fake.URL, "rotated")
	h.s.refreshLocal(t.Context())
	if st := h.s.Status(t.Context()); st.State != "connected" {
		t.Fatalf("after password change: %+v", st)
	}
	if c, _ := h.saved(t); c.Password != "" || c.Auth != "local" {
		t.Fatal("stored the local password", c)
	}

	// The service moved: continuity is checked, then the address saved.
	moved := opencodetest.New(t, "rotated")
	register(t, moved.URL, "rotated")
	h.s.refreshLocal(t.Context())
	if c, verified := h.saved(t); c.Endpoint != moved.URL || verified != moved.URL || c.Auth != "local" {
		t.Fatalf("moved: %+v %q", c, verified)
	}
}

func TestPair(t *testing.T) {
	h := newHarness(t, config.OpenCodeSettings{})
	fake := opencodetest.New(t, "server-password")
	for _, link := range []string{"", fake.URL, fake.URL + "/auth/connect/", fake.URL + "/auth/connect/x?y=1", "http://u:p@host/auth/connect/x"} {
		var invalid *db.InvalidError
		if err := h.s.Pair(t.Context(), link, false); !errors.As(err, &invalid) {
			t.Errorf("%q: %v", link, err)
		}
	}
	link := fake.Link()
	before := time.Now().UTC().Add(-time.Second)
	if err := h.s.Pair(t.Context(), link, false); err != nil {
		t.Fatal(err)
	}
	c, verified := h.saved(t)
	if c.Endpoint != fake.URL || verified != fake.URL || c.Auth != "paired" || c.Username != "opencode" || !fake.Accepts(c.Password) ||
		c.TokenIssuedAt == nil || c.TokenIssuedAt.Before(before) {
		t.Fatalf("saved %+v", c)
	}
	st := h.status(t)
	body, _ := json.Marshal(st)
	if st.State != "connected" || st.Auth != "paired" || !st.HasCredentials || st.ExpiresAt == nil ||
		!st.ExpiresAt.Equal(c.TokenIssuedAt.Add(30*24*time.Hour).Truncate(time.Second)) || strings.Contains(string(body), c.Password) {
		t.Fatalf("status %s", body)
	}
	err := h.s.Pair(t.Context(), link, false)
	if conflictCode(err) != CodePairingExpired || err.Error() != "This pairing link expired or was already used. Run opencode pair for a new one." {
		t.Fatalf("reused link: %v", err)
	}
	if after, _ := h.saved(t); after.Password != c.Password {
		t.Fatal("failed pairing replaced the token")
	}
}

func TestRenewal(t *testing.T) {
	h := newHarness(t, config.OpenCodeSettings{})
	fake := opencodetest.New(t, "server-password")
	if err := h.s.Pair(t.Context(), fake.Link(), false); err != nil {
		t.Fatal(err)
	}
	first, _ := h.saved(t)
	issued := *first.TokenIssuedAt
	// Status's health check renews in the background too: share the clock safely.
	var now atomic.Int64
	set := func(t time.Time) time.Time { now.Store(t.UnixNano()); return t }
	clock := set(issued.Add(6 * 24 * time.Hour))
	h.s.mu.Lock()
	h.s.now = func() time.Time { return time.Unix(0, now.Load()).UTC() }
	h.s.mu.Unlock()
	h.s.renewIfDue(t.Context())
	if fake.Pairs != 0 {
		t.Fatal("renewed a fresh token")
	}

	clock = set(issued.Add(8 * 24 * time.Hour))
	h.s.renewIfDue(t.Context())
	renewed, _ := h.saved(t)
	if fake.Pairs != 1 || renewed.Password == first.Password || !fake.Accepts(renewed.Password) || !renewed.TokenIssuedAt.Equal(clock) || renewed.Auth != "paired" {
		t.Fatalf("renewal: %d %+v", fake.Pairs, renewed)
	}
	if st := h.s.Status(t.Context()); st.State != "connected" || !st.ExpiresAt.Equal(clock.Add(30*24*time.Hour).Truncate(time.Second)) {
		t.Fatalf("after renewal: %+v", st)
	}
	h.idle()

	// A failed renewal keeps the token and waits an hour.
	clock = set(clock.Add(8 * 24 * time.Hour))
	fake.Revoke()
	h.s.renewIfDue(t.Context())
	kept, _ := h.saved(t)
	if kept.Password != renewed.Password || !kept.TokenIssuedAt.Equal(*renewed.TokenIssuedAt) {
		t.Fatal("failed renewal replaced the token")
	}
	attempts := fake.Pairs
	clock = set(clock.Add(30 * time.Minute))
	h.s.renewIfDue(t.Context())
	if fake.Pairs != attempts {
		t.Fatal("retried within an hour")
	}
}

func TestSaveAuthModes(t *testing.T) {
	h := newHarness(t, config.OpenCodeSettings{})
	fake := opencodetest.New(t, "server-password")
	if err := h.s.Pair(t.Context(), fake.Link(), false); err != nil {
		t.Fatal(err)
	}
	paired, _ := h.saved(t)
	browser := "https://browser.example"
	if err := h.s.Save(t.Context(), config.OpenCodeEdit{BrowserEndpoint: &browser}); err != nil {
		t.Fatal(err)
	}
	if c, _ := h.saved(t); c.Auth != "paired" || c.Password != paired.Password || c.TokenIssuedAt == nil || c.BrowserEndpoint != browser {
		t.Fatalf("browser edit changed auth: %+v", c)
	}

	user, wrong := "opencode", "wrong"
	if err := h.s.Save(t.Context(), config.OpenCodeEdit{Username: &user, Password: &wrong}); conflictCode(err) != CodeAuthRequired || err.Error() != "OpenCode refused access. Check the configured credentials." {
		t.Fatalf("rejected credentials: %v", err)
	}
	right := "server-password"
	if err := h.s.Save(t.Context(), config.OpenCodeEdit{Username: &user, Password: &right}); err != nil {
		t.Fatal(err)
	}
	if c, _ := h.saved(t); c.Auth != "basic" || c.Password != right || c.TokenIssuedAt != nil {
		t.Fatalf("credentials: %+v", c)
	}

	open := opencodetest.New(t, "")
	if err := h.s.Save(t.Context(), config.OpenCodeEdit{Endpoint: &open.URL}); err != nil {
		t.Fatal(err)
	}
	if c, verified := h.saved(t); c.Auth != "none" || c.Password != "" || c.Endpoint != open.URL || verified != open.URL {
		t.Fatalf("endpoint change: %+v", c)
	}
	if st := h.s.Status(t.Context()); st.Auth != "none" || st.State != "connected" {
		t.Fatalf("%+v", st)
	}

	bad := "http://127.0.0.1:49374\u200b"
	var invalid *db.InvalidError
	if err := h.s.Save(t.Context(), config.OpenCodeEdit{Endpoint: &bad}); !errors.As(err, &invalid) || invalid.Msg != "Service URL contains an invisible character (U+200B); retype it" {
		t.Fatalf("invisible character: %v", err)
	}
}

func TestDisconnect(t *testing.T) {
	h := newHarness(t, config.OpenCodeSettings{})
	fake := opencodetest.New(t, "server-password")
	if err := h.s.Pair(t.Context(), fake.Link(), false); err != nil {
		t.Fatal(err)
	}
	installs := h.installs
	if err := h.s.Disconnect(t.Context()); err != nil {
		t.Fatal(err)
	}
	c, verified := h.saved(t)
	if c != (config.OpenCodeSettings{Auth: "none"}) || verified != fake.URL || h.installed != nil || h.installs != installs+1 {
		t.Fatalf("disconnect: %+v %q %v", c, verified, h.installed)
	}
	if st := h.status(t); st.State != "unconfigured" || st.Auth != "none" || st.Endpoint != "" || st.HasCredentials {
		t.Fatalf("%+v", st)
	}
	// A later connection elsewhere still needs continuity with the old address.
	other := opencodetest.New(t, "")
	if err := h.s.Save(t.Context(), config.OpenCodeEdit{Endpoint: &other.URL}); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentManagedConnection(t *testing.T) {
	h := newHarness(t, config.OpenCodeSettings{Endpoint: "http://127.0.0.1:1"})
	for name, err := range map[string]error{
		"connect-local": h.s.ConnectLocal(t.Context(), false),
		"pair":          h.s.Pair(t.Context(), "http://127.0.0.1:1/auth/connect/x", false),
		"disconnect":    h.s.Disconnect(t.Context()),
	} {
		var c *db.ConflictError
		if !errors.As(err, &c) || !strings.Contains(c.Msg, "managed by the Stage server environment") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if st := h.status(t); st.Local.Reason != "environment" || st.Local.Available {
		t.Fatalf("%+v", st.Local)
	}
}
