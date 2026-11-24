package serve_test

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db/sqlite"
)

// syncBuffer is a log sink safe for the server's goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func bufLogger() (*slog.Logger, *syncBuffer) {
	b := &syncBuffer{}
	return slog.New(slog.NewTextHandler(b, nil)), b
}

// TestSQLiteEngineReported: unset STAGE_DATABASE_URL is SQLite in the data
// dir, reported by health and the startup log.
func TestSQLiteEngineReported(t *testing.T) {
	short := isolate(t)
	data := filepath.Join(short, "d")
	logger, logs := bufLogger()
	i := startLogged(t, cfg(t, data, "unix:"+filepath.Join(short, "s.sock")), newT3(), logger)
	if h := health(t, i.ready.Listeners[0]); h.Engine != "sqlite" || h.Schema != sqlite.MaxVersion() {
		t.Fatalf("health engine %q schema %d", h.Engine, h.Schema)
	}
	want := "msg=database engine=sqlite path=" + filepath.Join(i.ready.DataDir, sqlite.DBFile)
	if !strings.Contains(logs.String(), want) {
		t.Fatalf("startup log lacks %q:\n%s", want, logs)
	}
}
