package bridge_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/flexdinesh/stage/desktop/app/bridge"
	"github.com/flexdinesh/stage/desktop/app/procnet"
	"github.com/flexdinesh/stage/desktop/app/target"
)

// stub is a minimal Stage-like server for the proxy: health, an upload
// echo, an attachment, a redirect, a slow request and an event stream,
// with counters of the open slow requests and streams.
type stub struct {
	slow, streams atomic.Int32
	events        chan int // ids to emit on open streams
	lastHost      atomic.Pointer[string]
}

func (s *stub) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		h := r.Host
		s.lastHost.Store(&h)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok","service":"stage"}`)
	})
	mux.HandleFunc("POST /api/uploads", func(w http.ResponseWriter, r *http.Request) {
		f, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		b, _ := io.ReadAll(f)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]int{"id": 1, "sizeBytes": len(b)})
	})
	mux.HandleFunc("POST /api/echo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.Copy(w, r.Body)
	})
	mux.HandleFunc("GET /api/attachments/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Set-Cookie", "a=b")
		_, _ = w.Write(pngBytes)
	})
	mux.HandleFunc("GET /api/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/", http.StatusFound)
	})
	mux.HandleFunc("GET /api/slow", func(w http.ResponseWriter, r *http.Request) {
		s.slow.Add(1)
		defer s.slow.Add(-1)
		<-r.Context().Done()
	})
	mux.HandleFunc("GET /api/stream", func(w http.ResponseWriter, r *http.Request) {
		s.streams.Add(1)
		defer s.streams.Add(-1)
		after := -1
		if v := r.Header.Get("Last-Event-ID"); v != "" {
			after, _ = strconv.Atoi(v)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, "retry: 3000\n\n")
		w.(http.Flusher).Flush()
		for {
			select {
			case <-r.Context().Done():
				return
			case id := <-s.events:
				if id <= after {
					continue
				}
				fmt.Fprintf(w, "event: item\nid: %d\ndata: {}\n\n", id)
				w.(http.Flusher).Flush()
			}
		}
	})
	return mux
}

// shortDir is a temp dir short enough for Unix socket paths.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "sb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// serve runs the stub on sock until the test ends or stop is called.
func serve(t *testing.T, st *stub, sock string) (stop func()) {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: st.handler()}
	go func() { _ = srv.Serve(ln) }()
	var once sync.Once
	stop = func() { once.Do(func() { _ = srv.Close() }) }
	t.Cleanup(stop)
	return stop
}

