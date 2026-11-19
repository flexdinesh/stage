// Package bridge connects the desktop webview to the Stage server (bridge
// B of docs/adr/0001-desktop-shell.md): the page's own scheme
// (mygo://localhost) serves the frontend and reverse-proxies /api/* to the
// active target, so relative fetch, EventSource and <img> work unchanged.
// The target is this machine's server over its Unix socket (no TCP
// listener anywhere) or a saved remote server over TCP/TLS
// (docs/desktop.md, Servers).
//
// The target is swappable: the launcher sets it once the server is ready
// and may change it later (a restart, a foreground server, a switch to
// another server, which ends every open request and stream of the old
// one). Until then, and while the target refuses connections, JSON
// requests get 503 and event streams wait for it (APIProxy).
//
// The package does not import MyGo: it is plain net/http and unit-tested
// without a webview.
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"sync/atomic"
	"time"

	"github.com/flexdinesh/stage/desktop/app/target"
)

// Host is the synthetic Host of every request proxied to the local
// socket. The server's origin policy accepts it on Unix listeners only
// (docs/runtime.md); the .invalid TLD (RFC 2606) never resolves. Remote
// requests carry the remote URL's host.
const Host = target.SocketHost

// PageOrigin is the webview page's origin, the only Origin the bridge
// forwards for.
const PageOrigin = "mygo://localhost"

// APIPrefix is the only path prefix the bridge forwards.
const APIPrefix = "/api/"

// CSP is the Content-Security-Policy of every static response. Both
// 'self' and mygo://localhost are listed: WebKit does not always treat a
// custom scheme as 'self'.
const CSP = "default-src 'self' mygo://localhost; " +
	"connect-src 'self' mygo://localhost; " +
	"img-src 'self' mygo://localhost blob: data:; " +
	"style-src 'self' mygo://localhost 'unsafe-inline'; " +
	"font-src 'self' mygo://localhost data:; " +
	"script-src 'self' mygo://localhost; " +
	"object-src 'none'; frame-src 'none'; base-uri 'none'; form-action 'none'"

// ErrRedirect is returned for any redirect: the Stage API never redirects,
// so one is refused rather than followed off the target.
var ErrRedirect = errors.New("bridge: redirects are not followed")

// errSwitched: the request was cut because the app switched servers.
var errSwitched = errors.New("bridge: switched to another Stage server")

// methods are the HTTP methods the Stage API uses.
var methods = map[string]bool{
	http.MethodGet: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true,
}

// Denial is why the policy refused a request, with the status the bridge
// answers it with.
type Denial struct {
	Status int
	Reason string
}

func (d *Denial) Error() string { return "bridge: " + d.Reason }

// Permit applies the request policy to a method and URL path: a Stage API
// method, a path under /api/ that is already clean (no "..", ".", "//" or
// trailing slash), and no encoded slash or backslash that could re-split
// it.
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

// DefaultStreamWait bounds how long an event stream request waits for the
// target before it is answered with an empty stream (which EventSource
// retries).
const DefaultStreamWait = 25 * time.Second

// conn is one target with its transport and the context that ends every
// request to it when the target changes.
type conn struct {
	t      target.Target
	tr     *http.Transport
	ctx    context.Context
	cancel context.CancelFunc
}

// reqKey carries the request's conn and the page's own context to the
// proxy's hooks.
type reqKey struct{}

type reqInfo struct {
	c    *conn
	page context.Context
}

