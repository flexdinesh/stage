package client

import (
	"context"
	"fmt"
	"net/http"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// SetSteerer is POST /api/trees/{id}/steerer.
type SetSteerer struct {
	Prompt          string            `json:"prompt"`
	Title           string            `json:"title,omitempty"`
	ConnectionID    int64             `json:"connectionId"`
	WorkspaceID     string            `json:"workspaceId"`
	ExecutionConfig *execution.Config `json:"executionConfig,omitempty"`
	Replace         bool              `json:"replace,omitempty"`
	RequestID       string            `json:"requestId"`
}

// SteererResult is the set and stop steering response.
type SteererResult struct {
	Tree TreeDetail `json:"tree"`
	// Task: the new steerer (set only).
	Task     *Task    `json:"task,omitempty"`
	Warnings []string `json:"warnings"`
}

// SteeringEvents is a page of GET /api/trees/{id}/steering/events.
type SteeringEvents struct {
	Events  []db.SteeringEvent `json:"events"`
	HasMore bool               `json:"hasMore"`
}

// SetTreeSteerer creates, binds and launches tree id's steerer.
func (c *Client) SetTreeSteerer(ctx context.Context, id int64, in SetSteerer, caller *db.SessionRef) (SteererResult, error) {
	var out SteererResult
	err := c.doCaller(ctx, http.MethodPost, treePath(id, "steerer"), in, caller, &out)
	return out, err
}

// EndTreeSteering ends tree id's steerer binding and stops its session.
func (c *Client) EndTreeSteering(ctx context.Context, id int64, caller *db.SessionRef) (SteererResult, error) {
	var out SteererResult
	err := c.doCaller(ctx, http.MethodDelete, treePath(id, "steerer"), nil, caller, &out)
	return out, err
}

// TreeSteeringEvents reads tree id's steering events after cursor after
// (limit 0: the server's default).
func (c *Client) TreeSteeringEvents(ctx context.Context, id, after int64, limit int) (SteeringEvents, error) {
	path := fmt.Sprintf("%s?after=%d", treePath(id, "steering/events"), after)
	if limit > 0 {
		path += fmt.Sprintf("&limit=%d", limit)
	}
	var out SteeringEvents
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

// EscalateTask is escalate_to_user: the caller (the active steerer of the
// task's tree) notifies the user about task id's wait now, with reason.
func (c *Client) EscalateTask(ctx context.Context, id int64, reason string, caller *db.SessionRef) (Task, error) {
	var out Task
	err := c.doCaller(ctx, http.MethodPost, taskPath(id, "escalate"), map[string]string{"reason": reason}, caller, &out)
	return out, err
}
