//go:build !postgres

package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Postgres URL in a binary without the postgres tag: exit 78 naming the
// Docker image, before the data dir is touched, never printing the URL.
func TestServePostgresUnsupported(t *testing.T) {
	short, environ := env(t)
	data := filepath.Join(short, "data")
	code, stderr := runMain(t, append(environ, "STAGE_DATA_DIR="+data, "STAGE_DATABASE_URL=postgres://u:s3cret-pw@db/stage"), "serve", "--listen", "unix:"+filepath.Join(short, "s.sock"))
	if code != 78 || !strings.Contains(stderr, "no Postgres support") || !strings.Contains(stderr, "Docker image") || strings.Contains(stderr, "s3cret-pw") {
		t.Fatalf("postgres URL without the tag: %d %s", code, stderr)
	}
	if _, err := os.Stat(data); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("data dir touched: %v", err)
	}
}
