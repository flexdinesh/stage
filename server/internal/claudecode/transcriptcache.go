package claudecode

import (
	"os"
	"sync"
	"time"
)

// transcriptTTL is how long an unread transcript stays cached.
const transcriptTTL = 10 * time.Minute

// transcripts reuses a parsed transcript while its file keeps its size and
// modification time (transcripts only grow): every tick observes each
// watched session, including discovered ones Claude lists as done until
// `claude rm`, and a full parse per tick would grow with their history.
type transcripts struct {
	mu    sync.Mutex
	now   func() time.Time
	files map[string]cachedTranscript
}

type cachedTranscript struct {
	size int64
	mod  time.Time
	used time.Time
	t    Transcript
}

// read is ReadTranscript(path), from the cache when the file is unchanged.
// Entries unused for transcriptTTL are dropped on a miss.
func (c *transcripts) read(path string) (Transcript, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return Transcript{}, err
	}
	now := c.now()
	c.mu.Lock()
	if e, ok := c.files[path]; ok && e.size == fi.Size() && e.mod.Equal(fi.ModTime()) {
		e.used = now
		c.files[path] = e
		c.mu.Unlock()
		return e.t, nil
	}
	c.mu.Unlock()
	t, err := ReadTranscript(path)
	if err != nil {
		return t, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.files == nil {
		c.files = map[string]cachedTranscript{}
	}
	for p, e := range c.files {
		if now.Sub(e.used) > transcriptTTL {
			delete(c.files, p)
		}
	}
	c.files[path] = cachedTranscript{size: fi.Size(), mod: fi.ModTime(), used: now, t: t}
	return t, nil
}

// size is the number of cached transcripts (tests).
func (c *transcripts) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.files)
}