// APIProxy is the /api/* half of bridge B: a reverse proxy to the Stage
// server of the current target. It applies Permit and the page-origin
// check, flushes every write at once (SSE), forwards cancellation (the
// scheme request's context ends when the webview drops it), refuses
// redirects with 502 rather than letting the webview follow them, and
// drops cookies.
//
// Requests reach the server as a non-browser client: Host is the target's
// (stage.invalid on the socket, the remote's host over TCP) and Cookie,
// Origin and Sec-Fetch-* are stripped, so the server's cross-origin checks
// pass for a remote too (its hostname still needs STAGE_ALLOWED_ORIGINS
// or STAGE_PUBLIC_URL there, docs/runtime.md). The bridge enforces the
// page origin itself first: a cross-site request, or an unsafe method
// whose Origin is not the page's, gets 403.
//
// Without a usable target, JSON requests get 503 {error, code} with code
// "starting" (no target yet) or "unavailable" (it refuses connections, or
// the app switched servers mid-request). Event streams (Accept:
// text/event-stream) never get an HTTP error, which would close an
// EventSource for good: they wait for the target up to StreamWait without
// sending headers, then proxy. If it does not come back, they get an
// empty 200 stream with a short retry, so EventSource reconnects (with
// Last-Event-ID) and waits again. SetTarget to another server ends every
// open request and stream to the old one. A handler cannot fail a MyGo
// scheme request as a network error: a panic before the headers becomes
// a 500.
type APIProxy struct {
	cur atomic.Pointer[conn]
	rp  *httputil.ReverseProxy
	// StreamWait bounds an event stream's wait for the target (default
	// DefaultStreamWait).
	StreamWait time.Duration
}

// NewAPIProxy proxies to t; a zero Target leaves it unset until
// SetTarget.
func NewAPIProxy(t target.Target) *APIProxy {
	p := &APIProxy{StreamWait: DefaultStreamWait}
	p.cur.Store(newConn(t))
	p.rp = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			c := infoOf(pr.In).c
			base, _ := url.Parse(c.t.BaseURL())
			pr.Out.URL.Scheme = base.Scheme
			pr.Out.URL.Host = base.Host
			pr.Out.Host = c.t.Host()
			for k := range pr.Out.Header {
				if strings.HasPrefix(http.CanonicalHeaderKey(k), "Sec-Fetch-") {
					pr.Out.Header.Del(k)
				}
			}
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Origin")
		},
		Transport:     roundTripper{},
		FlushInterval: -1,
		ModifyResponse: func(res *http.Response) error {
			if res.StatusCode >= 300 && res.StatusCode < 400 && res.StatusCode != http.StatusNotModified {
				return ErrRedirect
			}
			res.Header.Del("Set-Cookie")
			return nil
		},
		ErrorHandler: p.proxyError,
		// Outside net/http's server, ReverseProxy logs every stream the
		// page or server ends ("suppressing panic for copyResponse
		// error"); that is routine here.
		ErrorLog: log.New(io.Discard, "", 0),
	}
	return p
}

func newConn(t target.Target) *conn {
	ctx, cancel := context.WithCancel(context.Background())
	c := &conn{t: t, ctx: ctx, cancel: cancel}
	if !t.IsZero() {
		c.tr = t.Transport()
	}
	return c
}

// roundTripper sends each request over its conn's transport.
type roundTripper struct{}

func (roundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	c := infoOf(r).c
	if c.tr == nil {
		return nil, &target.DialError{Err: target.ErrNoSocket}
	}
	return c.tr.RoundTrip(r)
}

func infoOf(r *http.Request) reqInfo {
	if v, ok := r.Context().Value(reqKey{}).(reqInfo); ok {
		return v
	}
	return reqInfo{c: &conn{ctx: context.Background()}, page: r.Context()}
}

// SetTarget points the proxy at t (a zero Target unsets it). Another
// server (or the same one at another address) ends every open request
// and stream to the previous one and drops its idle connections; the same
// target again changes nothing.
func (p *APIProxy) SetTarget(t target.Target) {
	if old := p.cur.Load(); old != nil && old.t.Same(t) {
		return
	}
	old := p.cur.Swap(newConn(t))
	if old != nil {
		old.cancel()
		if old.tr != nil {
			old.tr.CloseIdleConnections()
		}
	}
}

// Target is the current target, zero when unset.
func (p *APIProxy) Target() target.Target {
	if c := p.cur.Load(); c != nil {
		return c.t
	}
	return target.Target{}
}