func startStub(t *testing.T) (*stub, string) {
	t.Helper()
	st := &stub{events: make(chan int, 16)}
	sock := filepath.Join(shortDir(t), "s.sock")
	serve(t, st, sock)
	return st, sock
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPermit(t *testing.T) {
	cases := []struct {
		method, rawURL string
		status         int // 0: permitted
	}{
		{"GET", "mygo://localhost/api/health", 0},
		{"POST", "mygo://localhost/api/uploads", 0},
		{"PATCH", "mygo://localhost/api/snippets/1", 0},
		{"DELETE", "mygo://localhost/api/snippets/1", 0},
		{"PUT", "mygo://localhost/api/preferences/pinned", 0},
		{"GET", "mygo://localhost/api/threads/a%20b/transcript/stream?after=3", 0},
		{"HEAD", "mygo://localhost/api/health", http.StatusMethodNotAllowed},
		{"OPTIONS", "mygo://localhost/api/health", http.StatusMethodNotAllowed},
		{"TRACE", "mygo://localhost/api/health", http.StatusMethodNotAllowed},
		{"CONNECT", "mygo://localhost/api/health", http.StatusMethodNotAllowed},
		{"GET", "mygo://localhost/", http.StatusNotFound},
		{"GET", "mygo://localhost/api", http.StatusNotFound},
		{"GET", "mygo://localhost/api/", http.StatusNotFound},
		{"GET", "mygo://localhost/mcp", http.StatusNotFound},
		{"GET", "mygo://localhost/api/../mcp", http.StatusNotFound},
		{"GET", "mygo://localhost/api/./health", http.StatusNotFound},
		{"GET", "mygo://localhost/api//health", http.StatusNotFound},
		{"GET", "mygo://localhost/api/health/", http.StatusNotFound},
		{"GET", "mygo://localhost/api/a%2f..%2fmcp", http.StatusNotFound},
		{"GET", "mygo://localhost/api/a%5cb", http.StatusNotFound},
		{"GET", "mygo://localhost/apix/health", http.StatusNotFound},
	}
	for _, c := range cases {
		u, err := url.Parse(c.rawURL)
		if err != nil {
			t.Fatal(err)
		}
		err = bridge.Permit(c.method, u)
		var d *bridge.Denial
		switch {
		case c.status == 0 && err != nil:
			t.Errorf("%s %s: denied (%v), want permitted", c.method, c.rawURL, err)
		case c.status != 0 && !errors.As(err, &d):
			t.Errorf("%s %s: permitted, want %d", c.method, c.rawURL, c.status)
		case c.status != 0 && d.Status != c.status:
			t.Errorf("%s %s: status %d, want %d", c.method, c.rawURL, d.Status, c.status)
		}
	}
}

func TestSchemeRouting(t *testing.T) {
	st, sock := startStub(t)
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:9")
	static := http.FileServerFS(fstest.MapFS{"index.html": {Data: []byte("<!doctype html>app")}})
	h := bridge.Scheme(bridge.NewAPIProxy(target.Local(sock)), static)
	cases := []struct {
		method, target string
		status         int
		contains       string
	}{
		{"GET", "mygo://localhost/", 200, "app"},
		{"GET", "mygo://localhost/api/health", 200, `"status":"ok"`},
		{"POST", "mygo://localhost/", 405, "read-only"},
		{"PROPFIND", "mygo://localhost/api/health", 405, "denied"},
		{"GET", "mygo://localhost/api", 404, "denied"},
		{"GET", "mygo://localhost/api//health", 404, "denied"},
		{"GET", "mygo://localhost/api/redirect", 502, "redirect"},
		{"GET", "mygo://localhost/api/nope", 404, "not found"},
	}
	for _, c := range cases {
		rec := newStreamRecorder()
		h.ServeHTTP(rec, httpRequest(t, context.Background(), c.method, c.target, nil))
		status, body := rec.result()
		if status != c.status || !strings.Contains(body, c.contains) {
			t.Errorf("%s %s: %d %q, want %d containing %q", c.method, c.target, status, body, c.status, c.contains)
		}
	}
	if h := st.lastHost.Load(); h == nil || *h != bridge.Host {
		t.Errorf("proxied Host %v, want %s", h, bridge.Host)
	}
}

// TestStaticCSP: every static response carries the CSP; API responses
// keep the server's headers.
func TestStaticCSP(t *testing.T) {
	_, sock := startStub(t)
	static := http.FileServerFS(fstest.MapFS{"index.html": {Data: []byte("<!doctype html>app")}, "a.js": {Data: []byte("1")}})
	h := bridge.Scheme(bridge.NewAPIProxy(target.Local(sock)), static)
	for _, target := range []string{"mygo://localhost/", "mygo://localhost/a.js", "mygo://localhost/missing.css"} {
		rec := newStreamRecorder()
		h.ServeHTTP(rec, httpRequest(t, context.Background(), "GET", target, nil))
		rec.result()
		if got := rec.sentHeader().Get("Content-Security-Policy"); got != bridge.CSP {
			t.Errorf("%s: CSP %q", target, got)
		}
		if got := rec.sentHeader().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options %q", target, got)
		}
	}
	rec := newStreamRecorder()
	h.ServeHTTP(rec, httpRequest(t, context.Background(), "GET", "mygo://localhost/api/health", nil))
	rec.result()
	if got := rec.sentHeader().Get("Content-Security-Policy"); got != "" {
		t.Errorf("API response got the frontend CSP %q", got)
	}
	for _, d := range []string{"default-src 'self' mygo://localhost", "object-src 'none'", "frame-src 'none'", "base-uri 'none'", "form-action 'none'", "img-src 'self' mygo://localhost blob: data:"} {
		if !strings.Contains(bridge.CSP, d) {
			t.Errorf("CSP lacks %q", d)
		}
	}
}

