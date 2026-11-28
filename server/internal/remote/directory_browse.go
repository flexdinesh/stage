package remote

import (
	"context"
	"path"
	"strings"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

var _ execution.DirectoryBrowser = (*Adapter)(nil)

// Browse lists a folder through the worker's adapter.
func (a *Adapter) Browse(ctx context.Context, r execution.BrowseRequest) (execution.BrowseResult, error) {
	var out execution.BrowseResult
	err := callTrait[execution.DirectoryBrowser](ctx, a, KindDirectoryBrowse, r, &out)
	return out, err
}

// browseDirectory is the worker side of adapter.directoryBrowse.
func browseDirectory(ctx context.Context, a execution.Adapter, env workerchan.Envelope, _ workerchan.Upcall) (any, error) {
	browser, ok := execution.As[execution.DirectoryBrowser](a)
	if !ok {
		return nil, unsupported("folder browsing")
	}
	var r execution.BrowseRequest
	if err := decode(env, &r); err != nil {
		return nil, &execution.Invalid{Reason: "Malformed folder browse request."}
	}
	if len(r.Path) > execution.MaxBrowsePath || (strings.TrimSpace(r.Path) != "" && !path.IsAbs(r.Path)) {
		return nil, &execution.Invalid{Reason: "Enter an absolute folder path."}
	}
	return browser.Browse(ctx, r)
}
