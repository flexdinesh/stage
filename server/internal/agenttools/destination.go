package agenttools

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// ConfigInput keeps JSON string and boolean option values distinct on the MCP wire.
type ConfigInput struct {
	Model   string `json:"model,omitempty"`
	Options []struct {
		ID    string `json:"id"`
		Value any    `json:"value" jsonschema:"a string or boolean advertised by the selected model"`
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

func (a ConfigArgs) legacyConfig() bool {
	return a.Provider != "" || a.Model != "" || a.RuntimeMode != "" || a.InteractionMode != "" || a.Effort != "" || len(a.Options) > 0
}

func (o *Ops) destination(ctx context.Context, a ConfigArgs, p db.Project, c agentctx.Caller) (int64, error) {
	h, err := o.harnesses(ctx)
	if err != nil {
		return 0, err
	}
	// Replaced connections take no new work; neither do their bindings.
	cs := h.active()
	var bindings []db.ProjectBinding
	for _, b := range p.Bindings {
		if b.ConnectionActive {
			bindings = append(bindings, b)
		}
	}
	id := a.ConnectionID
	if id == 0 {
		var configured []int64
		for _, conn := range cs {
			if conn.Configured {
				configured = append(configured, conn.ID)
			}
		}
		switch {
		case len(bindings) == 1:
			id = bindings[0].ConnectionID
		case a.legacyConfig():
			id = h.t3()
		case len(bindings) > 1:
			return 0, errors.New("this project has multiple harness bindings; select connectionId from list_projects")
		case c.Session != nil:
			id = c.Session.ConnectionID
		case len(configured) == 1:
			id = configured[0]
		case len(cs) == 1:
			id = cs[0].ID
		default:
			return 0, errors.New("choose a destination connectionId from list_projects")
		}
	}
	if !slices.ContainsFunc(cs, func(c db.HarnessConnection) bool { return c.ID == id }) {
		if slices.ContainsFunc(h, func(c db.HarnessConnection) bool { return c.ID == id }) {
			return 0, fmt.Errorf("connection %d was replaced; select the current connection from list_projects", id)
		}
		return 0, fmt.Errorf("connection %d is unavailable; select a configured connection from list_projects", id)
	}
	return id, nil
}
