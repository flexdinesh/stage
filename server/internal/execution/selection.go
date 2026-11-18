package execution

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

func RequireAvailable(what string, a Availability) error {
	if a.Available {
		return nil
	}
	reason := a.Reason
	if strings.TrimSpace(reason) == "" {
		reason = "choose an available value from list_models for this connection"
	}
	return fmt.Errorf("%s is unavailable: %s", what, reason)
}

func RequireChoice(what, id string, choices []Choice) error {
	if id == "" {
		return nil
	}
	for _, c := range choices {
		if c.ID == id {
			return RequireAvailable(what+" "+id, c.Availability)
		}
	}
	return RequireAvailable(what+" "+id, Availability{})
}
func (f Capabilities) ValidateConfig(c Config) error {
	if err := RequireChoice("interaction mode", c.Mode, f.Modes); err != nil {
		return err
	}
	if c.Model == "" {
		if err := RequireChoice("permission", c.Permission, f.Permissions); err != nil {
			return err
		}
		if len(c.Options) > 0 {
			return errors.New("choose a model before setting permissions or model options")
		}
		return nil
	}
	for _, m := range f.Models {
		if m.ID != c.Model {
			continue
		}
		if err := RequireAvailable("model "+m.ID, m.Availability); err != nil {
			return err
		}
		if err := RequireChoice("permission", c.Permission, m.Permissions); err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, v := range c.Options {
			if seen[v.ID] {
				return fmt.Errorf("duplicate model option %s", v.ID)
			}
			seen[v.ID] = true
			if !slices.ContainsFunc(m.Options, func(o Option) bool { return o.ID == v.ID && slices.Contains(o.Values, v.Value) }) {
				return fmt.Errorf("option %s is unavailable for this model; choose a value from list_models", v.ID)
			}
		}
		return nil
	}
	return RequireAvailable("model "+c.Model, Availability{})
}
