package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/flexdinesh/stage/server/internal/execution"
)

var _ execution.DirectoryBrowser = (*Adapter)(nil)

const directoryBrowseUnsupported = "This Codex version cannot list folders (fs/readDirectory is missing). Type the path."

type fsDirectoryEntry struct {
	FileName    string `json:"fileName"`
	IsDirectory bool   `json:"isDirectory"`
}

type fsReadDirectoryResponse struct {
	Entries []fsDirectoryEntry `json:"entries"`
}

// Browse lists a folder with fs/readDirectory, which reads only. The default
// start is the host user's home: the daemon serves a local socket and runs
// as the adapter's user.
func (a *Adapter) Browse(ctx context.Context, r execution.BrowseRequest) (execution.BrowseResult, error) {
	var out execution.BrowseResult
	dir := r.Path
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil || !filepath.IsAbs(home) {
			return out, &execution.BrowseUnavailable{Reason: "Stage cannot find the Codex host's home folder. Type the path."}
		}
		dir = home
	}
	if !filepath.IsAbs(dir) {
		return out, &execution.Invalid{Reason: "Enter an absolute folder path on the Codex host."}
	}
	dir = filepath.Clean(dir)
	var res fsReadDirectoryResponse
	if err := a.client.call(ctx, "fs/readDirectory", map[string]any{"path": dir}, &res); err != nil {
		var re *RPCError
		switch {
		case unsupported(err):
			return out, &execution.BrowseUnavailable{Reason: directoryBrowseUnsupported}
		case errors.As(err, &re):
			return out, &execution.Invalid{Reason: "Codex cannot read " + dir + ": " + re.Message}
		}
		return out, err
	}
	entries := make([]execution.DirEntry, 0, len(res.Entries))
	for _, e := range res.Entries {
		if e.IsDirectory && e.FileName != "" && filepath.Base(e.FileName) == e.FileName {
			entries = append(entries, execution.DirEntry{Name: e.FileName, Path: filepath.Join(dir, e.FileName)})
		}
	}
	return execution.Listing(dir, entries, r.ShowHidden), nil
}
