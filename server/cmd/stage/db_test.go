package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func runDBArgs(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := runDB(context.Background(), args, &out, &errb)
	return code, out.String(), errb.String()
}

// Usage errors exit 2 with the usage; help exits 0.
func TestDBUsage(t *testing.T) {
	src := filepath.Join(t.TempDir(), "stage.db")
	cases := []struct {
		args []string
		want string
	}{
		{nil, "missing command"},
		{[]string{"move"}, `unknown command "move"`},
		{[]string{"copy"}, "--from is required"},
		{[]string{"copy", "--from", src}, "--to is required"},
		{[]string{"copy", "--from", src, "--to", "postgres://h/d", "extra"}, `unexpected argument "extra"`},
		{[]string{"copy", "--bogus"}, "flag provided but not defined"},
	}
	for _, c := range cases {
		code, _, stderr := runDBArgs(c.args...)
		if code != 2 || !strings.Contains(stderr, c.want) || !strings.Contains(stderr, "stage db copy --from PATH --to URL") {
			t.Errorf("%v: %d %s", c.args, code, stderr)
		}
	}
	for _, args := range [][]string{{"help"}, {"copy", "--help"}} {
		if code, out, _ := runDBArgs(args...); code != 0 || !strings.Contains(out, "stage db copy") {
			t.Errorf("%v: %d %s", args, code, out)
		}
	}
}

// A --to that is not a Postgres URL: exit 78, never printing it.
func TestDBCopyInvalidURL(t *testing.T) {
	src := filepath.Join(t.TempDir(), "stage.db")
	code, _, stderr := runDBArgs("copy", "--from", src, "--to", "mysql://u:s3cret-pw@db/stage")
	if code != 78 || !strings.Contains(stderr, "postgres://") || strings.Contains(stderr, "s3cret-pw") {
		t.Fatalf("%d %s", code, stderr)
	}
}

// main dispatches `stage db`.
func TestMainDispatchesDB(t *testing.T) {
	_, environ := env(t)
	if code, stderr := runMain(t, environ, "db"); code != 2 || !strings.Contains(stderr, "stage db: missing command") {
		t.Fatalf("%d %s", code, stderr)
	}
}