func TestProxyUploadAndAttachment(t *testing.T) {
	_, sock := startStub(t)
	h := bridge.Scheme(bridge.NewAPIProxy(target.Local(sock)), http.NotFoundHandler())
	body, ctype := multipartPNG(t, "dot.png", pngBytes)
	r := httpRequest(t, context.Background(), "POST", "mygo://localhost/api/uploads", body)
	r.Header.Set("Content-Type", ctype)
	rec := newStreamRecorder()
	h.ServeHTTP(rec, r)
	status, out := rec.result()
	if status != http.StatusCreated || !strings.Contains(out, fmt.Sprintf(`"sizeBytes":%d`, len(pngBytes))) {
		t.Fatalf("upload: %d %s", status, out)
	}
	rec = newStreamRecorder()
	h.ServeHTTP(rec, httpRequest(t, context.Background(), "GET", "mygo://localhost/api/attachments/1", nil))
	status, out = rec.result()
	if status != 200 || out != string(pngBytes) {
		t.Fatalf("attachment: %d, %d bytes", status, len(out))
	}
	if c := rec.sentHeader().Get("Set-Cookie"); c != "" {
		t.Errorf("Set-Cookie passed through: %q", c)
	}
}

// TestProxyBodyWithoutLength: MyGo hands over a body as an io.NopCloser
// and knows its length only from a Content-Length header, which macOS
// WKWebView does not send. The body must still reach the server.
func TestProxyBodyWithoutLength(t *testing.T) {
	_, sock := startStub(t)
	h := bridge.Scheme(bridge.NewAPIProxy(target.Local(sock)), http.NotFoundHandler())
	myGoRequest := func(path string, body []byte, ctype string) *http.Request {
		r := httpRequest(t, context.Background(), "POST", "mygo://localhost"+path, io.NopCloser(bytes.NewReader(body)))
		r.Header.Set("Content-Type", ctype)
		return r
	}
	const link = `{"link":"http://127.0.0.1:49374/auth/connect/abc"}`
	rec := newStreamRecorder()
	h.ServeHTTP(rec, myGoRequest("/api/echo", []byte(link), "application/json"))
	if status, out := rec.result(); status != 200 || out != link {
		t.Fatalf("json: %d %q, want %q", status, out, link)
	}
	upload, ctype := multipartPNG(t, "dot.png", pngBytes)
	raw, err := io.ReadAll(upload)
	if err != nil {
		t.Fatal(err)
	}
	rec = newStreamRecorder()
	h.ServeHTTP(rec, myGoRequest("/api/uploads", raw, ctype))
	if status, out := rec.result(); status != http.StatusCreated || !strings.Contains(out, fmt.Sprintf(`"sizeBytes":%d`, len(pngBytes))) {
		t.Fatalf("upload: %d %s", status, out)
	}
}

// TestProxyNotReady: no socket yet → 503 starting; a dead socket → 503
// unavailable; setting the socket makes requests work.
func TestProxyNotReady(t *testing.T) {
	p := bridge.NewAPIProxy(target.Target{})
	h := bridge.Scheme(p, http.NotFoundHandler())
	get := func() (int, string) {
		rec := newStreamRecorder()
		h.ServeHTTP(rec, httpRequest(t, context.Background(), "GET", "mygo://localhost/api/health", nil))
		return rec.result()
	}
	if status, body := get(); status != 503 || !strings.Contains(body, `"code":"starting"`) {
		t.Fatalf("unset socket: %d %s", status, body)
	}
	p.SetTarget(target.Local(filepath.Join(shortDir(t), "none.sock")))
	if status, body := get(); status != 503 || !strings.Contains(body, `"code":"unavailable"`) {
		t.Fatalf("dead socket: %d %s", status, body)
	}
	_, sock := startStub(t)
	p.SetTarget(target.Local(sock))
	if p.Target().Socket != sock {
		t.Fatalf("Target() = %+v", p.Target())
	}
	if status, body := get(); status != 200 {
		t.Fatalf("after SetSocket: %d %s", status, body)
	}
}

