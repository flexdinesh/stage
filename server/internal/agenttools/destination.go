package agenttools

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

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
	cs, err := o.Client.Connections(ctx)
	if err != nil {
		return 0, err
	}
	id := a.ConnectionID
	if id == 0 {
		switch {
		case len(p.Bindings) == 1:
			id = p.Bindings[0].ConnectionID
		case c.ThreadID != "" || a.legacyConfig():
			id = db.T3ConnectionID
		case len(p.Bindings) > 1:
			return 0, errors.New("this project has multiple harness bindings; select connectionId from list_projects")
		case c.Session != nil:
			id = c.Session.ConnectionID
		case len(cs) == 1:
			id = cs[0].ID
		default:
			return 0, errors.New("choose a destination connectionId from list_projects")
		}
	}
	if !slices.ContainsFunc(cs, func(c db.HarnessConnection) bool { return c.ID == id }) {
		return 0, fmt.Errorf("connection %d is unavailable; select a configured connection from list_projects", id)
	}
	return id, nil
}

func available(what string, a execution.Availability) error {
	if a.Available {
		return nil
	}
	reason := a.Reason
	if strings.TrimSpace(reason) == "" {
		reason = "choose an available value from list_models for this connection"
	}
	return fmt.Errorf("%s is unavailable: %s", what, reason)
}

func checkChoice(what, id string, choices []execution.Choice) error {
	if id == "" {
		return nil
	}
	for _, c := range choices {
		if c.ID == id {
			return available(what+" "+id, c.Availability)
		}
	}
	return available(what+" "+id, execution.Availability{})
}
func validateConfig(c execution.Config, f execution.Capabilities) error {
	if err := checkChoice("interaction mode", c.Mode, f.Modes); err != nil {
		return err
	}
	if c.Model == "" {
		if c.Permission != "" || len(c.Options) > 0 {
			return errors.New("choose a model before setting permissions or model options")
		}
		return nil
	}
	for _, m := range f.Models {
		if m.ID != c.Model {
			continue
		}
		if err := available("model "+m.ID, m.Availability); err != nil {
			return err
		}
		if err := checkChoice("permission", c.Permission, m.Permissions); err != nil {
			return err
		}
		for _, v := range c.Options {
			if !slices.ContainsFunc(m.Options, func(o execution.Option) bool { return o.ID == v.ID && slices.Contains(o.Values, v.Value) }) {
				return fmt.Errorf("option %s is unavailable for this model; choose a value from list_models", v.ID)
			}
		}
		return nil
	}
	return available("model "+c.Model, execution.Availability{})
}
