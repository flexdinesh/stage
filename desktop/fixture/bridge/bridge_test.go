package bridge_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/flexdinesh/stage/desktop/fixture/bridge"
	"github.com/flexdinesh/stage/desktop/fixture/procnet"
	"github.com/flexdinesh/stage/desktop/fixture/stub"
)

// startStub serves a fresh stub on a Unix socket under a short temp dir
// (socket paths are limited to about 104 bytes).
func startStub(t *testing.T) (*stub.Server, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "sf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	ln, err := stub.Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	st := stub.New()
	st.Ping = 50 * time.Millisecond
	srv := &http.Server{Handler: st.Handler()}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
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
		{"GET", "http://stage.invalid/api/health", 0},
		{"POST", "http://stage.invalid/api/uploads", 0},
		{"PATCH", "http://stage.invalid/api/snippets/1", 0},
		{"DELETE", "http://stage.invalid/api/snippets/1", 0},
		{"PUT", "http://stage.invalid/api/preferences/pinned", 0},
		{"GET", "http://stage.invalid/api/threads/a%20b/transcript/stream?after=3", 0},
		{"HEAD", "http://stage.invalid/api/health", http.StatusMethodNotAllowed},
		{"OPTIONS", "http://stage.invalid/api/health", http.StatusMethodNotAllowed},
		{"TRACE", "http://stage.invalid/api/health", http.StatusMethodNotAllowed},
		{"CONNECT", "http://stage.invalid/api/health", http.StatusMethodNotAllowed},
		{"GET", "http://stage.invalid/", http.StatusNotFound},
		{"GET", "http://stage.invalid/api", http.StatusNotFound},
		{"GET", "http://stage.invalid/api/", http.StatusNotFound},
		{"GET", "http://stage.invalid/mcp", http.StatusNotFound},
		{"GET", "http://stage.invalid/api/../mcp", http.StatusNotFound},
		{"GET", "http://stage.invalid/api/./health", http.StatusNotFound},
		{"GET", "http://stage.invalid/api//health", http.StatusNotFound},
		{"GET", "http://stage.invalid/api/health/", http.StatusNotFound},
		{"GET", "http://stage.invalid/api/a%2f..%2fmcp", http.StatusNotFound},
		{"GET", "http://stage.invalid/api/a%5cb", http.StatusNotFound},
		{"GET", "http://stage.invalid/apix/health", http.StatusNotFound},
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

func TestAllow(t *testing.T) {
	cases := []struct {
		method, rawURL, host string
		want                 bool
	}{
		{"GET", "http://stage.invalid/api/health", "", true},
		{"GET", "http://stage.invalid/api/health", "stage.invalid", true},
		{"GET", "https://stage.invalid/api/health", "", false},
		{"GET", "http://stage.invalid:80/api/health", "", false},
		{"GET", "http://example.com/api/health", "", false},
		{"GET", "http://127.0.0.1:7380/api/health", "", false},
		{"GET", "http://user:pw@stage.invalid/api/health", "", false},
		{"GET", "http://stage.invalid/api/health", "example.com", false},
		{"GET", "http://stage.invalid/mcp", "", false},
		{"TRACE", "http://stage.invalid/api/health", "", false},
	}
	for _, c := range cases {
		r, err := http.NewRequest(c.method, c.rawURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Host = c.host
		if got := bridge.Allow(r); got != c.want {
			t.Errorf("%s %s (Host %q): Allow %v, want %v", c.method, c.rawURL, c.host, got, c.want)
		}
	}
}

// TestUnixClient carries JSON and a multipart upload over the socket, as
// bridge A's Go side does for the fetch plugin.
func TestUnixClient(t *testing.T) {
	_, sock := startStub(t)
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:9")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")
	c := bridge.UnixClient(sock)

	res, err := c.Post(bridge.Origin+"/api/snippets", "application/json", strings.NewReader(`{"name":"a","body":"b"}`))
	if err != nil {
		t.Fatal(err)
	}
	var sn stub.Snippet
	decode(t, res, http.StatusCreated, &sn)
	if sn.Name != "a" || sn.ID == 0 {
		t.Fatalf("created %+v", sn)
	}

	body, ctype := multipartPNG(t, "dot.png", pngBytes)
	res, err = c.Post(bridge.Origin+"/api/uploads", ctype, body)
	if err != nil {
		t.Fatal(err)
	}
	var a stub.Attachment
	decode(t, res, http.StatusCreated, &a)
	if a.MimeType != "image/png" || a.SizeBytes != len(pngBytes) {
		t.Fatalf("uploaded %+v", a)
	}
}

func TestUnixClientRefusesRedirect(t *testing.T) {
	_, sock := startStub(t)
	_, err := bridge.UnixClient(sock).Get(bridge.Origin + "/api/fixture/redirect")
	if !errors.Is(err, bridge.ErrRedirect) {
		t.Fatalf("redirect: err %v, want ErrRedirect", err)
	}
}

// TestUnixClientIgnoresHost: whatever host a request names, the
// transport dials only the socket.
func TestUnixClientIgnoresHost(t *testing.T) {
	_, sock := startStub(t)
	res, err := bridge.UnixClient(sock).Get("http://example.com/api/health")
	if err != nil {
		t.Fatal(err)
	}
	var h map[string]string
	decode(t, res, http.StatusOK, &h)
	if h["status"] != "ok" {
		t.Fatalf("health %v", h)
	}
}

func TestSchemeRouting(t *testing.T) {
	_, sock := startStub(t)
	static := http.FileServerFS(fstest.MapFS{"index.html": {Data: []byte("<!doctype html>probe")}})
	h := bridge.Scheme(bridge.NewAPIProxy(sock), static)
	cases := []struct {
		method, target string
		status         int
		contains       string
	}{
		{"GET", "mygo://localhost/", 200, "probe"},
		{"GET", "mygo://localhost/api/health", 200, `"status":"ok"`},
		{"POST", "mygo://localhost/", 405, "read-only"},
		{"PROPFIND", "mygo://localhost/api/health", 405, "denied"},
		{"GET", "mygo://localhost/api", 404, "denied"},
		{"GET", "mygo://localhost/api//health", 404, "denied"},
		{"GET", "mygo://localhost/api/fixture/redirect", 502, "redirect"},
		{"GET", "mygo://localhost/api/nope", 404, "no such endpoint"},
	}
	for _, c := range cases {
		rec := newStreamRecorder()
		h.ServeHTTP(rec, httpRequest(t, context.Background(), c.method, c.target, nil))
		status, body := rec.result()
		if status != c.status || !strings.Contains(body, c.contains) {
			t.Errorf("%s %s: %d %q, want %d containing %q", c.method, c.target, status, body, c.status, c.contains)
		}
	}
}

func TestSchemeProxyUploadAndAttachment(t *testing.T) {
	_, sock := startStub(t)
	h := bridge.Scheme(bridge.NewAPIProxy(sock), http.NotFoundHandler())

	body, ctype := multipartPNG(t, "dot.png", pngBytes)
	r := httpRequest(t, context.Background(), "POST", "mygo://localhost/api/uploads", body)
	r.Header.Set("Content-Type", ctype)
	rec := newStreamRecorder()
	h.ServeHTTP(rec, r)
	status, out := rec.result()
	var a stub.Attachment
	if status != http.StatusCreated || json.Unmarshal([]byte(out), &a) != nil {
		t.Fatalf("upload: %d %s", status, out)
	}

	rec = newStreamRecorder()
	h.ServeHTTP(rec, httpRequest(t, context.Background(), "GET", "mygo://localhost/api/attachments/"+itoa(a.ID), nil))
	status, out = rec.result()
	if status != 200 || out != string(pngBytes) {
		t.Fatalf("attachment: %d, %d bytes", status, len(out))
	}
	for k, want := range map[string]string{
		"Content-Type":            "image/png",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "default-src 'none'; sandbox",
	} {
		if got := rec.sentHeader().Get(k); got != want {
			t.Errorf("attachment %s = %q, want %q", k, got, want)
		}
	}

	big := append(append([]byte{}, pngBytes...), make([]byte, stub.MaxAttachmentBytes)...)
	body, ctype = multipartPNG(t, "big.png", big)
	r = httpRequest(t, context.Background(), "POST", "mygo://localhost/api/uploads", body)
	r.Header.Set("Content-Type", ctype)
	rec = newStreamRecorder()
	h.ServeHTTP(rec, r)
	if status, out := rec.result(); status != 400 || !strings.Contains(out, "larger than 10 MB") {
		t.Fatalf("oversize upload: %d %s", status, out)
	}
}

// TestSchemeProxySSE: each event reaches the page as it is emitted (not
// when the stream ends), a reconnect with Last-Event-ID resumes without
// gaps or duplicates, and the page going away ends the stream on the
// server.
func TestSchemeProxySSE(t *testing.T) {
	st, sock := startStub(t)
	h := bridge.Scheme(bridge.NewAPIProxy(sock), http.NotFoundHandler())
	client := bridge.UnixClient(sock)
	emit := func(n int) {
		res, err := client.Post(bridge.Origin+"/api/fixture/threads/t1/emit", "application/json", strings.NewReader(`{"count":`+itoa(int64(n))+`}`))
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
	}

	ctx, cancel := context.WithCancel(context.Background())
	rec := newStreamRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(rec, httpRequest(t, ctx, "GET", "mygo://localhost/api/threads/t1/transcript/stream", nil))
	}()
	waitFor(t, "stream open", func() bool { return st.Counts().Streams == 1 })
	waitFor(t, "stream headers", func() bool { return rec.sentHeader() != nil })
	if got := rec.sentHeader().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type %q", got)
	}
	emit(3)
	events := rec.events(t, 3)
	for i, ev := range events {
		if ev.id != itoa(int64(i)) || ev.event != "item" {
			t.Fatalf("event %d = %+v", i, ev)
		}
	}
	waitFor(t, "a ping", func() bool { return strings.Contains(rec.body(), ": ping") })
	cancel()
	<-done
	waitFor(t, "stream closed on the server", func() bool { return st.Counts().Streams == 0 })

	// Reconnect as EventSource does, with Last-Event-ID.
	emit(2)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	rec = newStreamRecorder()
	r := httpRequest(t, ctx, "GET", "mygo://localhost/api/threads/t1/transcript/stream", nil)
	r.Header.Set("Last-Event-ID", events[len(events)-1].id)
	done = make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(rec, r)
	}()
	resumed := rec.events(t, 2)
	if resumed[0].id != "3" || resumed[1].id != "4" {
		t.Fatalf("resumed %+v, want ids 3, 4", resumed)
	}
	res, err := client.Post(bridge.Origin+"/api/fixture/threads/t1/reset", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	<-done
	if !strings.Contains(rec.body(), "event: reset") {
		t.Fatalf("no reset event in %q", rec.body())
	}
}

