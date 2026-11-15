// Package stub is a stand-in for the Stage server over a Unix domain
// socket. It reproduces Stage's wire contract (docs/api.md) for the
// endpoints the desktop bridge must carry: JSON CRUD (snippets), health,
// multipart image upload, attachment bytes with Stage's headers, and the
// SSE transcript stream (retry, id, item/state events, ping, reset,
// Last-Event-ID). The real stream needs a live T3 thread, which a
// reproducible fixture cannot have.
//
// Routes under /api/fixture/ exist only here: they drive the stream
// (emit, drop, reset), count open streams and slow requests (evidence of
// cancellation), and serve a redirect and a download.
package stub

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MaxAttachmentBytes and AttachmentMimeTypes are Stage's
// (server/internal/db/attachments.go).
const MaxAttachmentBytes = 10 << 20

var AttachmentMimeTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

// Snippet is Stage's snippet (docs/api.md, Snippets).
type Snippet struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Body      string `json:"body"`
	ProjectID *int64 `json:"projectId"`
}

// Attachment is Stage's attachment metadata.
type Attachment struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	MimeType  string `json:"mimeType"`
	SizeBytes int    `json:"sizeBytes"`
}

// Item is a transcript item of the stub; Stage's are richer, the stream's
// framing is what the fixture tests.
type Item struct {
	Cursor int    `json:"cursor"`
	Text   string `json:"text"`
	At     int64  `json:"at"` // unix ms when emitted, for latency
}

// Counts is GET /api/fixture/streams.
type Counts struct {
	Streams int `json:"streams"`
	Slow    int `json:"slow"`
}

// Server is the stub's state. The zero value is not usable; use New.
type Server struct {
	// Retry is the SSE retry field in ms (Stage sends 3000).
	Retry int
	// Ping is the SSE keep-alive interval (Stage's is 15s).
	Ping time.Duration

	mu          sync.Mutex
	nextID      int64
	snippets    map[int64]Snippet
	attachments map[int64]attachment
	threads     map[string]*thread
	slow        int
}

type attachment struct {
	Attachment
	data []byte
}

type thread struct {
	items []Item
	subs  map[*subscriber]struct{}
}

type subscriber struct {
	events chan Item
	drop   chan bool // true: reset (terminal), false: plain drop
}

// New returns an empty stub with Stage's SSE timings.
func New() *Server {
	return &Server{
		Retry:       3000,
		Ping:        15 * time.Second,
		snippets:    map[int64]Snippet{},
		attachments: map[int64]attachment{},
		threads:     map[string]*thread{},
	}
}

