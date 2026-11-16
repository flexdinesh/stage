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
	h := bridge.Scheme(bridge.NewAPIProxy(sock), static)
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
	h := bridge.Scheme(bridge.NewAPIProxy(sock), static)
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
	h := bridge.Scheme(bridge.NewAPIProxy(sock), http.NotFoundHandler())
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

// TestProxyNotReady: no socket yet → 503 starting; a dead socket → 503
// unavailable; setting the socket makes requests work.
func TestProxyNotReady(t *testing.T) {
	p := bridge.NewAPIProxy("")
	h := bridge.Scheme(p, http.NotFoundHandler())
	get := func() (int, string) {
		rec := newStreamRecorder()
		h.ServeHTTP(rec, httpRequest(t, context.Background(), "GET", "mygo://localhost/api/health", nil))
		return rec.result()
	}
	if status, body := get(); status != 503 || !strings.Contains(body, `"code":"starting"`) {
		t.Fatalf("unset socket: %d %s", status, body)
	}
	p.SetSocket(filepath.Join(shortDir(t), "none.sock"))
	if status, body := get(); status != 503 || !strings.Contains(body, `"code":"unavailable"`) {
		t.Fatalf("dead socket: %d %s", status, body)
	}
	_, sock := startStub(t)
	p.SetSocket(sock)
	if p.Socket() != sock {
		t.Fatalf("Socket() = %q", p.Socket())
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
	h := bridge.Scheme(bridge.NewAPIProxy(sock), http.NotFoundHandler())

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
	p := bridge.NewAPIProxy(sock)
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
	p.SetSocket(filepath.Join(shortDir(t), "none.sock"))
	p.StreamWait = 200 * time.Millisecond
	rec = newStreamRecorder()
	h.ServeHTTP(rec, sseRequest(t, context.Background(), "mygo://localhost/api/stream", ""))
	status, body := rec.result()
	if status != 200 || rec.sentHeader().Get("Content-Type") != "text/event-stream" || !strings.Contains(body, "retry:") {
		t.Fatalf("expired wait: %d %q %q", status, rec.sentHeader().Get("Content-Type"), body)
	}
	p.SetSocket("")
	rec = newStreamRecorder()
	h.ServeHTTP(rec, sseRequest(t, context.Background(), "mygo://localhost/api/stream", ""))
	if status, body := rec.result(); status != 200 || !strings.Contains(body, "retry:") {
		t.Fatalf("unset socket stream: %d %q", status, body)
	}
}

// TestProxyCancel: a request the page aborts ends on the server.
func TestProxyCancel(t *testing.T) {
	st, sock := startStub(t)
	h := bridge.Scheme(bridge.NewAPIProxy(sock), http.NotFoundHandler())
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
	bridge.Scheme(bridge.NewAPIProxy(sock), http.NotFoundHandler()).ServeHTTP(rec, httpRequest(t, context.Background(), "GET", "mygo://localhost/api/health", nil))
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
