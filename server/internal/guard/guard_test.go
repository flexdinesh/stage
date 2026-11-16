package guard

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// servers runs the guarded handler on a TCP and a unix listener (so the
// listener network reaches the policy as in production) and returns
// clients that send requests to each.
func servers(t *testing.T, origins []string) (tcp, unix func(*http.Request) int) {
	t.Helper()
	p, err := New(origins, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h := p.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(299) }))
	tl, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "grd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	ul, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h}
	go func() { _ = srv.Serve(tl) }()
	go func() { _ = srv.Serve(ul) }()
	t.Cleanup(func() { _ = srv.Close() })
	tcpClient := &http.Client{}
	unixClient := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}}
	send := func(c *http.Client, base string) func(*http.Request) int {
		return func(r *http.Request) int {
			u := *r.URL
			b, _ := http.NewRequest("GET", base+u.RequestURI(), nil)
			r.URL = b.URL
			res, err := c.Do(r)
			if err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()
			return res.StatusCode
		}
	}
	return send(tcpClient, "http://"+tl.Addr().String()), send(unixClient, "http://stage")
}

type req struct {
	method, host, origin, site, ctype, body string
}

func (q req) build(t *testing.T) *http.Request {
	t.Helper()
	var body io.Reader
	if q.body != "" {
		body = strings.NewReader(q.body)
	}
	m := q.method
	if m == "" {
		m = "GET"
	}
	r, err := http.NewRequest(m, "http://x/api/tasks", body)
	if err != nil {
		t.Fatal(err)
	}
	r.Host = q.host
	for k, v := range map[string]string{"Origin": q.origin, "Sec-Fetch-Site": q.site, "Content-Type": q.ctype} {
		if v != "" {
			r.Header.Set(k, v)
		}
	}
	return r
}

const ok = 299

func TestPolicy(t *testing.T) {
	tcp, unix := servers(t, []string{"https://m.tail.ts.net", "http://stage.lan:7380"})
	post := func(q req) req { q.method, q.ctype, q.body = "POST", "application/json", `{}`; return q }
	for _, c := range []struct {
		name      string
		r         req
		tcp, unix int
	}{
		// Allowed clients.
		{"go client tcp", post(req{host: "127.0.0.1:7380"}), ok, ok},
		{"go client unix", post(req{host: "stage"}), 403, ok},
		{"bridge B (desktop webview)", post(req{host: "stage.invalid", origin: DesktopOrigin, site: "same-origin"}), 403, ok},
		{"bridge B without fetch metadata", post(req{host: "stage.invalid", origin: DesktopOrigin}), 403, ok},
		{"PWA same origin", post(req{host: "localhost:7380", origin: "http://localhost:7380", site: "same-origin"}), ok, ok},
		{"browser at IPv6 loopback", post(req{host: "[::1]:7380", origin: "http://[::1]:7380", site: "same-origin"}), ok, ok},
		{"LAN IP literal", post(req{host: "192.168.1.5:7380", origin: "http://192.168.1.5:7380", site: "same-origin"}), ok, ok},
		{"vite dev proxy", post(req{host: "localhost:5173", origin: "http://localhost:5173", site: "same-origin"}), ok, ok},
		{"proxy preserving Host", post(req{host: "m.tail.ts.net", origin: "https://m.tail.ts.net", site: "same-origin"}), ok, ok},
		{"proxy rewriting Host to loopback", post(req{host: "127.0.0.1:7380", origin: "https://m.tail.ts.net", site: "same-origin"}), ok, ok},
		{"proxy rewriting Host, old browser", post(req{host: "127.0.0.1:7380", origin: "https://m.tail.ts.net"}), ok, ok},
		{"allowed host, any port", post(req{host: "stage.lan:9999", origin: "http://stage.lan:9999"}), ok, ok},
		{"HTTP MCP client (no Origin)", post(req{host: "127.0.0.1:7380"}), ok, ok},
		{"navigation", req{host: "localhost:7380", site: "none"}, ok, ok},
		{"empty-body POST without type", req{method: "POST", host: "127.0.0.1"}, ok, ok},
		{"multipart upload", req{method: "POST", host: "127.0.0.1", ctype: "multipart/form-data; boundary=x", body: "--x--"}, ok, ok},
		{"json with charset", req{method: "PATCH", host: "127.0.0.1", ctype: "application/json; charset=utf-8", body: "{}"}, ok, ok},

		// Rejected.
		{"DNS rebinding Host", req{host: "evil.example:7380", site: "same-origin"}, 403, 403},
		{"rebinding POST", post(req{host: "evil.example:7380", origin: "http://evil.example:7380", site: "same-origin"}), 403, 403},
		{"cross-site POST", post(req{host: "127.0.0.1:7380", origin: "https://evil.example", site: "cross-site"}), 403, 403},
		{"cross-site POST, old browser", post(req{host: "127.0.0.1:7380", origin: "https://evil.example"}), 403, 403},
		{"cross-site GET embed", req{host: "127.0.0.1:7380", site: "cross-site"}, 403, 403},
		{"same-site GET", req{host: "localhost:7380", site: "same-site", origin: "http://localhost:5174"}, 403, 403},
		{"desktop origin over TCP", post(req{host: "127.0.0.1:7380", origin: DesktopOrigin}), 403, ok},
		{"desktop origin cross-site over TCP", req{host: "127.0.0.1:7380", origin: DesktopOrigin, site: "cross-site"}, 403, ok},
		{"text/plain JSON (form CSRF)", req{method: "POST", host: "127.0.0.1", ctype: "text/plain", body: "{}"}, 415, 415},
		{"urlencoded body", req{method: "POST", host: "127.0.0.1", ctype: "application/x-www-form-urlencoded", body: "a=b"}, 415, 415},
		{"body without type", req{method: "DELETE", host: "127.0.0.1", body: "{}"}, 415, 415},
	} {
		if got := tcp(c.r.build(t)); got != c.tcp {
			t.Errorf("%s over tcp: %d, want %d", c.name, got, c.tcp)
		}
		host := c.r.host
		if host == "127.0.0.1:7380" && c.unix == ok && c.tcp == ok {
			host = "stage" // what socket clients send; loopback works too
		}
		r := c.r
		r.host = host
		if got := unix(r.build(t)); got != c.unix {
			t.Errorf("%s over unix: %d, want %d", c.name, got, c.unix)
		}
	}
}

func TestNewRejectsBadOrigin(t *testing.T) {
	if _, err := New([]string{"not an origin"}, nil); err == nil {
		t.Fatal("bad origin accepted")
	}
}

// HTTP/1.0 requests may carry no Host at all.
func TestEmptyHostRejected(t *testing.T) {
	p, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.hostAllowed("", false) || p.hostAllowed("", true) {
		t.Fatal("empty host allowed")
	}
}
