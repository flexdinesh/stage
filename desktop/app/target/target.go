// Package target is the Stage server the desktop app talks to: this
// machine's server over its Unix socket (local, implicit) or a saved
// remote server over TCP/TLS (docs/desktop.md, Servers). It owns the
// saved list (<userData>/servers.json) and how to reach each target:
// the transport (never an HTTP proxy), the Host header and the base URL.
// It does not import MyGo.
package target

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Kind is where a target runs.
type Kind string

// Kinds.
const (
	// KindLocal: this machine's server, over its Unix socket.
	KindLocal Kind = "local"
	// KindRemote: a saved server on another machine, over TCP (http or
	// https).
	KindRemote Kind = "remote"
)

// LocalID is the id of the implicit local target; it is never saved and
// never removed.
const LocalID = "local"

// LocalName is the local target's label.
const LocalName = "This machine"

// SocketHost is the synthetic Host of requests over the local socket
// (bridge.Host); the server's origin policy accepts it on Unix listeners
// only (docs/runtime.md). The .invalid TLD (RFC 2606) never resolves.
const SocketHost = "stage.invalid"

// Target is one Stage server.
type Target struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind Kind   `json:"kind"`
	// URL is a remote server's base URL (http(s)://host[:port]), "" for
	// the local one.
	URL string `json:"url"`
	// Socket is the local server's Unix socket once known; never sent to
	// the page (HostState.server.socket carries it).
	Socket string `json:"-"`
}

// Local is the local target on socket ("" while unknown).
func Local(socket string) Target {
	return Target{ID: LocalID, Name: LocalName, Kind: KindLocal, Socket: socket}
}

// IsZero reports an unset target.
func (t Target) IsZero() bool { return t.ID == "" }

// Remote reports a remote target.
func (t Target) Remote() bool { return t.Kind == KindRemote }

// Same reports whether t and o reach the same server the same way (name
// changes do not count).
func (t Target) Same(o Target) bool {
	return t.ID == o.ID && t.Kind == o.Kind && t.URL == o.URL && t.Socket == o.Socket
}

// Host is the Host header of requests to t: the remote URL's host (with
// its port), else SocketHost.
func (t Target) Host() string {
	if t.Remote() {
		if u, err := url.Parse(t.URL); err == nil {
			return u.Host
		}
	}
	return SocketHost
}

// BaseURL is the URL requests to t are built on: the remote URL, else
// http://stage.invalid (the transport dials the socket).
func (t Target) BaseURL() string {
	if t.Remote() {
		return t.URL
	}
	return "http://" + SocketHost
}

// DialError is a failure to reach a target: no socket yet, connect, DNS
// or TLS handshake.
type DialError struct{ Err error }

func (e *DialError) Error() string { return "Stage server unavailable: " + e.Err.Error() }
func (e *DialError) Unwrap() error { return e.Err }

// ErrNoSocket: the local target's socket is not known yet.
var ErrNoSocket = errors.New("the Stage server is starting")

// dialTimeout bounds a TCP connect (and TLS handshake) to a remote.
const dialTimeout = 10 * time.Second

// Dial opens a connection to t: the socket, or TCP to the remote's host
// (TLS for https). Errors are *DialError.
func (t Target) Dial(ctx context.Context) (net.Conn, error) {
	if !t.Remote() {
		if t.Socket == "" {
			return nil, &DialError{ErrNoSocket}
		}
		var d net.Dialer
		c, err := d.DialContext(ctx, "unix", t.Socket)
		if err != nil {
			return nil, &DialError{err}
		}
		return c, nil
	}
	u, err := url.Parse(t.URL)
	if err != nil {
		return nil, &DialError{err}
	}
	addr := u.Host
	if u.Port() == "" {
		port := "80"
		if u.Scheme == "https" {
			port = "443"
		}
		addr = net.JoinHostPort(u.Hostname(), port)
	}
	d := net.Dialer{Timeout: dialTimeout}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, &DialError{err}
	}
	if u.Scheme != "https" {
		return c, nil
	}
	tc := tls.Client(c, &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12})
	hctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	if err := tc.HandshakeContext(hctx); err != nil {
		_ = c.Close()
		return nil, &DialError{err}
	}
	return tc, nil
}

// Transport is an HTTP transport to t: every connection goes to t (the
// socket, or the remote host with TLS for https), never through
// HTTP_PROXY and the like, with no response header timeout (event streams
// idle between pings).
func (t Target) Transport() *http.Transport {
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) { return t.Dial(ctx) }
	return &http.Transport{
		Proxy:           nil,
		DialContext:     dial,
		DialTLSContext:  dial,
		MaxIdleConns:    16,
		IdleConnTimeout: 90 * time.Second,
	}
}

// NormalizeURL checks a remote server URL and returns its canonical form:
// http or https, a host, no credentials, path, query or fragment;
// lowercase scheme and host, no trailing slash, default ports dropped.
func NormalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("enter the server URL, e.g. http://192.168.1.20:7380")
	}
	if !strings.Contains(raw, "://") {
		return "", fmt.Errorf("%q: add the scheme, e.g. http://%s", raw, raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%q is not a URL", raw)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("%q: use http:// or https://", raw)
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case host == "":
		return "", fmt.Errorf("%q has no host", raw)
	case u.User != nil:
		return "", fmt.Errorf("%q: credentials in the URL are not supported", raw)
	case u.Path != "" && u.Path != "/", u.RawQuery != "", u.Fragment != "", u.Opaque != "":
		return "", fmt.Errorf("%q: give only the server address (scheme, host and port), without a path", raw)
	}
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]" // IPv6 literal
	}
	return scheme + "://" + host, nil
}
