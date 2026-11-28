package agenttools

import (
	"context"
	"crypto/rand"
	"strings"

	"github.com/flexdinesh/stage/server/internal/client"
)

// ---- tree steering (ADR 0013) ----

// SetTreeSteererArgs is set_tree_steerer.
type SetTreeSteererArgs struct {
	CallerArgs
	TreeID          int64        `json:"treeId" jsonschema:"tree to steer"`
	Prompt          string       `json:"prompt" jsonschema:"what the steerer session starts with: how to steer this tree"`
	Title           string       `json:"title,omitempty" jsonschema:"steerer task title (default Steer: <tree title>)"`
	ConnectionID    int64        `json:"connectionId" jsonschema:"destination connection from list_projects; its harness must offer send"`
	WorkspaceID     string       `json:"workspaceId" jsonschema:"exact destination workspace on the connection from list_projects"`
	ExecutionConfig *ConfigInput `json:"executionConfig,omitempty" jsonschema:"model, options, permission and mode from list_models; omitted = the connection's pinned model, else the harness defaults"`
	Replace         bool         `json:"replace,omitempty" jsonschema:"replace the current steerer: end its binding and stop its session"`
	RequestID       string       `json:"requestId,omitempty" jsonschema:"idempotency key; reuse it only to retry the identical request (default: a new one)"`
}

func (o *Ops) SetTreeSteerer(ctx context.Context, a SetTreeSteererArgs) (client.SteererResult, error) {
	c, err := o.caller(ctx, a.CallerArgs)
	if err != nil {
		return client.SteererResult{}, err
	}
	workspace := a.WorkspaceID
	rc, err := o.resolveConfig(ctx, ConfigArgs{ConnectionID: a.ConnectionID, WorkspaceID: &workspace, ExecutionConfig: a.ExecutionConfig, Title: a.Title, Prompt: a.Prompt}, c, true)
	if err != nil {
		return client.SteererResult{}, err
	}
	requestID := strings.TrimSpace(a.RequestID)
	if requestID == "" {
		requestID = "stage-steer:" + rand.Text()
	}
	return o.Client.SetTreeSteerer(ctx, a.TreeID, client.SetSteerer{
		Prompt: a.Prompt, Title: a.Title, ConnectionID: rc.connectionID, WorkspaceID: rc.workspaceID,
		ExecutionConfig: rc.config, Replace: a.Replace, RequestID: requestID,
	}, c.Session)
}

// TreeSteeringArgs names a steered tree.
type TreeSteeringArgs struct {
	CallerArgs
	TreeID int64 `json:"treeId" jsonschema:"tree id"`
}

func (o *Ops) EndTreeSteering(ctx context.Context, a TreeSteeringArgs) (client.SteererResult, error) {
	c, err := o.caller(ctx, a.CallerArgs)
	if err != nil {
		return client.SteererResult{}, err
	}
	return o.Client.EndTreeSteering(ctx, a.TreeID, c.Session)
}

// SteeringEventsArgs is get_steering_events.
type SteeringEventsArgs struct {
	TreeID int64 `json:"treeId" jsonschema:"tree id"`
	After  int64 `json:"after,omitempty" jsonschema:"return events after this event id (the last id you saw; default 0: from the start)"`
	Limit  int   `json:"limit,omitempty" jsonschema:"at most this many (1-200, default 50)"`
}

func (o *Ops) GetSteeringEvents(ctx context.Context, a SteeringEventsArgs) (client.SteeringEvents, error) {
	return o.Client.TreeSteeringEvents(ctx, a.TreeID, a.After, a.Limit)
}
