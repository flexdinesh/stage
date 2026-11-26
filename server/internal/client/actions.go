package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// ActionScope names a Stage object. Project actions also select a connection.
type ActionScope struct {
	ConnectionID   *int64 `json:"connectionId,omitempty" jsonschema:"target connection ID"`
	TaskID         *int64 `json:"taskId,omitempty" jsonschema:"target task ID"`
	ProjectID      *int64 `json:"projectId,omitempty" jsonschema:"target project ID"`
	OnConnectionID *int64 `json:"onConnectionId,omitempty" jsonschema:"required for projectId; connection on which to target the project"`
}

func (s ActionScope) Validate() error {
	count := 0
	for _, id := range []*int64{s.ConnectionID, s.TaskID, s.ProjectID} {
		if id != nil {
			if *id <= 0 {
				return errors.New("action target ID must be positive")
			}
			count++
		}
	}
	if count != 1 {
		return errors.New("exactly one connection, task, or project is required")
	}
	if s.ProjectID != nil {
		if s.OnConnectionID == nil || *s.OnConnectionID <= 0 {
			return errors.New("a project action requires a positive on-connection ID")
		}
	} else if s.OnConnectionID != nil {
		return errors.New("on-connection is only for project actions")
	}
	return nil
}

func (s ActionScope) endpoint(action string) string {
	var path string
	switch {
	case s.ConnectionID != nil:
		path = fmt.Sprintf("/api/connections/%d/actions", *s.ConnectionID)
	case s.TaskID != nil:
		path = taskPath(*s.TaskID, "actions")
	default:
		path = fmt.Sprintf("/api/projects/%d/actions", *s.ProjectID)
	}
	if action != "" {
		path += "/" + url.PathEscape(action)
	}
	if s.OnConnectionID != nil {
		path += "?connectionId=" + strconv.FormatInt(*s.OnConnectionID, 10)
	}
	return path
}

// ActionCaller carries transport context; the server makes authorization decisions.
type ActionCaller struct {
	Session   *db.SessionRef
	Agent     bool
	Confirmed bool
}

// ListActions reads the action declarations for a target.
func (c *Client) ListActions(ctx context.Context, scope ActionScope, caller ActionCaller) ([]execution.Action, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	out := []execution.Action{}
	err := c.actionRequest(ctx, http.MethodGet, scope.endpoint(""), nil, caller, &out)
	return out, err
}

// RunAction dispatches an action with its raw JSON object.
func (c *Client) RunAction(ctx context.Context, scope ActionScope, action string, input json.RawMessage, caller ActionCaller) (execution.ActionResult, error) {
	var out execution.ActionResult
	if err := scope.Validate(); err != nil {
		return out, err
	}
	if strings.TrimSpace(action) == "" {
		return out, errors.New("action ID is required")
	}
	if input == nil {
		input = json.RawMessage("{}")
	}
	object := bytes.TrimSpace(input)
	if len(object) == 0 || object[0] != '{' || !json.Valid(input) {
		return out, errors.New("action input must be a JSON object")
	}
	err := c.actionRequest(ctx, http.MethodPost, scope.endpoint(action), input, caller, &out)
	return out, err
}

func (c *Client) actionRequest(ctx context.Context, method, path string, input json.RawMessage, caller ActionCaller, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(input))
	if err != nil {
		return err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if caller.Agent || caller.Session != nil {
		req.Header.Set("Stage-Action-Agent", "1")
	}
	if caller.Session != nil {
		req.Header.Set("Stage-Caller-Session", fmt.Sprintf("%d:%s", caller.Session.ConnectionID, caller.Session.ExternalSessionID))
	}
	if caller.Confirmed && method == http.MethodPost {
		req.Header.Set("Stage-Action-Confirmed", "1")
	}
	_, err = c.send(req, out)
	return err
}
