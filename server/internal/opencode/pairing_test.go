package opencode_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/opencode"
	"github.com/flexdinesh/stage/server/internal/opencode/opencodetest"
)

func TestURLErrorsNameTheFieldAndInvisibleCharacters(t *testing.T) {
	for _, tc := range []struct {
		cfg  opencode.Settings
		want string
	}{
		{opencode.Settings{Endpoint: "http://127.0.0.1:49374\u200b"}, "Service URL contains an invisible character (U+200B); retype it"},
		{opencode.Settings{Endpoint: "\ufeffhttp://127.0.0.1:49374"}, "Service URL contains an invisible character (U+FEFF)"},
		{opencode.Settings{Endpoint: "http://127.0.0.1:49374", BrowserEndpoint: "http://host\u00a0:1"}, "Browser service URL contains an invisible character (U+00A0)"},
		{opencode.Settings{Endpoint: "http://127.0.0.1:49374", WebClient: "http://web\t"}, "Web client URL contains an invisible character (U+0009)"},
		{opencode.Settings{Endpoint: "127.0.0.1:49374"}, "Service URL must be an http:// or https:// URL"},
		{opencode.Settings{Endpoint: "http://user:secret@127.0.0.1:49374"}, "Service URL must not include credentials"},
		{opencode.Settings{Endpoint: "http://127.0.0.1:49374", WebClient: "https://web.example?token=secret"}, "Web client URL must not include a query or fragment"},
		{opencode.Settings{Endpoint: "http://"}, "Service URL needs a host"},
	} {
		_, err := opencode.New(2, tc.cfg)
		if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "secret") {
			t.Errorf("%+v: %v, want %q", tc.cfg, err, tc.want)
		}
	}
	if _, err := opencode.New(2, opencode.Settings{Endpoint: "http://127.0.0.1:49374"}); err != nil {
		t.Fatal(err)
	}
}

func writeRegistration(t *testing.T, body string) string {
	t.Helper()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	path := filepath.Join(state, "opencode", "service.json")
	if body != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestRegistration(t *testing.T) {
	path := writeRegistration(t, "")
	var re *opencode.RegistrationError
	if _, err := opencode.ReadRegistration(); !errors.As(err, &re) || re.Reason != "missing" || !strings.Contains(err.Error(), path) {
		t.Fatalf("missing: %v", err)
	}
	for _, body := range []string{"{", `{"url":"http://127.0.0.1:1"}`, `{"url":"ftp://x","pid":3,"password":"secret"}`} {
		writeRegistration(t, body)
		_, err := opencode.ReadRegistration()
		if !errors.As(err, &re) || re.Reason != "invalid" || strings.Contains(err.Error(), "secret") {
			t.Errorf("%s: %v", body, err)
		}
	}
	writeRegistration(t, `{"id":"svc","version":"2.3.0","url":"http://127.0.0.1:49374/","pid":7,"password":"pw"}`)
	reg, err := opencode.ReadRegistration()
	if err != nil || reg != (opencode.Registration{ID: "svc", Version: "2.3.0", URL: "http://127.0.0.1:49374", Password: "pw", PID: 7}) {
		t.Fatalf("%+v %v", reg, err)
	}
	if opencode.RegistrationPath() != filepath.Join(os.Getenv("XDG_STATE_HOME"), "opencode", "service.json") {
		t.Fatal(opencode.RegistrationPath())
	}
}

func TestParsePairLink(t *testing.T) {
	endpoint, code, err := opencode.ParsePairLink("  https://box.example:49374/auth/connect/abc_-1 \n")
	if err != nil || endpoint != "https://box.example:49374" || code != "abc_-1" {
		t.Fatal(endpoint, code, err)
	}
	for link, want := range map[string]string{
		"":                                   "Paste the link",
		"box:49374/auth/connect/abc":         "http:// or https://",
		"http:///auth/connect/abc":           "needs a host",
		"http://u:secret@box/auth/connect/a": "credentials",
		"http://box/auth/connect/abc?x=1":    "query or fragment",
		"http://box/auth/connect/abc#x":      "query or fragment",
		"http://box/auth/connect/":           "auth/connect/CODE",
		"http://box/auth/connect/a/b":        "auth/connect/CODE",
		"http://box/x/auth/connect/abc":      "auth/connect/CODE",
		"http://box/auth/connect/ab\u200bc":  "invisible character (U+200B)",
	} {
		if _, _, err := opencode.ParsePairLink(link); err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "secret") {
			t.Errorf("%q: %v, want %q", link, err, want)
		}
	}
}

func TestRedeemAndRenew(t *testing.T) {
	fake := opencodetest.New(t, "server-password")
	endpoint, code, err := opencode.ParsePairLink(fake.Link())
	if err != nil {
		t.Fatal(err)
	}
	token, err := opencode.Redeem(t.Context(), endpoint, code)
	if err != nil || !fake.Accepts(token) {
		t.Fatal(token, err)
	}
	if _, err := opencode.Redeem(t.Context(), endpoint, code); !errors.Is(err, opencode.ErrPairingExpired) {
		t.Fatal("reused code", err)
	}
	fresh, err := opencode.Renew(t.Context(), endpoint, token)
	if err != nil || fresh == token || !fake.Accepts(fresh) || fake.Pairs != 1 {
		t.Fatal(fresh, err)
	}
	fake.Revoke()
	if _, err := opencode.Renew(t.Context(), endpoint, fresh); err == nil || strings.Contains(err.Error(), fresh) {
		t.Fatal("revoked token renewed", err)
	}
}