// Handler routes the stub's API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "db": "ok"})
	})
	mux.HandleFunc("GET /api/snippets", s.listSnippets)
	mux.HandleFunc("POST /api/snippets", s.createSnippet)
	mux.HandleFunc("GET /api/snippets/{id}", s.getSnippet)
	mux.HandleFunc("PATCH /api/snippets/{id}", s.patchSnippet)
	mux.HandleFunc("DELETE /api/snippets/{id}", s.deleteSnippet)
	mux.HandleFunc("POST /api/uploads", s.upload)
	mux.HandleFunc("GET /api/attachments/{id}", s.getAttachment)
	mux.HandleFunc("GET /api/threads/{id}/transcript/stream", s.stream)

	mux.HandleFunc("POST /api/fixture/threads/{id}/emit", s.emit)
	mux.HandleFunc("POST /api/fixture/threads/{id}/drop", s.dropStreams(false))
	mux.HandleFunc("POST /api/fixture/threads/{id}/reset", s.dropStreams(true))
	mux.HandleFunc("GET /api/fixture/streams", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.Counts())
	})
	mux.HandleFunc("GET /api/fixture/slow", s.slowRequest)
	mux.HandleFunc("GET /api/fixture/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com/", http.StatusFound)
	})
	mux.HandleFunc("GET /api/fixture/download", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="stage-fixture.txt"`)
		_, _ = io.WriteString(w, DownloadBody)
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint", "")
	})
	return mux
}

// DownloadBody is what GET /api/fixture/download serves.
const DownloadBody = "stage fixture download\n"

// Counts reports open transcript streams and slow requests.
func (s *Server) Counts() Counts {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, t := range s.threads {
		n += len(t.subs)
	}
	return Counts{Streams: n, Slow: s.slow}
}

// Listen listens on the Unix socket path, replacing a stale socket file
// but never a live socket or another file, and makes it owner-only.
func Listen(path string) (net.Listener, error) {
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("stub: %s exists and is not a socket", path)
		}
		if c, err := net.Dial("unix", path); err == nil {
			_ = c.Close()
			return nil, fmt.Errorf("stub: %s is in use", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return ln, nil
}

func (s *Server) listSnippets(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	out := make([]Snippet, 0, len(s.snippets))
	for _, sn := range s.snippets {
		out = append(out, sn)
	}
	s.mu.Unlock()
	slices.SortFunc(out, func(a, b Snippet) int { return strings.Compare(a.Name, b.Name) })
	writeJSON(w, http.StatusOK, map[string][]Snippet{"snippets": out})
}

type snippetInput struct {
	Name *string `json:"name"`
	Body *string `json:"body"`
}

func (s *Server) createSnippet(w http.ResponseWriter, r *http.Request) {
	var in snippetInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error(), "")
		return
	}
	if in.Name == nil || strings.TrimSpace(*in.Name) == "" || in.Body == nil || strings.TrimSpace(*in.Body) == "" {
		writeError(w, http.StatusBadRequest, "name and body are required", "")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sn := range s.snippets {
		if sn.Name == *in.Name {
			writeError(w, http.StatusConflict, "a snippet with this name exists", "")
			return
		}
	}
	s.nextID++
	sn := Snippet{ID: s.nextID, Name: *in.Name, Body: *in.Body}
	s.snippets[sn.ID] = sn
	writeJSON(w, http.StatusCreated, sn)
}

func (s *Server) getSnippet(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	sn, found := s.snippets[id]
	s.mu.Unlock()
	if !found {
		writeError(w, http.StatusNotFound, "snippet not found", "")
		return
	}
	writeJSON(w, http.StatusOK, sn)
}

func (s *Server) patchSnippet(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in snippetInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error(), "")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sn, found := s.snippets[id]
	if !found {
		writeError(w, http.StatusNotFound, "snippet not found", "")
		return
	}
	if in.Name != nil {
		sn.Name = *in.Name
	}
	if in.Body != nil {
		sn.Body = *in.Body
	}
	s.snippets[id] = sn
	writeJSON(w, http.StatusOK, sn)
}

func (s *Server) deleteSnippet(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	_, found := s.snippets[id]
	delete(s.snippets, id)
	s.mu.Unlock()
	if !found {
		writeError(w, http.StatusNotFound, "snippet not found", "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// upload mirrors Stage's uploadImage: field "file", 10 MB, type sniffed
// from the bytes, 400 for every refusal.
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxAttachmentBytes+64<<10)
	file, header, err := r.FormFile("file")
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusBadRequest, "image is larger than 10 MB", "")
			return
		}
		writeError(w, http.StatusBadRequest, fmt.Sprintf("multipart field \"file\" is required: %v", err), "")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxAttachmentBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read image: "+err.Error(), "")
		return
	}
	if len(data) == 0 {
		writeError(w, http.StatusBadRequest, "image is empty", "")
		return
	}
	if len(data) > MaxAttachmentBytes {
		writeError(w, http.StatusBadRequest, "image is larger than 10 MB", "")
		return
	}
	mime := http.DetectContentType(data)
	if !slices.Contains(AttachmentMimeTypes, mime) {
		writeError(w, http.StatusBadRequest, "unsupported image type "+mime+": use PNG, JPEG, GIF or WebP", "")
		return
	}
	s.mu.Lock()
	s.nextID++
	a := attachment{Attachment{ID: s.nextID, Name: header.Filename, MimeType: mime, SizeBytes: len(data)}, data}
	s.attachments[a.ID] = a
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, a.Attachment)
}

// getAttachment mirrors Stage's headers.
func (s *Server) getAttachment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	a, found := s.attachments[id]
	s.mu.Unlock()
	if !found {
		writeError(w, http.StatusNotFound, "attachment not found", "")
		return
	}
	h := w.Header()
	h.Set("Content-Type", a.MimeType)
	h.Set("Content-Length", strconv.Itoa(len(a.data)))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(a.data)
}

func (s *Server) thread(id string) *thread {
	t := s.threads[id]
	if t == nil {
		t = &thread{subs: map[*subscriber]struct{}{}}
		s.threads[id] = t
	}
	return t
}

// stream mirrors Stage's streamTranscript framing: retry first, then
// "event: item\nid: <cursor>\ndata: <json>", pings as comments, and a
// terminal "event: reset". Last-Event-ID wins over ?after.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	after, err := cursor(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "")
		return
	}
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	write := func(format string, args ...any) bool {
		if _, err := fmt.Fprintf(w, format, args...); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if !write("retry: %d\n\n", s.Retry) {
		return
	}

	sub := &subscriber{events: make(chan Item, 256), drop: make(chan bool, 1)}
	s.mu.Lock()
	t := s.thread(r.PathValue("id"))
	backlog := make([]Item, 0, len(t.items))
	for _, it := range t.items {
		if it.Cursor > after {
			backlog = append(backlog, it)
		}
	}
	t.subs[sub] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(t.subs, sub)
		s.mu.Unlock()
	}()

	send := func(it Item) bool {
		b, err := json.Marshal(it)
		if err != nil {
			return false
		}
		return write("event: item\nid: %d\ndata: %s\n\n", it.Cursor, b)
	}
	for _, it := range backlog {
		if !send(it) {
			return
		}
	}
	ping := time.NewTicker(s.Ping)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			if !write(": ping\n\n") {
				return
			}
		case reset := <-sub.drop:
			if reset {
				write("event: reset\ndata: {}\n\n")
			}
			return
		case it := <-sub.events:
			if !send(it) {
				return
			}
		}
	}
}

// emit appends count items ({"count": n}, default 1) and sends them to
// open streams.
func (s *Server) emit(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Count int `json:"count"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error(), "")
			return
		}
	}
	if in.Count <= 0 {
		in.Count = 1
	}
	s.mu.Lock()
	t := s.thread(r.PathValue("id"))
	out := make([]Item, 0, in.Count)
	for range in.Count {
		it := Item{Cursor: len(t.items), Text: fmt.Sprintf("item %d", len(t.items)), At: time.Now().UnixMilli()}
		t.items = append(t.items, it)
		out = append(out, it)
		for sub := range t.subs {
			select {
			case sub.events <- it:
			default: // a stalled reader reconnects with Last-Event-ID
			}
		}
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string][]Item{"items": out})
}

