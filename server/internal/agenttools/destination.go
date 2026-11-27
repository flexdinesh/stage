package agenttools

import (
	"errors"
	"fmt"
	"slices"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// ConfigInput keeps JSON string and boolean option values distinct on the MCP wire.
type ConfigInput struct {
	Model   string `json:"model,omitempty"`
	Options []struct {
		ID    string `json:"id"`
		Value any    `json:"value" jsonschema:"a value the connection declares in list_models (harness-wide options and the model's own): one of a choice's values, true or false for a boolean, any text for a text option (e.g. a worktree base ref)"`
	} `json:"options,omitempty"`
	Permission string `json:"permission,omitempty"`
	Mode       string `json:"mode,omitempty"`
}

func (in ConfigInput) config() (execution.Config, error) {
	c := execution.Config{Model: in.Model, Permission: in.Permission, Mode: in.Mode}
	seen := map[string]bool{}
	for _, o := range in.Options {
		if o.ID == "" || seen[o.ID] {
			return c, errors.New("each model option needs one distinct id")
		}
		seen[o.ID] = true
		var value execution.OptionValue
		switch v := o.Value.(type) {
		case string:
			value = execution.StringValue(v)
		case bool:
			value = execution.BoolValue(v)
		default:
			return c, fmt.Errorf("model option %s requires a string or boolean", o.ID)
		}
		c.Options = append(c.Options, execution.ModelOption{ID: o.ID, Value: value})
	}
	return c, nil
}

const destinationHelp = "select connectionId and workspaceId from list_projects, or use projects ls to find values for --connection and --workspace-id"

func legacyConnection(a ConfigArgs, p db.Project) (int64, error) {
	id := a.ConnectionID
	if a.BindingID != nil {
		i := slices.IndexFunc(p.Bindings, func(b db.ProjectBinding) bool { return b.ConnectionActive && b.ID == *a.BindingID })
		if i < 0 || (id != 0 && id != p.Bindings[i].ConnectionID) {
			return 0, fmt.Errorf("%w, %s", db.ErrInvalidBinding, destinationHelp)
		}
		return p.Bindings[i].ConnectionID, nil
	}
	if id != 0 {
		return id, nil
	}
	available := 0
	for _, b := range p.Bindings {
		if !b.ConnectionActive {
			continue
		}
		if id != 0 && id != b.ConnectionID {
			return 0, fmt.Errorf("this project has bindings on multiple connections, %s", destinationHelp)
		}
		id = b.ConnectionID
		if b.Status == "available" {
			available++
		}
	}
	if id == 0 {
		return 0, fmt.Errorf("this project has no active binding, %s", destinationHelp)
	}
	if available > 1 {
		return 0, fmt.Errorf("this project has multiple available workspaces, %s", destinationHelp)
	}
	return id, nil
}
