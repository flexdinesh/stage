package logfile

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRotate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	path := filepath.Join(dir, "stage.log")
	r, err := Open(path, 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"aaaaaa\n", "bbbbbb\n", "cccccc\n", "dddddd\n"} {
		if _, err := r.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	_ = r.Close()
	want := map[string]string{"stage.log": "dddddd\n", "stage.log.1": "cccccc\n", "stage.log.2": "bbbbbb\n"}
	for name, body := range want {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !bytes.Equal(b, []byte(body)) {
			t.Fatalf("%s = %q, %v; want %q", name, b, err, body)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "stage.log.3")); !os.IsNotExist(err) {
		t.Fatalf("stage.log.3 kept: %v", err)
	}
	st, _ := os.Stat(dir)
	fst, _ := os.Stat(path)
	if st.Mode().Perm() != 0o700 || fst.Mode().Perm() != 0o600 {
		t.Fatalf("modes dir %v file %v", st.Mode().Perm(), fst.Mode().Perm())
	}
}

// Reopening appends and counts the existing size toward rotation.
func TestReopenAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "l", "stage.log")
	r, _ := Open(path, 10, 1)
	_, _ = r.Write([]byte("12345678"))
	_ = r.Close()
	r, _ = Open(path, 10, 1)
	_, _ = r.Write([]byte("abc"))
	_ = r.Close()
	if b, _ := os.ReadFile(path); string(b) != "abc" {
		t.Fatalf("after reopen: %q", b)
	}
	if b, _ := os.ReadFile(path + ".1"); string(b) != "12345678" {
		t.Fatalf("rotated: %q", b)
	}
}
