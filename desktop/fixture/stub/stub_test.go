package stub

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnippetCRUD(t *testing.T) {
	h := New().Handler()
	do := func(method, target, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
		return rec
	}
	if rec := do("POST", "/api/snippets", `{"name":"a","body":"b"}`); rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	if rec := do("POST", "/api/snippets", `{"name":"a","body":"c"}`); rec.Code != 409 {
		t.Fatalf("duplicate: %d", rec.Code)
	}
	if rec := do("POST", "/api/snippets", `{"name":" ","body":"c"}`); rec.Code != 400 {
		t.Fatalf("blank name: %d", rec.Code)
	}
	if rec := do("PATCH", "/api/snippets/1", `{"body":"z"}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"body":"z"`) {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	if rec := do("GET", "/api/snippets", ""); !strings.Contains(rec.Body.String(), `"snippets":[{"id":1`) {
		t.Fatalf("list: %s", rec.Body)
	}
	if rec := do("DELETE", "/api/snippets/1", ""); rec.Code != 204 {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec := do("GET", "/api/snippets/1", ""); rec.Code != 404 || !strings.Contains(rec.Body.String(), `"error"`) {
		t.Fatalf("get deleted: %d %s", rec.Code, rec.Body)
	}
}

func TestUploadRefusesNonImage(t *testing.T) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "x.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(fw, "plain text, not a PNG")
	_ = mw.Close()
	r := httptest.NewRequest("POST", "/api/uploads", &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	New().Handler().ServeHTTP(rec, r)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "unsupported image type") {
		t.Fatalf("non-image upload: %d %s", rec.Code, rec.Body)
	}
}

func TestStreamCursor(t *testing.T) {
	r := httptest.NewRequest("GET", "/x?after=4", nil)
	if n, err := cursor(r); err != nil || n != 4 {
		t.Fatalf("after=4: %d %v", n, err)
	}
	r.Header.Set("Last-Event-ID", "7")
	if n, err := cursor(r); err != nil || n != 7 {
		t.Fatalf("Last-Event-ID wins: %d %v", n, err)
	}
	r.Header.Set("Last-Event-ID", "x")
	if _, err := cursor(r); err == nil {
		t.Fatal("invalid Last-Event-ID accepted")
	}
}

func TestListen(t *testing.T) {
	dir, err := os.MkdirTemp("", "sf")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s.sock")
	ln, err := Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = http.Serve(ln, New().Handler()) }()
	if _, err := Listen(sock); err == nil || !strings.Contains(err.Error(), "in use") {
		t.Fatalf("second listen on a live socket: %v", err)
	}
	fi, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode %v, want 0600", fi.Mode().Perm())
	}
	_ = ln.Close()

	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(file); err == nil {
		t.Fatal("listen replaced a regular file")
	}
}
