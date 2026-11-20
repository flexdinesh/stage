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

// PermissionsFor is m's permission list; nil (not reported) inherits the harness list.
func (f Capabilities) PermissionsFor(m Model) []Choice {
	if m.Permissions == nil {
		return f.Permissions
	}
	return m.Permissions
}

// OptionsFor is what a config for m may set: the harness-wide options, then
// m's own (nil m: the harness-wide ones only).
func (f Capabilities) OptionsFor(m *Model) []Option {
	out := slices.Clone(f.Options)
	if m != nil {
		out = append(out, m.Options...)
	}
	return out
}

// Valid rejects capabilities whose option ids collide: twice harness-wide,
// or a model option reusing a harness-wide id (or its own twice).
func (f Capabilities) Valid() error {
	wide := map[string]bool{}
	for _, o := range f.Options {
		if wide[o.ID] {
			return fmt.Errorf("malformed capabilities: harness option %s defined twice", o.ID)
		}
		wide[o.ID] = true
	}
	for _, m := range f.Models {
		own := map[string]bool{}
		for _, o := range m.Options {
			if wide[o.ID] || own[o.ID] {
				return fmt.Errorf("malformed capabilities: model %s redefines option %s", m.ID, o.ID)
			}
			own[o.ID] = true
		}
	}
	return nil
}

// validOptions checks each option once against allowed.
func validOptions(options []ModelOption, allowed []Option) error {
	seen := map[string]bool{}
	for _, v := range options {
		if seen[v.ID] {
			return fmt.Errorf("duplicate model option %s", v.ID)
		}
		seen[v.ID] = true
		if !slices.ContainsFunc(allowed, func(o Option) bool { return o.ID == v.ID && slices.Contains(o.Values, v.Value) }) {
			return fmt.Errorf("option %s is unavailable for this model; choose a value from list_models", v.ID)
		}
	}
	return nil
}

func (f Capabilities) ValidateConfig(c Config) error {
	if err := f.Valid(); err != nil {
		return err
	}
	if err := RequireChoice("interaction mode", c.Mode, f.Modes); err != nil {
		return err
	}
	if c.Model == "" {
		if err := RequireChoice("permission", c.Permission, f.Permissions); err != nil {
			return err
		}
		for _, v := range c.Options {
			if !slices.ContainsFunc(f.Options, func(o Option) bool { return o.ID == v.ID }) {
				return errors.New("choose a model before setting model options")
			}
		}
		return validOptions(c.Options, f.Options)
	}
	for _, m := range f.Models {
		if m.ID != c.Model {
			continue
		}
		if err := RequireAvailable("model "+m.ID, m.Availability); err != nil {
			return err
		}
		if err := RequireChoice("permission", c.Permission, f.PermissionsFor(m)); err != nil {
			return err
		}
		return validOptions(c.Options, f.OptionsFor(&m))
	}
	return RequireAvailable("model "+c.Model, Availability{})
}
