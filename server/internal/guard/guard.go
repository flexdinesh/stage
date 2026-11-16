// Package guard rejects browser requests a Stage server must not serve:
// DNS-rebinding Hosts, cross-site requests and form-encodable bodies. It is
// request-origin hygiene, not authentication: anything that can reach a
// listener directly (a local process, a non-browser client) still has full
// access. Remote use belongs behind an authenticating HTTPS reverse proxy
// (docs/runtime.md, "Request origin policy").
package guard

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// DesktopOrigin is the page origin of the desktop webview (bridge B, ADR
// 0001). It reaches Stage only through the unix socket, so it is trusted on
// unix listeners only.
const DesktopOrigin = "mygo://localhost"

// socketHosts are the Host values local socket clients send: the Go client
// ("stage") and bridge B ("stage.invalid"). Single-label "stage" can be
// spoofed on a LAN (LLMNR/mDNS), so they are accepted on unix listeners only.
var socketHosts = map[string]bool{"stage": true, "stage.invalid": true}

// Policy is the request-origin policy for /api and /mcp.
type Policy struct {
	hosts      map[string]bool // hostnames of AllowedOrigins
	tcp, unix  *http.CrossOriginProtection
	trustedTCP map[string]bool
	trustedUDS map[string]bool
	log        *slog.Logger
	logged     sync.Map // rejected host -> struct{}: log once per host
}

// New builds a policy. allowedOrigins are normalized origins
// (config.ParseOrigin) trusted besides same-origin ones; their hostnames
// pass the Host check.
func New(allowedOrigins []string, log *slog.Logger) (*Policy, error) {
	if log == nil {
		log = slog.Default()
	}
	p := &Policy{
		hosts: map[string]bool{}, tcp: http.NewCrossOriginProtection(), unix: http.NewCrossOriginProtection(),
		trustedTCP: map[string]bool{}, trustedUDS: map[string]bool{DesktopOrigin: true}, log: log,
	}
	if err := p.unix.AddTrustedOrigin(DesktopOrigin); err != nil {
		return nil, err
	}
	for _, o := range allowedOrigins {
		u, err := url.Parse(o)
		if err != nil || u.Hostname() == "" {
			return nil, fmt.Errorf("allowed origin %q: invalid", o)
		}
		p.hosts[strings.ToLower(u.Hostname())] = true
		for _, c := range []*http.CrossOriginProtection{p.tcp, p.unix} {
			if err := c.AddTrustedOrigin(o); err != nil {
				return nil, err
			}
		}
		p.trustedTCP[o], p.trustedUDS[o] = true, true
	}
	return p, nil
}

// Wrap applies the policy before h:
//  1. Host must be an IP literal, localhost, a hostname of an allowed
//     origin, or (unix listeners) stage / stage.invalid: 403.
//  2. Sec-Fetch-Site cross-site or same-site, unless Origin is trusted: 403
//     (also on GET, so other sites cannot embed /api images or streams).
//  3. Unsafe methods: http.CrossOriginProtection with the trusted origins
//     (DesktopOrigin on unix listeners only): 403.
//  4. A request body on an unsafe method must be application/json or
//     multipart/form-data: 415 (text/plain and urlencoded bodies are what a
//     cross-site form can send).
func (p *Policy) Wrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		unix := isUnix(r)
		host := hostname(r.Host)
		if !p.hostAllowed(host, unix) {
			if _, seen := p.logged.LoadOrStore(host, struct{}{}); !seen {
				p.log.Warn("rejected request host (add its origin to STAGE_ALLOWED_ORIGINS if it is a trusted proxy)", "host", r.Host, "path", r.URL.Path)
			}
			deny(w, http.StatusForbidden, fmt.Sprintf("host %q not allowed: add its origin to STAGE_ALLOWED_ORIGINS (or set STAGE_PUBLIC_URL) if it is a trusted proxy", r.Host))
			return
		}
		trusted := p.trustedTCP
		cop := p.tcp
		if unix {
			trusted, cop = p.trustedUDS, p.unix
		}
		switch r.Header.Get("Sec-Fetch-Site") {
		case "cross-site", "same-site":
			if !trusted[r.Header.Get("Origin")] {
				deny(w, http.StatusForbidden, "cross-site request rejected")
				return
			}
		}
		if err := cop.Check(r); err != nil {
			deny(w, http.StatusForbidden, "cross-origin request rejected")
			return
		}
		if !safeMethod(r.Method) && r.ContentLength != 0 && !jsonOrMultipart(r.Header.Get("Content-Type")) {
			deny(w, http.StatusUnsupportedMediaType, "request body must be application/json (or multipart/form-data for uploads)")
			return
		}
		h.ServeHTTP(w, r)
	})
}

func (p *Policy) hostAllowed(host string, unix bool) bool {
	switch {
	case host == "":
		return false
	case host == "localhost", p.hosts[host]:
		return true
	case unix && socketHosts[host]:
		return true
	}
	// DNS rebinding needs a name; an IP literal Host cannot be rebound.
	return net.ParseIP(host) != nil
}

// isUnix reports whether the request arrived on a unix socket listener.
func isUnix(r *http.Request) bool {
	a, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	return ok && a != nil && a.Network() == "unix"
}

// hostname is the lowercased Host without port or IPv6 brackets.
func hostname(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(h, "["), "]"))
}

func safeMethod(m string) bool { return m == "GET" || m == "HEAD" || m == "OPTIONS" }

func jsonOrMultipart(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	return err == nil && (mt == "application/json" || mt == "multipart/form-data")
}

func deny(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