// TestProxySSE: events reach the page as they are emitted, a reconnect
// with Last-Event-ID resumes, and the page going away ends the stream on
// the server.
func TestProxySSE(t *testing.T) {
	st, sock := startStub(t)
	h := bridge.Scheme(bridge.NewAPIProxy(target.Local(sock)), http.NotFoundHandler())

	ctx, cancel := context.WithCancel(context.Background())
	rec := newStreamRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(rec, sseRequest(t, ctx, "mygo://localhost/api/stream", ""))
	}()
	waitFor(t, "stream open", func() bool { return st.streams.Load() == 1 })
	waitFor(t, "stream headers", func() bool { return rec.sentHeader() != nil })
	if got := rec.sentHeader().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type %q", got)
	}
	for i := range 3 {
		st.events <- i
	}
	evs := rec.events(t, 3)
	for i, ev := range evs {
		if ev.id != strconv.Itoa(i) {
			t.Fatalf("event %d = %+v", i, ev)
		}
	}
	cancel()
	<-done
	waitFor(t, "stream closed on the server", func() bool { return st.streams.Load() == 0 })

	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	rec = newStreamRecorder()
	go h.ServeHTTP(rec, sseRequest(t, ctx, "mygo://localhost/api/stream", "2"))
	waitFor(t, "stream reopened", func() bool { return st.streams.Load() == 1 })
	for _, id := range []int{1, 2, 3, 4} {
		st.events <- id
	}
	resumed := rec.events(t, 2)
	if resumed[0].id != "3" || resumed[1].id != "4" {
		t.Fatalf("resumed %+v, want ids 3, 4", resumed)
	}
}

// TestProxySSEWaitsForServer: with the server gone, an event stream gets
// no HTTP error; it waits without headers and proxies once the server is
// back. A wait that runs out answers an empty 200 event stream with a
// retry, which EventSource reconnects from.
func TestProxySSEWaitsForServer(t *testing.T) {
	st := &stub{events: make(chan int, 16)}
	sock := filepath.Join(shortDir(t), "s.sock")
	stop := serve(t, st, sock)
	p := bridge.NewAPIProxy(target.Local(sock))
	h := bridge.Scheme(p, http.NotFoundHandler())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := newStreamRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(rec, sseRequest(t, ctx, "mygo://localhost/api/stream", ""))
	}()
	waitFor(t, "stream open", func() bool { return st.streams.Load() == 1 })
	stop() // the server dies mid-stream
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not end when the server went away")
	}
	if status, _ := rec.result(); status != 200 {
		t.Fatalf("stream status %d", status)
	}
	_ = os.Remove(sock)

	// The reconnect waits without headers while the socket is dead.
	rec = newStreamRecorder()
	done = make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(rec, sseRequest(t, ctx, "mygo://localhost/api/stream", "0"))
	}()
	time.Sleep(400 * time.Millisecond)
	if rec.sentHeader() != nil {
		t.Fatalf("headers sent while the server is down: %v", rec.sentHeader())
	}
	st2 := &stub{events: make(chan int, 16)}
	serve(t, st2, sock)
	waitFor(t, "stream reopened on the new server", func() bool { return st2.streams.Load() == 1 })
	st2.events <- 0
	st2.events <- 1
	if evs := rec.events(t, 1); evs[0].id != "1" {
		t.Fatalf("resumed %+v, want id 1", evs)
	}

	// A wait that runs out: an empty stream with a retry, never an error.
	p.SetTarget(target.Local(filepath.Join(shortDir(t), "none.sock")))
	p.StreamWait = 200 * time.Millisecond
	rec = newStreamRecorder()
	h.ServeHTTP(rec, sseRequest(t, context.Background(), "mygo://localhost/api/stream", ""))
	status, body := rec.result()
	if status != 200 || rec.sentHeader().Get("Content-Type") != "text/event-stream" || !strings.Contains(body, "retry:") {
		t.Fatalf("expired wait: %d %q %q", status, rec.sentHeader().Get("Content-Type"), body)
	}
	p.SetTarget(target.Target{})
	rec = newStreamRecorder()
	h.ServeHTTP(rec, sseRequest(t, context.Background(), "mygo://localhost/api/stream", ""))
	if status, body := rec.result(); status != 200 || !strings.Contains(body, "retry:") {
		t.Fatalf("unset socket stream: %d %q", status, body)
	}
}

