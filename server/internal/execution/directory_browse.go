package execution

import (
	"cmp"
	"context"
	"path"
	"slices"
	"strings"
)

// MaxDirectoryEntries bounds one folder listing; a larger folder is
// truncated (BrowseResult.Truncated) and the person types the path.
const MaxDirectoryEntries = 500

// MaxBrowsePath bounds a requested path.
const MaxBrowsePath = 4096

// BrowseRequest names a folder on the harness's host. Empty Path means the
// adapter's default start.
type BrowseRequest struct {
	Path       string `json:"path,omitempty"`
	ShowHidden bool   `json:"showHidden,omitempty"`
}

// DirEntry is one child folder: its name and absolute path.
type DirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// BrowseResult lists a folder's child folders (never files). Parent is ""
// at the filesystem root.
type BrowseResult struct {
	Path      string     `json:"path"`
	Parent    string     `json:"parent"`
	Entries   []DirEntry `json:"entries"`
	Truncated bool       `json:"truncated"`
}

// DirectoryBrowser lists folders on the harness's host with the harness's
// own API, so a person can pick a path field's value (OpDirectoryBrowse).
// It reads only. *Invalid: the harness refused the path (missing, not a
// folder, unreadable). *BrowseUnavailable: it cannot browse right now.
type DirectoryBrowser interface {
	Browse(context.Context, BrowseRequest) (BrowseResult, error)
}

// BrowseUnavailable means the adapter cannot browse now, in its own words
// (the API answers 409 operation_unavailable with Reason).
type BrowseUnavailable struct{ Reason string }

func (e *BrowseUnavailable) Error() string { return e.Reason }

// Listing builds an adapter's result for the absolute folder dir from its
// child folders: dot-named folders only when showHidden, sorted by name
// (case-insensitive), at most MaxDirectoryEntries. Paths use forward
// slashes (POSIX hosts).
func Listing(dir string, entries []DirEntry, showHidden bool) BrowseResult {
	dir = path.Clean(dir)
	out := BrowseResult{Path: dir, Parent: ParentDir(dir), Entries: make([]DirEntry, 0, min(len(entries), MaxDirectoryEntries))}
	kept := make([]DirEntry, 0, len(entries))
	for _, e := range entries {
		if e.Name == "" || e.Name == "." || e.Name == ".." || (!showHidden && strings.HasPrefix(e.Name, ".")) {
			continue
		}
		kept = append(kept, e)
	}
	slices.SortFunc(kept, func(a, b DirEntry) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.Name, b.Name))
	})
	if len(kept) > MaxDirectoryEntries {
		kept, out.Truncated = kept[:MaxDirectoryEntries], true
	}
	out.Entries = append(out.Entries, kept...)
	return out
}

// ParentDir is the parent of the absolute folder dir, or "" at the root.
func ParentDir(dir string) string {
	dir = path.Clean(dir)
	if dir == "/" || !path.IsAbs(dir) {
		return ""
	}
	return path.Dir(dir)
}
