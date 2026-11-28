package api

import (
	"context"
	"errors"
	"net/http"
	"path"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/remote"
)

const typePath = "Type the path."

// browseDirectories is GET /api/connections/{connectionId}/directories:
// child folders of ?path= (empty: the adapter's default start) on the
// harness's host, through the declared directoryBrowse operation. People
// only: agents, MCP and worker agent gateways type paths.
func (s *server) browseDirectories(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Cache-Control", "private, no-store")
	if !humanSurface(w, r, "Browse folders from the Stage app. Agents type paths.") {
		return nil
	}
	id, err := s.connectionPath(r)
	if err != nil {
		return err
	}
	params := r.URL.Query()
	req := execution.BrowseRequest{Path: params.Get("path")}
	switch hidden := params.Get("hidden"); hidden {
	case "", "0", "false":
	case "1", "true":
		req.ShowHidden = true
	default:
		return db.Invalid("hidden must be true or false")
	}
	if len(req.Path) > execution.MaxBrowsePath {
		return db.Invalid("path must be at most %d bytes", execution.MaxBrowsePath)
	}
	if strings.TrimSpace(req.Path) == "" {
		req.Path = ""
	} else if !path.IsAbs(req.Path) {
		return db.Invalid("Enter an absolute folder path.")
	}

	ctx, cancel := context.WithTimeout(r.Context(), remote.CallTimeout)
	defer cancel()
	c, err := s.Store.GetHarnessConnection(ctx, id)
	if err != nil {
		return err
	}
	if !c.Active {
		return db.ConnectionRetired()
	}
	a, err := s.Engine.Connections.Adapter(id)
	if err != nil {
		return err
	}
	features, err := a.Features(ctx)
	if err != nil {
		return writeBrowseError(w, err)
	}
	op := features.Operations[execution.OpDirectoryBrowse]
	browser, ok := execution.As[execution.DirectoryBrowser](a)
	if !ok || !op.Available {
		reason := op.Reason
		if reason == "" {
			reason = "This connection does not support folder browsing. " + typePath
		}
		return &db.ConflictError{Code: "operation_unavailable", Msg: reason}
	}
	out, err := browser.Browse(ctx, req)
	if err != nil {
		return writeBrowseError(w, err)
	}
	if out.Entries == nil {
		out.Entries = []execution.DirEntry{}
	}
	if len(out.Entries) > execution.MaxDirectoryEntries {
		out.Entries, out.Truncated = out.Entries[:execution.MaxDirectoryEntries], true
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// writeBrowseError answers an adapter failure: the harness's refusal (400),
// unavailable (409), an offline worker, or 502 directory_browse_failed.
func writeBrowseError(w http.ResponseWriter, err error) error {
	var (
		invalid     *execution.Invalid
		unavailable *execution.BrowseUnavailable
		offline     *remote.UnavailableError
		conflict    *db.ConflictError
	)
	switch {
	case errors.As(err, &invalid):
		return db.Invalid("%s", invalid.Reason)
	case errors.As(err, &unavailable):
		return &db.ConflictError{Code: "operation_unavailable", Msg: unavailable.Reason}
	case errors.As(err, &offline), errors.As(err, &conflict):
		return err
	case errors.Is(err, remote.ErrOffline):
		return &remote.UnavailableError{Code: remote.CodeMachineOffline, Message: "The Stage worker is offline. " + typePath}
	case errors.Is(err, execution.ErrUnavailable):
		return &db.ConflictError{Code: "operation_unavailable", Msg: "The harness is unavailable. " + typePath}
	}
	accessError(w, http.StatusBadGateway, "directory_browse_failed", "The harness could not list this folder. Retry or type the path.")
	return nil
}