// TestProxyCancel: a request the page aborts ends on the server.
func TestProxyCancel(t *testing.T) {
	st, sock := startStub(t)
	h := bridge.Scheme(bridge.NewAPIProxy(target.Local(sock)), http.NotFoundHandler())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(newStreamRecorder(), httpRequest(t, ctx, "GET", "mygo://localhost/api/slow", nil))
	}()
	waitFor(t, "slow request open", func() bool { return st.slow.Load() == 1 })
	cancel()
	<-done
	waitFor(t, "slow request ended on the server", func() bool { return st.slow.Load() == 0 })
}

// TestNoTCPListener: serving the bridge in this process opens no TCP
// listener.
func TestNoTCPListener(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("procnet is implemented on Linux and macOS")
	}
	_, sock := startStub(t)
	rec := newStreamRecorder()
	bridge.Scheme(bridge.NewAPIProxy(target.Local(sock)), http.NotFoundHandler()).ServeHTTP(rec, httpRequest(t, context.Background(), "GET", "mygo://localhost/api/health", nil))
	if status, _ := rec.result(); status != 200 {
		t.Fatalf("scheme health: %d", status)
	}
	addrs, err := procnet.ListeningTCP(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if len(addrs) != 0 {
		t.Fatalf("TCP listeners: %v", addrs)
	}
}

