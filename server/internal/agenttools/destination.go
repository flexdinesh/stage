package agenttools

import (
	"errors"
	"fmt"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/google/jsonschema-go/jsonschema"
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

// nullableConfigSchema is ConfigInput's input schema accepting null (an
// update clears the stored configuration).
var nullableConfigSchema = func() *jsonschema.Schema {
	s, err := jsonschema.For[ConfigInput](nil)
	if err != nil {
		panic(fmt.Sprintf("ConfigInput schema: %v", err))
	}
	s.Type, s.Types = "", []string{"object", "null"}
	return s
}()

const destinationHelp = "select connectionId and workspaceId from list_projects, or use projects ls to find values for --connection and --workspace-id"