// dropStreams ends the thread's open streams: plainly (the client
// reconnects) or with a terminal reset event.
func (s *Server) dropStreams(reset bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		t := s.thread(r.PathValue("id"))
		n := 0
		for sub := range t.subs {
			select {
			case sub.drop <- reset:
				n++
			default:
			}
		}
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]int{"dropped": n})
	}
}

// slowRequest answers headers, then holds the body open until the client
// goes away, counting itself in Counts.Slow meanwhile.
func (s *Server) slowRequest(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.slow++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.slow--
		s.mu.Unlock()
	}()
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			if _, err := io.WriteString(w, "."); err != nil {
				return
			}
			if rc.Flush() != nil {
				return
			}
		}
	}
}

func cursor(r *http.Request) (int, error) {
	raw, name := strings.TrimSpace(r.Header.Get("Last-Event-ID")), "Last-Event-ID"
	if raw == "" {
		raw, name = strings.TrimSpace(r.URL.Query().Get("after")), "after"
	}
	if raw == "" {
		return -1, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < -1 {
		return 0, fmt.Errorf("invalid %s %q", name, raw)
	}
	return n, nil
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id", "")
		return 0, false
	}
	return id, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg, code string) {
	body := map[string]string{"error": msg}
	if code != "" {
		body["code"] = code
	}
	writeJSON(w, status, body)
}
