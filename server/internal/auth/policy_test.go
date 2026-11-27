package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestPasswordPolicyAndVerifierBounds(t *testing.T) {
	s := New(nil, "", nil)
	ctx := t.Context()
	for _, password := range []string{strings.Repeat("ひ", 15), strings.Repeat("🛠", 128), "Pockets full of blue clover"} {
		checkErr(t, validatePassword(password), nil)
	}
	for _, password := range []string{strings.Repeat("🛠", 14), strings.Repeat("🛠", 129), "valid until here\xff"} {
		checkErr(t, validatePassword(password), ErrPasswordLength)
	}
	checkErr(t, validatePassword("PasswordPassword"), ErrCommonPassword)
	pass := "Café at seven under rain"
	a := require[string](t)(s.hashPassword(ctx, pass))
	b := require[string](t)(s.hashPassword(ctx, pass))
	if !strings.HasPrefix(a, verifierV1) || a == b || strings.Contains(a, pass) {
		t.Fatal("password verifier is not versioned, salted and opaque")
	}
	if match := require[bool](t)(s.verifyPassword(ctx, a, "Cafe\u0301 at seven under rain")); !match {
		t.Fatal("canonically equivalent passphrases did not match")
	}
	malicious := strings.Replace(a, "m=65536", "m=4294967295", 1)
	if _, _, ok := parseVerifier(malicious); ok {
		t.Fatal("accepted unbounded verifier parameters")
	}
	if match := require[bool](t)(s.verifyPassword(ctx, malicious, pass)); match {
		t.Fatal("matched unsupported parameters")
	}
	for range argonSlots {
		s.hashSlots <- struct{}{}
	}
	_, err := s.hashPassword(ctx, pass)
	checkErr(t, err, ErrBusy)
	_, err = s.verifyPassword(ctx, a, pass)
	checkErr(t, err, ErrBusy)
}

func TestSignInLimiterBoundsAndSaturation(t *testing.T) {
	s := New(nil, "", nil)
	l := &s.limiter
	now := time.Date(2026, 10, 6, 13, 0, 0, 0, time.UTC)
	for i := range accountLimit {
		if !l.allow(" Admin@example.test ", netip.AddrFrom4([4]byte{192, 0, 2, byte(i)}), now) {
			t.Fatal("account throttled early")
		}
	}
	if l.allow(strings.Repeat(" ", 300)+"admin@EXAMPLE.TEST", testPeer, now) || !l.allow("admin@example.test", testPeer, now.Add(limitWindow)) {
		t.Fatal("canonical account limit or deadline did not hold")
	}
	now = now.Add(2 * limitWindow)
	for i := range ipLimit {
		if !l.allow(fmt.Sprintf("account%d@example.test", i), testPeer, now) {
			t.Fatal("IP throttled early")
		}
	}
	if l.allow("external@example.test", netip.MustParseAddr("::ffff:192.0.2.44"), now) {
		t.Fatal("IPv4-mapped peer evaded rate limit")
	}
	now = now.Add(limitWindow)
	for i := range limitCapacity {
		if !l.allow(fmt.Sprintf("account%d@example.test", i), netip.AddrFrom4([4]byte{192, 0, byte(i >> 8), byte(i)}), now) {
			t.Fatalf("limiter capacity reached early after %d keys", i)
		}
	}
	if l.allow("external@example.test", netip.MustParseAddr("203.0.113.1"), now) ||
		len(l.accounts) != limitCapacity || len(l.addresses) != limitCapacity {
		t.Fatal("limiter permitted unbounded state")
	}
	if !l.allow("external@example.test", netip.MustParseAddr("203.0.113.1"), now.Add(limitWindow)) {
		t.Fatal("expired limiter entries were retained")
	}
}

func TestPublicOriginCanonicalAndErrorDoesNotEchoInput(t *testing.T) {
	for raw, want := range map[string]string{
		"https://STAGE.example.test:443/": "https://stage.example.test",
		"https://stage.example.test:8443": "https://stage.example.test:8443",
		"http://LOCALHOST:80/":            "http://localhost",
		"http://127.0.0.1:7380":           "http://127.0.0.1:7380",
		"http://[0:0:0:0:0:0:0:1]:7380/":  "http://[::1]:7380",
	} {
		if got := require[string](t)(ParsePublicOrigin(raw)); got != want {
			t.Errorf("origin = %q, want %q", got, want)
		}
	}
	for _, raw := range []string{
		"", "http://192.0.2.44", "http://127.0.0.1.example.test", "http://127.1", "https://[example.test]",
		"https://stage.example.test:0", "https://stage.example.test:65536", "https://stage.example.test:",
		"https://user:private@stage.example.test", "https://stage.example.test/private", "https://stage.example.test/?",
		"https://stage.example.test/#private", "https://stage.example.test#", "https://stage.example.test/%2F",
	} {
		_, err := ParsePublicOrigin(raw)
		checkErr(t, err, ErrPublicOrigin)
		if strings.Contains(err.Error(), "private") {
			t.Fatal("configuration error echoed sensitive input")
		}
	}
}

func TestCredentialValuesAreRedactedWhenLoggedOrEncoded(t *testing.T) {
	credential, _ := newSecret()
	for _, value := range []any{credential, Link{address: credential}, IssuedGrant{Link: Link{address: credential}}, SignInResult{Token: credential}} {
		var buf bytes.Buffer
		slog.New(slog.NewTextHandler(&buf, nil)).Info("check", "value", value)
		encoded := require[[]byte](t)(json.Marshal(value))
		for _, representation := range []string{
			fmt.Sprintf("%v %+v %#v %q", value, value, value, value), buf.String(), string(encoded),
		} {
			if strings.Contains(representation, credential.Reveal()) {
				t.Fatalf("%T published a credential", value)
			}
		}
	}
}
