// Package bridge connects a desktop webview to the Stage server over a Unix
// domain socket, with no TCP listener. Two candidate bridges share one
// request policy:
//
//   - A, the MyGo fetch plugin: pages request the synthetic origin
//     http://stage.invalid through @mygo-plugins/fetch; Allow vets each
//     request and UnixClient carries it to the socket.
//   - B, scheme forwarding: the page's own scheme (mygo://localhost)
//     serves the frontend and reverse-proxies /api/* to the socket
//     (Scheme, NewAPIProxy), so relative fetch, EventSource and <img> work
//     unchanged.
//
// The package does not import MyGo: it is plain net/http and unit-tested
// without a webview.
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"time"
)

// Host is the synthetic host of the Stage API for bridge A. The .invalid
// TLD (RFC 2606) never resolves, so a misrouted request cannot reach a
// real network host.
const Host = "stage.invalid"

// Origin is the synthetic origin pages of bridge A request.
const Origin = "http://" + Host

// APIPrefix is the only path prefix either bridge forwards.
const APIPrefix = "/api/"

// ErrRedirect is returned for any redirect: the Stage API never redirects,
// so one is refused rather than followed off the socket.
var ErrRedirect = errors.New("bridge: redirects are not followed")

// methods are the HTTP methods the Stage API uses.
var methods = map[string]bool{
	http.MethodGet: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true,
}

// Denial is why the policy refused a request, with the status a bridge
// answers it with.
type Denial struct {
	Status int
	Reason string
}

func (d *Denial) Error() string { return "bridge: " + d.Reason }

// Permit applies the request policy shared by both bridges to a method
// and URL path: a Stage API method, a path under /api/ that is already
// clean (no "..", ".", "//" or trailing slash), and no encoded slash or
// backslash that could re-split it.
func Permit(method string, u *url.URL) error {
	if !methods[method] {
		return &Denial{http.StatusMethodNotAllowed, fmt.Sprintf("method %s is not allowed", method)}
	}
	p := u.Path
	if !strings.HasPrefix(p, APIPrefix) || path.Clean(p) != p {
		return &Denial{http.StatusNotFound, fmt.Sprintf("path %q is not a Stage API path", p)}
	}
	raw := strings.ToLower(u.EscapedPath())
	if strings.Contains(p, `\`) || strings.Contains(raw, "%2f") || strings.Contains(raw, "%5c") {
		return &Denial{http.StatusNotFound, fmt.Sprintf("path %q has an encoded separator", p)}
	}
	return nil
}

// Allow is the fetch plugin's Allow for bridge A: plain http to the
// synthetic host only, without credentials, port or Host override, and
// within the shared policy.
func Allow(r *http.Request) bool {
	u := r.URL
	if u.Scheme != "http" || u.Host != Host || u.User != nil {
		return false
	}
	if r.Host != "" && r.Host != Host {
		return false
	}
	return Permit(r.Method, u) == nil
}

// Transport dials socket for every request, whatever the URL's host: it
// has no proxy (HTTP_PROXY and the like are ignored) and no TCP fallback.
func Transport(socket string) *http.Transport {
	return &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
		MaxIdleConns:          16,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 0, // SSE streams send headers at once, then idle between pings
	}
}

// UnixClient is the http.Client of bridge A: Transport(socket), no cookie
// jar, and every redirect refused. The fetch plugin calls CheckRedirect
// for redirect mode "follow", returns the 3xx response for "manual" and
// rejects for "error", so no mode follows a redirect.
func UnixClient(socket string) *http.Client {
	return &http.Client{
		Transport: Transport(socket),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return ErrRedirect
		},
	}
}

// NewAPIProxy is the /api/* half of bridge B: a reverse proxy to the
// Stage server on socket. It applies Permit, flushes every write at once
// (SSE), forwards cancellation (the scheme request's context ends when
// the webview drops it), refuses redirects with 502 rather than letting
// the webview follow them, and drops cookies.
func NewAPIProxy(socket string) http.Handler {
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = Host
			pr.Out.Host = Host
			pr.Out.Header.Del("Cookie")
		},
		Transport:     Transport(socket),
		FlushInterval: -1,
		ModifyResponse: func(res *http.Response) error {
			if res.StatusCode >= 300 && res.StatusCode < 400 && res.StatusCode != http.StatusNotModified {
				return ErrRedirect
			}
			res.Header.Del("Set-Cookie")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if r.Context().Err() != nil {
				return // the page went away; nobody reads the answer
			}
			code := "unavailable"
			if errors.Is(err, ErrRedirect) {
				code = "redirect"
			}
			WriteError(w, http.StatusBadGateway, err.Error(), code)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ReverseProxy aborts with http.ErrAbortHandler when the page goes
		// away mid-body (a closed EventSource); MyGo's scheme server would
		// log that as a crash.
		defer func() {
			if v := recover(); v != nil {
				if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					return
				}
				panic(v)
			}
		}()
		if err := Permit(r.Method, r.URL); err != nil {
			var d *Denial
			if errors.As(err, &d) {
				WriteError(w, d.Status, d.Reason, "denied")
				return
			}
			WriteError(w, http.StatusForbidden, err.Error(), "denied")
			return
		}
		rp.ServeHTTP(w, r)
	})
}

// Scheme is bridge B's handler of the page's scheme: /api and /api/* go to
// api, everything else to static (GET and HEAD only), which serves the
// frontend (mygo.FileServer in the app, with its index.html fallback).
func Scheme(api, static http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, APIPrefix) {
			api.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			WriteError(w, http.StatusMethodNotAllowed, "the frontend is read-only", "denied")
			return
		}
		static.ServeHTTP(w, r)
	})
}

// WriteError answers with Stage's error body, {error, code?}.
func WriteError(w http.ResponseWriter, status int, msg, code string) {
	body := map[string]string{"error": msg}
	if code != "" {
		body["code"] = code
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