// TestSchemeProxyCancel: a request the page aborts ends on the server.
func TestSchemeProxyCancel(t *testing.T) {
	st, sock := startStub(t)
	h := bridge.Scheme(bridge.NewAPIProxy(sock), http.NotFoundHandler())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(newStreamRecorder(), httpRequest(t, ctx, "GET", "mygo://localhost/api/fixture/slow", nil))
	}()
	waitFor(t, "slow request open", func() bool { return st.Counts().Slow == 1 })
	cancel()
	<-done
	waitFor(t, "slow request ended on the server", func() bool { return st.Counts().Slow == 0 })
}

// TestUnixClientCancel: the same for bridge A's client.
func TestUnixClientCancel(t *testing.T) {
	st, sock := startStub(t)
	ctx, cancel := context.WithCancel(context.Background())
	r, err := http.NewRequestWithContext(ctx, "GET", bridge.Origin+"/api/fixture/slow", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := bridge.UnixClient(sock).Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	waitFor(t, "slow request open", func() bool { return st.Counts().Slow == 1 })
	cancel()
	waitFor(t, "slow request ended on the server", func() bool { return st.Counts().Slow == 0 })
}

// TestNoTCPListener: serving the stub and both bridges in this process
// opens no TCP listener.
func TestNoTCPListener(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("procnet is implemented on Linux and macOS")
	}
	_, sock := startStub(t)
	res, err := bridge.UnixClient(sock).Get(bridge.Origin + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
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

func decode(t *testing.T, res *http.Response, status int, v any) {
	t.Helper()
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != status {
		t.Fatalf("status %d, want %d: %s", res.StatusCode, status, b)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
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

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
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
