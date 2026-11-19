package cli_test

import (
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/t3"
)

func TestT3Status(t *testing.T) {
	h := newHarness(t)
	code, out, errOut := h.run("", "t3")
	if code != 0 || !strings.Contains(out, "t3 connected at http://127.0.0.1:3773 (default)") ||
		!strings.Contains(out, "public    http://t3.test") || !strings.Contains(out, "server    fake vfake (environment fake-env)") ||
		!strings.Contains(out, "sync      never") || !strings.Contains(out, "auto-pair unavailable (fake T3)") || strings.Contains(out, "next:") {
		t.Fatalf("t3: %d %q %q", code, out, errOut)
	}
	code, out, _ = h.run("", "t3", "status", "--json")
	st := decode[struct {
		State       string `json:"state"`
		T3URLSource string `json:"t3UrlSource"`
		Reachable   bool   `json:"reachable"`
		AutoPair    struct {
			Reason *string `json:"reason"`
		} `json:"autoPair"`
	}](t, out)
	if code != 0 || st.State != "connected" || st.T3URLSource != "default" || !st.Reachable || st.AutoPair.Reason == nil || *st.AutoPair.Reason != "fake" {
		t.Fatalf("t3 status --json: %d %s", code, out)
	}
	h.fake.SetProbe(t3.Probe{CheckedAt: time.Now(), Err: "connection refused"})
	code, out, _ = h.run("", "t3", "status")
	if code != 0 || !strings.Contains(out, "t3 unreachable at") || !strings.Contains(out, "server    not answering: connection refused") ||
		!strings.Contains(out, "next: start T3 (`t3 serve`)") {
		t.Fatalf("unreachable: %d %q", code, out)
	}
	if code, _, errOut := h.run("", "t3", "nope"); code != 2 || !strings.Contains(errOut, "t3 nope: unknown") {
		t.Fatalf("unknown verb: %d %q", code, errOut)
	}
}

func TestT3Pair(t *testing.T) {
	h := newHarness(t)
	code, out, errOut := h.run("", "t3", "pair", "--code", "ABCD-1234")
	if code != 0 || !strings.Contains(out, "t3 connected") || len(h.fake.Pairs) != 1 || h.fake.Pairs[0] != "ABCD-1234" {
		t.Fatalf("pair --code: %d %q %q %v", code, out, errOut, h.fake.Pairs)
	}
	// No code: auto-pair, which the fake cannot do (409).
	if code, _, errOut := h.run("", "t3", "pair"); code != 1 || !strings.Contains(errOut, "stage API 409: auto-pair is not available with the fake T3 client") {
		t.Fatalf("auto pair: %d %q", code, errOut)
	}
	if code, _, errOut := h.run("", "t3", "pair", "--code", " "); code != 2 || !strings.Contains(errOut, "--code is empty") {
		t.Fatalf("empty code: %d %q", code, errOut)
	}
	if code, _, errOut := h.run("", "t3", "pair", "ABCD"); code != 2 || !strings.Contains(errOut, "use --code CODE") {
		t.Fatalf("positional code: %d %q", code, errOut)
	}
}

func TestT3Disconnect(t *testing.T) {
	h := newHarness(t)
	code, out, errOut := h.run("", "t3", "disconnect")
	if code != 0 || !strings.Contains(out, "t3 unpaired") || !strings.Contains(out, "next: run `t3 pair`") {
		t.Fatalf("disconnect: %d %q %q", code, out, errOut)
	}
	if code, _, errOut := h.run("", "t3", "disconnect", "now"); code != 2 || !strings.Contains(errOut, "unexpected argument") {
		t.Fatalf("extra arg: %d %q", code, errOut)
	}
	if code, out, errOut := h.run("", "t3", "pair", "--code", "ABCD-1234"); code != 0 || !strings.Contains(out, "t3 connected") {
		t.Fatalf("re-pair: %d %q %q", code, out, errOut)
	}
}

func TestT3URL(t *testing.T) {
	h := newHarness(t)
	code, out, errOut := h.run("", "t3", "url", "http://localhost:4000/")
	if code != 0 || !strings.Contains(out, "at http://localhost:4000 (saved setting)") || h.fake.BaseURL != "http://localhost:4000" {
		t.Fatalf("url set: %d %q %q", code, out, errOut)
	}
	code, out, _ = h.run("", "t3", "url", "--json")
	if st := decode[struct {
		T3URL  string `json:"t3Url"`
		Source string `json:"t3UrlSource"`
	}](t, out); code != 0 || st.T3URL != "http://localhost:4000" || st.Source != "settings" {
		t.Fatalf("url show: %d %s", code, out)
	}
	if code, _, errOut := h.run("", "t3", "url", "ftp://x"); code != 1 || !strings.Contains(errOut, "stage API 400: url must start with http:// or https://") {
		t.Fatalf("bad url: %d %q", code, errOut)
	}
	if code, _, errOut := h.run("", "t3", "url", "http://a", "--reset"); code != 2 || !strings.Contains(errOut, "want one URL or --reset") {
		t.Fatalf("url and reset: %d %q", code, errOut)
	}
	code, out, errOut = h.run("", "t3", "url", "--reset")
	if code != 0 || !strings.Contains(out, "at http://127.0.0.1:3773 (default)") || h.fake.BaseURL != "http://127.0.0.1:3773" {
		t.Fatalf("reset: %d %q %q", code, out, errOut)
	}
}
