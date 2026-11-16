// Package bridge connects the desktop webview to the Stage server over a
// Unix domain socket, with no TCP listener (bridge B of
// docs/adr/0001-desktop-shell.md): the page's own scheme
// (mygo://localhost) serves the frontend and reverse-proxies /api/* to the
// socket, so relative fetch, EventSource and <img> work unchanged.
//
// The socket is swappable: the launcher sets it once the server is ready
// and may change it later (a restart, a foreground server). Until then,
// and while the socket refuses connections, JSON requests get 503 and
// event streams wait for it (APIProxy).
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
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"sync/atomic"
	"time"
)

// Host is the synthetic Host of every proxied request. The server's
// origin policy accepts it on Unix listeners only (docs/runtime.md); the
// .invalid TLD (RFC 2606) never resolves.
const Host = "stage.invalid"

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
// so one is refused rather than followed off the socket.
var ErrRedirect = errors.New("bridge: redirects are not followed")

// errNotReady: no socket is set yet (the server is starting).
var errNotReady = errors.New("bridge: the Stage server is starting")

// dialError is a failure to connect to the socket.
type dialError struct{ err error }

func (e *dialError) Error() string { return "bridge: Stage server unavailable: " + e.err.Error() }
func (e *dialError) Unwrap() error { return e.err }

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
// socket before it is answered with an empty stream (which EventSource
// retries).
const DefaultStreamWait = 25 * time.Second

// APIProxy is the /api/* half of bridge B: a reverse proxy to the Stage
// server on the current socket. It applies Permit, flushes every write at
// once (SSE), forwards cancellation (the scheme request's context ends
// when the webview drops it), refuses redirects with 502 rather than
// letting the webview follow them, and drops cookies.
//
// Without a usable socket, JSON requests get 503 {error, code} with code
// "starting" (no socket yet) or "unavailable" (it refuses connections).
// Event streams (Accept: text/event-stream) never get an HTTP error, which
// would close an EventSource for good: they wait for the socket up to
// StreamWait without sending headers, then proxy. If it does not come
// back, they get an empty 200 stream with a short retry, so EventSource
// reconnects (with Last-Event-ID) and waits again. A handler cannot fail
// a MyGo scheme request as a network error: a panic before the headers
// becomes a 500.
type APIProxy struct {
	socket atomic.Pointer[string]
	rp     *httputil.ReverseProxy
	tr     *http.Transport
	// StreamWait bounds an event stream's wait for the socket (default
	// DefaultStreamWait).
	StreamWait time.Duration
}

// NewAPIProxy proxies to socket; "" leaves it unset until SetSocket.
func NewAPIProxy(socket string) *APIProxy {
	p := &APIProxy{StreamWait: DefaultStreamWait}
	p.socket.Store(&socket)
	p.tr = &http.Transport{
		Proxy:                 nil, // HTTP_PROXY and the like never apply
		DialContext:           func(ctx context.Context, _, _ string) (net.Conn, error) { return p.dial(ctx) },
		MaxIdleConns:          16,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 0, // SSE streams send headers at once, then idle between pings
	}
	p.rp = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = Host
			pr.Out.Host = Host
			pr.Out.Header.Del("Cookie")
		},
		Transport:     p.tr,
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

// SetSocket points the proxy at socket ("" unsets it) and drops idle
// connections to the previous one.
func (p *APIProxy) SetSocket(socket string) {
	if old := p.socket.Swap(&socket); old != nil && *old != socket {
		p.tr.CloseIdleConnections()
	}
}

// Socket is the current socket, "" when unset.
func (p *APIProxy) Socket() string {
	if s := p.socket.Load(); s != nil {
		return *s
	}
	return ""
}

func (p *APIProxy) dial(ctx context.Context) (net.Conn, error) {
	s := p.Socket()
	if s == "" {
		return nil, errNotReady
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", s)
	if err != nil {
		return nil, &dialError{err}
	}
	return c, nil
}

func (p *APIProxy) proxyError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		return // the page went away; nobody reads the answer
	}
	var de *dialError
	switch {
	case errors.Is(err, ErrRedirect):
		WriteError(w, http.StatusBadGateway, err.Error(), "redirect")
	case isEventStream(r):
		writeRetry(w)
	case errors.Is(err, errNotReady):
		WriteError(w, http.StatusServiceUnavailable, err.Error(), "starting")
	case errors.As(err, &de):
		WriteError(w, http.StatusServiceUnavailable, err.Error(), "unavailable")
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
	if isEventStream(r) && !p.waitSocket(r.Context()) {
		if r.Context().Err() == nil {
			writeRetry(w)
		}
		return
	}
	p.rp.ServeHTTP(w, r)
}

// waitSocket reports whether the socket accepts connections, waiting up to
// StreamWait for it.
func (p *APIProxy) waitSocket(ctx context.Context) bool {
	wait := p.StreamWait
	if wait <= 0 {
		wait = DefaultStreamWait
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	delay := 100 * time.Millisecond
	for {
		if c, err := p.dial(ctx); err == nil {
			_ = c.Close()
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
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