func (p *APIProxy) proxyError(w http.ResponseWriter, r *http.Request, err error) {
	info := infoOf(r)
	if info.page.Err() != nil {
		return // the page went away; nobody reads the answer
	}
	if info.c.ctx.Err() != nil {
		err = errSwitched
	}
	var de *target.DialError
	switch {
	case errors.Is(err, ErrRedirect):
		WriteError(w, http.StatusBadGateway, err.Error(), "redirect")
	case isEventStream(r):
		writeRetry(w)
	case errors.Is(err, target.ErrNoSocket), info.c.t.IsZero():
		WriteError(w, http.StatusServiceUnavailable, "bridge: the Stage server is starting", "starting")
	case errors.As(err, &de), errors.Is(err, errSwitched):
		WriteError(w, http.StatusServiceUnavailable, "bridge: "+err.Error(), "unavailable")
	default:
		WriteError(w, http.StatusBadGateway, err.Error(), "unavailable")
	}
}

func (p *APIProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// ReverseProxy aborts with http.ErrAbortHandler when the server or the
	// page goes away mid-body; the stream then just ends, which EventSource
	// retries. MyGo's scheme server would log the panic as a crash.
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
	if reason := crossOrigin(r); reason != "" {
		WriteError(w, http.StatusForbidden, "bridge: "+reason, "denied")
		return
	}
	c := p.cur.Load()
	if isEventStream(r) {
		if c = p.waitTarget(r.Context()); c == nil {
			if r.Context().Err() == nil {
				writeRetry(w)
			}
			return
		}
	}
	// The request ends with the page or when the target changes.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(c.ctx, cancel)
	defer stop()
	ctx = context.WithValue(ctx, reqKey{}, reqInfo{c: c, page: r.Context()})
	// MyGo sets ContentLength only from a Content-Length header, which
	// macOS WKWebView does not send; ReverseProxy drops a body of length 0.
	// Forward it with unknown length (chunked) instead.
	if r.ContentLength == 0 && r.Body != nil && r.Body != http.NoBody {
		r.ContentLength = -1
	}
	p.rp.ServeHTTP(w, r.WithContext(ctx))
}

// crossOrigin is why the request did not come from the app's own page, ""
// when it did: Sec-Fetch-Site cross-site or same-site, or an unsafe
// method with another Origin (the server's guard does the same for
// unstripped requests, docs/runtime.md).
func crossOrigin(r *http.Request) string {
	origin := r.Header.Get("Origin")
	switch r.Header.Get("Sec-Fetch-Site") {
	case "cross-site", "same-site":
		if origin != PageOrigin {
			return "cross-site request rejected"
		}
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		return ""
	}
	if origin != "" && origin != PageOrigin {
		return "cross-origin request rejected"
	}
	return ""
}

// waitTarget returns the current target once it accepts connections,
// waiting up to StreamWait for it (following target changes); nil when
// the wait runs out or ctx ends.
func (p *APIProxy) waitTarget(ctx context.Context) *conn {
	wait := p.StreamWait
	if wait <= 0 {
		wait = DefaultStreamWait
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	delay := 100 * time.Millisecond
	for {
		c := p.cur.Load()
		if !c.t.IsZero() {
			dctx, cancel := context.WithTimeout(ctx, max(wait/2, time.Second))
			conn, err := c.t.Dial(dctx)
			cancel()
			if err == nil {
				_ = conn.Close()
				return c
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-deadline.C:
			return nil
		case <-time.After(delay):
		}
		delay = min(delay*2, time.Second)
	}
}

func isEventStream(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/event-stream")
}

// writeRetry answers an event stream with an empty stream that asks for a
// reconnect in a second: EventSource reconnects after any stream ends, but
// fails for good on an HTTP error or another content type.
func writeRetry(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("retry: 1000\n\n"))
}

// Scheme is bridge B's handler of the page's scheme: /api and /api/* go to
// api, everything else to static (GET and HEAD only), which serves the
// frontend (mygo.FileServer in the app, with its index.html fallback),
// with the CSP header on every response.
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
		w.Header().Set("Content-Security-Policy", CSP)
		w.Header().Set("X-Content-Type-Options", "nosniff")
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
