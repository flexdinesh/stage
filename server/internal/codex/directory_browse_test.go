package codex_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// folderTree makes dir/{a,b,.hid}, a file, a symlink to a and a broken
// symlink.
func folderTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"a", "B", ".hid"} {
		if err := os.Mkdir(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{"lnk": filepath.Join(dir, "a"), "broken": filepath.Join(dir, "nope")} {
		if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func entryNames(r execution.BrowseResult) []string {
	out := []string{}
	for _, e := range r.Entries {
		out = append(out, e.Name)
	}
	return out
}

func TestBrowseListsFolders(t *testing.T) {
	r := newRig(t)
	dir := folderTree(t)
	c, err := r.adapter.Features(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !c.Operations[execution.OpDirectoryBrowse].Available {
		t.Fatalf("op %+v", c.Operations[execution.OpDirectoryBrowse])
	}
	browser, ok := execution.As[execution.DirectoryBrowser](r.adapter)
	if !ok {
		t.Fatal("adapter hides DirectoryBrowser")
	}
	got, err := browser.Browse(t.Context(), execution.BrowseRequest{Path: dir + "/"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != dir || got.Parent != filepath.Dir(dir) || got.Truncated || !slices.Equal(entryNames(got), []string{"a", "B", "lnk"}) {
		t.Fatalf("listing %+v", got)
	}
	if got.Entries[0].Path != filepath.Join(dir, "a") {
		t.Fatalf("entry %+v", got.Entries[0])
	}
	hidden, err := browser.Browse(t.Context(), execution.BrowseRequest{Path: dir, ShowHidden: true})
	if err != nil || !slices.Equal(entryNames(hidden), []string{".hid", "a", "B", "lnk"}) {
		t.Fatalf("hidden %+v %v", hidden, err)
	}
}

// The default start is the host user's home.
func TestBrowseStartsAtHome(t *testing.T) {
	r := newRig(t)
	home := folderTree(t)
	t.Setenv("HOME", home)
	got, err := r.adapter.Browse(t.Context(), execution.BrowseRequest{})
	if err != nil || got.Path != home || !slices.Equal(entryNames(got), []string{"a", "B", "lnk"}) {
		t.Fatalf("home %+v %v", got, err)
	}
}

func TestBrowseRefusals(t *testing.T) {
	r := newRig(t)
	dir := folderTree(t)
	for path, want := range map[string]string{
		filepath.Join(dir, "nope"):  "Codex cannot read " + filepath.Join(dir, "nope") + ": No such file or directory (os error 2)",
		filepath.Join(dir, "f.txt"): "Codex cannot read " + filepath.Join(dir, "f.txt") + ": Not a directory (os error 20)",
		"relative/dir":              "Enter an absolute folder path on the Codex host.",
	} {
		_, err := r.adapter.Browse(t.Context(), execution.BrowseRequest{Path: path})
		if got := invalidReason(t, err); got != want {
			t.Errorf("%s: %q, want %q", path, got, want)
		}
	}
	r.srv.Unsupported("fs/readDirectory")
	_, err := r.adapter.Browse(t.Context(), execution.BrowseRequest{Path: dir})
	var unavailable *execution.BrowseUnavailable
	if !errors.As(err, &unavailable) || unavailable.Reason != "This Codex version cannot list folders (fs/readDirectory is missing). Type the path." {
		t.Fatalf("older Codex: %v", err)
	}
}

func TestBrowseTruncates(t *testing.T) {
	r := newRig(t)
	dir := t.TempDir()
	for i := range execution.MaxDirectoryEntries + 2 {
		if err := os.Mkdir(filepath.Join(dir, fmt.Sprintf("d%03d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.adapter.Browse(t.Context(), execution.BrowseRequest{Path: dir})
	if err != nil || len(got.Entries) != execution.MaxDirectoryEntries || !got.Truncated || got.Entries[0].Name != "d000" {
		t.Fatalf("big folder: %d %v %v", len(got.Entries), got.Truncated, err)
	}
}
