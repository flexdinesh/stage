package claudecode

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// An unchanged transcript is parsed once; a grown one is read again; one
// unused for transcriptTTL is dropped on the next miss.
func TestTranscriptCache(t *testing.T) {
	now := time.Now()
	c := &transcripts{now: func() time.Time { return now }}
	dir := t.TempDir()
	write := func(name, s string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	user := `{"type":"user","entrypoint":"cli","uuid":"u1","timestamp":"2026-11-25T00:00:00Z","message":{"role":"user","content":"hi"}}` + "\n"
	reply := func(text string) string {
		return `{"type":"assistant","uuid":"a1","timestamp":"2026-11-25T00:00:01Z","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"` + text + `"}]}}` + "\n"
	}
	p := write("a.jsonl", user+reply("one"))
	tr, err := c.read(p)
	if err != nil || tr.Reply != "one" {
		t.Fatalf("%+v %v", tr, err)
	}
	// Same size and mtime: the cached parse, not the bytes on disk.
	fi, _ := os.Stat(p)
	write("a.jsonl", user+reply("two"))
	if err := os.Chtimes(p, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	if tr, _ = c.read(p); tr.Reply != "one" {
		t.Fatalf("cache missed: %+v", tr)
	}
	write("a.jsonl", user+reply("three, longer"))
	if tr, _ = c.read(p); tr.Reply != "three, longer" {
		t.Fatalf("stale: %+v", tr)
	}
	now = now.Add(transcriptTTL + time.Second)
	q := write("b.jsonl", user)
	if _, err := c.read(q); err != nil || c.size() != 1 {
		t.Fatalf("unused entry kept: %d %v", c.size(), err)
	}
	if _, err := c.read(filepath.Join(dir, "missing.jsonl")); err == nil {
		t.Fatal("read a missing transcript")
	}
}