// pngBytes is a 1x1 PNG.
var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\xcf\xc0\xf0\x1f\x00\x05\x00\x01\xff\x89\x99=\x1d\x00\x00\x00\x00IEND\xaeB`\x82")

func multipartPNG(t *testing.T, name string, data []byte) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, mw.FormDataContentType()
}

func httpRequest(t *testing.T, ctx context.Context, method, target string, body io.Reader) *http.Request {
	t.Helper()
	r, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		t.Fatal(err)
	}
	r.RequestURI = r.URL.RequestURI()
	return r
}

func sseRequest(t *testing.T, ctx context.Context, target, lastID string) *http.Request {
	t.Helper()
	r := httpRequest(t, ctx, "GET", target, nil)
	r.Header.Set("Accept", "text/event-stream")
	if lastID != "" {
		r.Header.Set("Last-Event-ID", lastID)
	}
	return r
}

// streamRecorder is a concurrent http.ResponseWriter and http.Flusher, as
// MyGo's scheme writer is: it records only what was flushed, so a test
// sees what a page would see while the response is still open.
type streamRecorder struct {
	mu      sync.Mutex
	header  http.Header
	sent    http.Header // header as of WriteHeader
	status  int
	pending bytes.Buffer
	flushed bytes.Buffer
	changed chan struct{}
}

func newStreamRecorder() *streamRecorder {
	return &streamRecorder{header: http.Header{}, changed: make(chan struct{}, 1)}
}

func (r *streamRecorder) Header() http.Header { return r.header }

func (r *streamRecorder) WriteHeader(status int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status == 0 {
		r.status = status
		r.sent = r.header.Clone()
	}
}

func (r *streamRecorder) Write(p []byte) (int, error) {
	r.WriteHeader(http.StatusOK)
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pending.Write(p)
}

func (r *streamRecorder) Flush() {
	r.WriteHeader(http.StatusOK)
	r.mu.Lock()
	_, _ = r.pending.WriteTo(&r.flushed)
	r.mu.Unlock()
	select {
	case r.changed <- struct{}{}:
	default:
	}
}

// result flushes and returns the status and whole body, once the handler
// returned.
func (r *streamRecorder) result() (int, string) {
	r.Flush()
	return r.status, r.body()
}

// sentHeader is the header the handler sent, nil before WriteHeader.
func (r *streamRecorder) sentHeader() http.Header {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sent
}

func (r *streamRecorder) body() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.flushed.String()
}

type sseEvent struct{ event, id, data string }

// events waits until n item events were flushed and returns them.
func (r *streamRecorder) events(t *testing.T, n int) []sseEvent {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		evs := parseSSE(r.body())
		if len(evs) >= n {
			return evs[:n]
		}
		select {
		case <-r.changed:
		case <-deadline:
			t.Fatalf("got %d events, want %d: %q", len(evs), n, r.body())
		}
	}
}

func parseSSE(s string) []sseEvent {
	var out []sseEvent
	for _, block := range strings.Split(s, "\n\n") {
		var ev sseEvent
		sc := bufio.NewScanner(strings.NewReader(block))
		for sc.Scan() {
			k, v, _ := strings.Cut(sc.Text(), ": ")
			switch k {
			case "event":
				ev.event = v
			case "id":
				ev.id = v
			case "data":
				ev.data = v
			}
		}
		if ev.event == "item" {
			out = append(out, ev)
		}
	}
	return out
}

// headerRecorder wraps a handler and keeps the last request's Host and
// headers.
type headerRecorder struct {
	h    http.Handler
	mu   sync.Mutex
	host string
	hdr  http.Header
	n    int
}

func (hr *headerRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hr.mu.Lock()
	hr.host, hr.hdr = r.Host, r.Header.Clone()
	hr.n++
	hr.mu.Unlock()
	hr.h.ServeHTTP(w, r)
}

func (hr *headerRecorder) last() (string, http.Header, int) {
	hr.mu.Lock()
	defer hr.mu.Unlock()
	return hr.host, hr.hdr, hr.n
}

// remoteStub runs the stub over TCP (httptest) as a remote target.
func remoteStub(t *testing.T) (*stub, *headerRecorder, target.Target) {
	t.Helper()
	st := &stub{events: make(chan int, 16)}
	hr := &headerRecorder{h: st.handler()}
	srv := httptest.NewServer(hr)
	t.Cleanup(srv.Close)
	u, err := target.NormalizeURL(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return st, hr, target.Target{ID: "s-remote", Name: "Remote", Kind: target.KindRemote, URL: u}
}

// TestProxyRemoteTCP: a remote target gets the request over TCP with its
// own Host, and without the page's Cookie, Origin and Sec-Fetch-*
// headers (a non-browser client to its origin policy); HTTP_PROXY never
// applies.
func TestProxyRemoteTCP(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:9")
	_, hr, rt := remoteStub(t)
	h := bridge.Scheme(bridge.NewAPIProxy(rt), http.NotFoundHandler())
	body, ctype := multipartPNG(t, "dot.png", pngBytes)
	r := httpRequest(t, context.Background(), "POST", "mygo://localhost/api/uploads", body)
	r.Header.Set("Content-Type", ctype)
	r.Header.Set("Origin", bridge.PageOrigin)
	r.Header.Set("Cookie", "a=b")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("Sec-Fetch-Mode", "cors")
	r.Header.Set("Sec-Fetch-Dest", "empty")
	rec := newStreamRecorder()
	h.ServeHTTP(rec, r)
	if status, out := rec.result(); status != http.StatusCreated {
		t.Fatalf("remote upload: %d %s", status, out)
	}
	host, hdr, _ := hr.last()
	if host != rt.Host() {
		t.Errorf("Host %q, want %q", host, rt.Host())
	}
	for _, k := range []string{"Origin", "Cookie", "Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-Dest"} {
		if v := hdr.Get(k); v != "" {
			t.Errorf("%s forwarded: %q", k, v)
		}
	}
	if hdr.Get("Content-Type") != ctype {
		t.Errorf("Content-Type %q", hdr.Get("Content-Type"))
	}
}

// TestProxyPageOrigin: the bridge enforces the page origin itself (it
// strips Origin before the server): cross-site requests and unsafe
// methods from another origin never reach the server.
func TestProxyPageOrigin(t *testing.T) {
	_, hr, rt := remoteStub(t)
	h := bridge.Scheme(bridge.NewAPIProxy(rt), http.NotFoundHandler())
	cases := []struct {
		method, origin, site string
		status               int
	}{
		{"POST", "https://evil.example", "", 403},
		{"POST", "null", "", 403},
		{"DELETE", "http://localhost:5174", "", 403},
		{"GET", "https://evil.example", "cross-site", 403},
		{"GET", "", "same-site", 403},
		{"GET", "", "", 200},
		{"GET", "https://evil.example", "", 200}, // a GET's Origin alone is not a cross-site signal
		{"POST", bridge.PageOrigin, "same-origin", 404},
		{"POST", "", "", 404}, // no Origin: WebKit may omit it
	}
	for _, c := range cases {
		path := "mygo://localhost/api/health"
		if c.method != "GET" {
			path = "mygo://localhost/api/nope"
		}
		r := httpRequest(t, context.Background(), c.method, path, strings.NewReader("{}"))
		r.Header.Set("Content-Type", "application/json")
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if c.site != "" {
			r.Header.Set("Sec-Fetch-Site", c.site)
		}
		_, _, before := hr.last()
		rec := newStreamRecorder()
		h.ServeHTTP(rec, r)
		status, body := rec.result()
		if status != c.status {
			t.Errorf("%s origin %q site %q: %d %s, want %d", c.method, c.origin, c.site, status, body, c.status)
		}
		_, _, after := hr.last()
		if c.status == 403 && (after != before || !strings.Contains(body, `"code":"denied"`)) {
			t.Errorf("%s origin %q site %q reached the server or was not denied: %s", c.method, c.origin, c.site, body)
		}
	}
}

// TestProxySwitchCancels: switching servers ends the old server's open
// streams and requests at once (a JSON request in flight gets 503
// unavailable); the reconnecting stream lands on the new server.
func TestProxySwitchCancels(t *testing.T) {
	local, sock := startStub(t)
	remote, _, rt := remoteStub(t)
	p := bridge.NewAPIProxy(target.Local(sock))
	h := bridge.Scheme(p, http.NotFoundHandler())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := newStreamRecorder()
	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		h.ServeHTTP(stream, sseRequest(t, ctx, "mygo://localhost/api/stream", ""))
	}()
	slow := newStreamRecorder()
	slowDone := make(chan struct{})
	go func() {
		defer close(slowDone)
		h.ServeHTTP(slow, httpRequest(t, ctx, "GET", "mygo://localhost/api/slow", nil))
	}()
	waitFor(t, "local stream and slow request open", func() bool { return local.streams.Load() == 1 && local.slow.Load() == 1 })

	p.SetTarget(rt)
	for name, done := range map[string]chan struct{}{"stream": streamDone, "slow request": slowDone} {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatalf("%s did not end on the switch", name)
		}
	}
	waitFor(t, "old server's requests closed", func() bool { return local.streams.Load() == 0 && local.slow.Load() == 0 })
	if status, body := slow.result(); status != 503 || !strings.Contains(body, `"code":"unavailable"`) {
		t.Errorf("in-flight request after switch: %d %s", status, body)
	}
	if status, _ := stream.result(); status != 200 {
		t.Errorf("stream status %d", status)
	}

	// EventSource reconnects: the new stream is the remote's.
	again := newStreamRecorder()
	go h.ServeHTTP(again, sseRequest(t, ctx, "mygo://localhost/api/stream", ""))
	waitFor(t, "stream on the new server", func() bool { return remote.streams.Load() == 1 })
	remote.events <- 7
	if evs := again.events(t, 1); evs[0].id != "7" {
		t.Fatalf("events %+v", evs)
	}
	// The same target again keeps open streams.
	p.SetTarget(rt)
	time.Sleep(50 * time.Millisecond)
	if remote.streams.Load() != 1 {
		t.Fatal("SetTarget with the same target ended the stream")
	}
}

// TestProxyRemoteUnreachable: a remote that refuses connections maps to
// 503 unavailable for JSON and to the retry stream for EventSource.
func TestProxyRemoteUnreachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	p := bridge.NewAPIProxy(target.Target{ID: "s-dead", Name: "Dead", Kind: target.KindRemote, URL: "http://" + addr})
	p.StreamWait = 300 * time.Millisecond
	h := bridge.Scheme(p, http.NotFoundHandler())
	rec := newStreamRecorder()
	h.ServeHTTP(rec, httpRequest(t, context.Background(), "GET", "mygo://localhost/api/health", nil))
	if status, body := rec.result(); status != 503 || !strings.Contains(body, `"code":"unavailable"`) {
		t.Fatalf("dead remote JSON: %d %s", status, body)
	}
	rec = newStreamRecorder()
	h.ServeHTTP(rec, sseRequest(t, context.Background(), "mygo://localhost/api/stream", ""))
	if status, body := rec.result(); status != 200 || !strings.Contains(body, "retry:") {
		t.Fatalf("dead remote stream: %d %q", status, body)
	}
}
