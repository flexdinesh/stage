package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

// Secret is released only by an explicit call to Reveal. Formatting and
// ordinary serialization do not include its value.
type Secret struct{ value string }

func (s Secret) Reveal() string { return s.value }

func (Secret) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte("[redacted]")) }

func newSecret() (Secret, store.TokenDigest) {
	var raw [32]byte
	rand.Read(raw[:])
	return Secret{base64.RawURLEncoding.EncodeToString(raw[:])}, sha256.Sum256(raw[:])
}

func tokenDigest(token string) (store.TokenDigest, bool) {
	var raw [32]byte
	if len(token) != base64.RawURLEncoding.EncodedLen(len(raw)) {
		return store.TokenDigest{}, false
	}
	n, err := base64.RawURLEncoding.Strict().Decode(raw[:], []byte(token))
	if err != nil || n != len(raw) {
		return store.TokenDigest{}, false
	}
	return sha256.Sum256(raw[:]), true
}

// Link holds the once-returned grant URL and the deadline shown with it.
type Link struct {
	address   Secret
	ExpiresAt time.Time
}

func (l Link) URL() string { return l.address.Reveal() }

func (Link) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte("[redacted link]")) }

func grantPath(purpose store.GrantPurpose) string {
	switch purpose {
	case store.GrantSetup:
		return "/auth/setup"
	case store.GrantInvite:
		return "/auth/invite"
	case store.GrantPasswordReset:
		return "/auth/reset"
	}
	return ""
}

// ParsePublicOrigin validates the operator-controlled browser origin. HTTPS
// may terminate at the proxy. Loopback HTTP is for local testing.
func ParsePublicOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Opaque != "" ||
		(u.EscapedPath() != "" && u.EscapedPath() != "/") || u.ForceQuery || u.RawQuery != "" || strings.Contains(raw, "#") {
		return "", ErrPublicOrigin
	}
	host := strings.ToLower(u.Hostname())
	ip, ipErr := netip.ParseAddr(host)
	if ipErr == nil && ip.Zone() != "" {
		return "", ErrPublicOrigin
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !(host == "localhost" || ip.IsLoopback())) {
		return "", ErrPublicOrigin
	}
	if ipErr != nil && (strings.HasPrefix(u.Host, "[") || !dnsName(host)) {
		return "", ErrPublicOrigin
	}
	if ipErr == nil {
		host = ip.String()
		if ip.Is6() {
			host = "[" + host + "]"
		}
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", ErrPublicOrigin
		}
		if !(u.Scheme == "https" && n == 443 || u.Scheme == "http" && n == 80) {
			host += ":" + strconv.Itoa(n)
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return "", ErrPublicOrigin
	}
	return u.Scheme + "://" + host, nil
}

func dnsName(host string) bool {
	if len(host) > 253 {
		return false
	}
	for label := range strings.SplitSeq(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	// Browsers interpret a numeric final label as an IPv4 address, even
	// when netip rejects its noncanonical spelling.
	last := host[strings.LastIndexByte(host, '.')+1:]
	if digits, hex := strings.CutPrefix(last, "0x"); hex {
		return strings.Trim(digits, "0123456789abcdef") != ""
	}
	return strings.Trim(last, "0123456789") != ""
}
