package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// WorkspaceAdd is POST /api/connections/{id}/workspaces (ADR 0012).
type WorkspaceAdd struct {
	SourceID  string          `json:"sourceId"`
	Input     json.RawMessage `json:"input"`
	RequestID string          `json:"requestId"`
	ProjectID *int64          `json:"projectId,omitempty"`
	Confirmed bool            `json:"confirmed,omitempty"`
}

// AddedWorkspace is the 201 body. Agent callers get no path, message or
// facts (ADR 0010 projection).
type AddedWorkspace struct {
	Workspace struct {
		db.Destination
		Path string `json:"path,omitempty"`
	} `json:"workspace"`
	Group    db.MergeSource   `json:"group"`
	Existing bool             `json:"existing"`
	Message  string           `json:"message,omitempty"`
	Facts    []execution.Fact `json:"facts,omitempty"`
}

// AddWorkspace asks the connection's harness to add a workspace. The server
// waits up to execution.WorkspaceAddTimeout for the harness, so this call
// outlasts the client's default timeout.
func (c *Client) AddWorkspace(ctx context.Context, connection int64, in WorkspaceAdd, caller *db.SessionRef) (AddedWorkspace, error) {
	var out AddedWorkspace
	long := *c
	if c.HTTP != nil {
		hc := *c.HTTP
		hc.Timeout = execution.WorkspaceAddTimeout + 30*time.Second
		long.HTTP = &hc
	}
	err := long.doCaller(ctx, http.MethodPost, fmt.Sprintf("/api/connections/%d/workspaces", connection), in, caller, &out)
	return out, err
}
