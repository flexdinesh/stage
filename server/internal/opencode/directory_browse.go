package opencode

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/flexdinesh/stage/server/internal/execution"
)

var _ execution.DirectoryBrowser = (*Adapter)(nil)

const (
	directoryBrowseUnavailable = "This OpenCode version cannot list folders (fs.list is missing). Type the path."
	browseNoProject            = "OpenCode lists no projects yet. Type the path."
	// browseAnchors bounds how many listed projects Browse tries as its
	// location when their folders are gone.
	browseAnchors = 5
)

// errDirectoryNotFound: GET /api/fs/list answered 404 FileNotFoundError (a
// missing path or a file).
var errDirectoryNotFound = errors.New("folder not found")

type wireFSList struct {
	Location struct{ Directory string }
	Data     []struct{ Path, Type string }
}

// Browse lists a folder with fs.list. Every call names an existing project
// as its location: a request without one resolves OpenCode's default
// location (the service's working directory) and persists it as a project
// (OpenCode 2.3.2, verified on a throwaway opencode serve; pinned by
// TestBrowseAnchorsToAListedProject). A listed path is never persisted. The
// anchor is the most recently active listed project that is not archived
// and not global; the default start is its parent folder. Entry paths come
// back relative to the location.
func (a *Adapter) Browse(ctx context.Context, r execution.BrowseRequest) (execution.BrowseResult, error) {
	var out execution.BrowseResult
	if r.Path != "" {
		if err := invisible("Path", r.Path); err != nil {
			return out, &execution.Invalid{Reason: err.Error()}
		}
		if !path.IsAbs(r.Path) {
			return out, &execution.Invalid{Reason: "Enter an absolute path on the OpenCode host, like /home/me/project."}
		}
	}
	contract, err := a.checkContract(ctx)
	if err != nil {
		return out, err
	}
	if !contract.fsList {
		return out, &execution.BrowseUnavailable{Reason: directoryBrowseUnavailable}
	}
	projects, err := a.projects(ctx)
	if err != nil {
		return out, err
	}
	anchors := browseAnchorDirs(projects)
	if len(anchors) == 0 {
		return out, &execution.BrowseUnavailable{Reason: browseNoProject}
	}
	for _, anchor := range anchors {
		dir := path.Clean(r.Path)
		if r.Path == "" {
			dir = path.Dir(anchor)
		}
		var list wireFSList
		err := a.get(ctx, "/api/fs/list?"+url.Values{"location[directory]": {anchor}, "path": {dir}}.Encode(), &list)
		switch {
		case errors.Is(err, errLocationNotFound):
			// The anchor's folder is gone; try the next listed project.
			continue
		case errors.Is(err, errDirectoryNotFound):
			return out, &execution.Invalid{Reason: fmt.Sprintf("Folder not found on the OpenCode host: %s.", dir)}
		case err != nil:
			return out, err
		}
		if !path.IsAbs(list.Location.Directory) {
			return out, errors.New("invalid OpenCode folder listing")
		}
		var entries []execution.DirEntry
		for _, e := range list.Data {
			if e.Type != "directory" {
				continue
			}
			// Direct children: the name is the resolved entry's last
			// segment, kept under the requested path (OpenCode may
			// resolve a symlinked folder).
			name := path.Base(path.Join(list.Location.Directory, e.Path))
			entries = append(entries, execution.DirEntry{Name: name, Path: path.Join(dir, name)})
		}
		return execution.Listing(dir, entries, r.ShowHidden), nil
	}
	return out, &execution.BrowseUnavailable{Reason: browseNoProject}
}

// browseAnchorDirs are the listed projects Browse may name as its location,
// most recently active first.
func browseAnchorDirs(projects []wireProject) []string {
	usable := make([]wireProject, 0, len(projects))
	for _, p := range projects {
		if p.ID != globalProject && p.Time.Archived <= 0 && path.IsAbs(p.Canonical) && strings.TrimSpace(p.Canonical) != "/" {
			usable = append(usable, p)
		}
	}
	slices.SortStableFunc(usable, func(a, b wireProject) int {
		return cmp.Or(cmp.Compare(b.Time.Active, a.Time.Active), cmp.Compare(b.Time.Updated, a.Time.Updated))
	})
	out := make([]string, 0, min(len(usable), browseAnchors))
	for _, p := range usable[:min(len(usable), browseAnchors)] {
		out = append(out, path.Clean(p.Canonical))
	}
	return out
}
