package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// ---- adding harness workspaces (ADR 0012) ----

// WorkspaceSourcesArgs is list_workspace_sources.
type WorkspaceSourcesArgs struct {
	ConnectionID int64 `json:"connectionId" jsonschema:"the harness connection from list_connections or list_projects"`
}

// WorkspaceSourcesOut is whether the connection can add workspaces and how.
type WorkspaceSourcesOut struct {
	Availability execution.Availability     `json:"availability"`
	Sources      []execution.WorkspaceSource `json:"sources"`
}

func (o *Ops) ListWorkspaceSources(ctx context.Context, a WorkspaceSourcesArgs) (WorkspaceSourcesOut, error) {
	if a.ConnectionID <= 0 {
		return WorkspaceSourcesOut{}, errors.New("connectionId is required: choose a harness connection from list_connections")
	}
	c, err := o.Client.Capabilities(ctx, a.ConnectionID)
	if err != nil {
		return WorkspaceSourcesOut{}, err
	}
	out := WorkspaceSourcesOut{Availability: c.Operations[execution.OpWorkspaceAdd], Sources: c.WorkspaceSources}
	if out.Sources == nil {
		out.Sources = []execution.WorkspaceSource{}
	}
	if !out.Availability.Available && out.Availability.Reason == "" {
		out.Availability.Reason = "This connection does not declare adding workspaces."
	}
	return out, nil
}

// AddWorkspaceArgs is add_workspace.
type AddWorkspaceArgs struct {
	CallerArgs
	ConnectionID int64          `json:"connectionId" jsonschema:"the harness connection that adds the workspace"`
	SourceID     string         `json:"sourceId" jsonschema:"a source id from list_workspace_sources"`
	Input        map[string]any `json:"input,omitempty" jsonschema:"input object matching the source's declared fields"`
	RequestID    string         `json:"requestId" jsonschema:"required key (at most 256 bytes); retry an uncertain outcome with the same requestId"`
	ProjectID    *int64         `json:"projectId,omitempty" jsonschema:"Stage project group to put the workspace in; omitted lets Stage group it"`
	Confirmed    bool           `json:"confirmed,omitempty" jsonschema:"true only after the user confirmed the source's confirm text"`
}

func (o *Ops) AddWorkspace(ctx context.Context, a AddWorkspaceArgs) (client.AddedWorkspace, error) {
	if a.ConnectionID <= 0 {
		return client.AddedWorkspace{}, errors.New("connectionId is required: choose a harness connection from list_connections")
	}
	if strings.TrimSpace(a.RequestID) == "" {
		return client.AddedWorkspace{}, errors.New("requestId is required: reuse it only to retry the same request")
	}
	input := json.RawMessage("{}")
	if a.Input != nil {
		var err error
		if input, err = json.Marshal(a.Input); err != nil {
			return client.AddedWorkspace{}, err
		}
	}
	c, err := o.caller(ctx, a.CallerArgs)
	if err != nil {
		return client.AddedWorkspace{}, err
	}
	return o.Client.AddWorkspace(ctx, a.ConnectionID, client.WorkspaceAdd{
		SourceID: a.SourceID, Input: input, RequestID: a.RequestID, ProjectID: a.ProjectID, Confirmed: a.Confirmed,
	}, c.Session)
}
