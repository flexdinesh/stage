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

// Valid rejects malformed capabilities: option ids that collide (twice
// harness-wide, or a model option reusing a harness-wide id or its own),
// and options that do not fit their kind or depend on an option outside
// their config's set.
func (f Capabilities) Valid() error {
	wide := map[string]bool{}
	for _, o := range f.Options {
		if wide[o.ID] {
			return fmt.Errorf("malformed capabilities: harness option %s defined twice", o.ID)
		}
		wide[o.ID] = true
	}
	if err := validDeclarations(f.Options); err != nil {
		return err
	}
	for _, m := range f.Models {
		own := map[string]bool{}
		for _, o := range m.Options {
			if wide[o.ID] || own[o.ID] {
				return fmt.Errorf("malformed capabilities: model %s redefines option %s", m.ID, o.ID)
			}
			own[o.ID] = true
		}
		if err := validDeclarations(f.OptionsFor(&m)); err != nil {
			return fmt.Errorf("%w (model %s)", err, m.ID)
		}
	}
	return nil
}

// validDeclarations checks each option's kind, values and dependency
// within set (the options one config may set).
func validDeclarations(set []Option) error {
	for _, o := range set {
		if err := validDeclaration(o); err != nil {
			return err
		}
		if o.DependsOn == nil {
			continue
		}
		i := slices.IndexFunc(set, func(d Option) bool { return d.ID == o.DependsOn.Option })
		if i < 0 || set[i].ID == o.ID {
			return fmt.Errorf("malformed capabilities: option %s depends on unknown option %s", o.ID, o.DependsOn.Option)
		}
		if set[i].check(o.DependsOn.Value) != nil {
			return fmt.Errorf("malformed capabilities: option %s depends on a value option %s does not take", o.ID, o.DependsOn.Option)
		}
	}
	return nil
}

func validDeclaration(o Option) error {
	switch o.EffectiveKind() {
	case OptionChoice:
	case OptionBoolean:
		if slices.ContainsFunc(o.Values, func(v OptionValue) bool { return !v.IsBool }) {
			return fmt.Errorf("malformed capabilities: boolean option %s has a non-boolean value", o.ID)
		}
	case OptionText:
		if len(o.Values) > 0 {
			return fmt.Errorf("malformed capabilities: text option %s lists values", o.ID)
		}
	default:
		return fmt.Errorf("malformed capabilities: option %s has unknown kind %q", o.ID, o.Kind)
	}
	if o.Default != nil && o.check(*o.Default) != nil {
		return fmt.Errorf("malformed capabilities: option %s has a default it does not take", o.ID)
	}
	return nil
}

// check is whether o takes v: one of a choice's values, a boolean (one of
// its values when it lists them), or a non-empty text.
func (o Option) check(v OptionValue) error {
	switch o.EffectiveKind() {
	case OptionBoolean:
		if !v.IsBool || (len(o.Values) > 0 && !slices.Contains(o.Values, v)) {
			return fmt.Errorf("option %s takes true or false", o.ID)
		}
	case OptionText:
		if v.IsBool || strings.TrimSpace(v.Str) == "" {
			return fmt.Errorf("option %s takes a non-empty text; leave it out for the harness default", o.ID)
		}
	default:
		if !slices.Contains(o.Values, v) {
			return fmt.Errorf("option %s is unavailable for this model; choose a value from list_models", o.ID)
		}
	}
	return nil
}

// validOptions checks each option once against allowed: it must be
// declared, take its value, and have its dependency met (the dependency's
// selected value, else its default).
func validOptions(options []ModelOption, allowed []Option) error {
	seen := map[string]bool{}
	for _, v := range options {
		if seen[v.ID] {
			return fmt.Errorf("duplicate model option %s", v.ID)
		}
		seen[v.ID] = true
		i := slices.IndexFunc(allowed, func(o Option) bool { return o.ID == v.ID })
		if i < 0 {
			return fmt.Errorf("option %s is unavailable for this model; choose a value from list_models", v.ID)
		}
		if err := allowed[i].check(v.Value); err != nil {
			return err
		}
	}
	for _, v := range options {
		o := allowed[slices.IndexFunc(allowed, func(o Option) bool { return o.ID == v.ID })]
		if o.DependsOn == nil {
			continue
		}
		dep := allowed[slices.IndexFunc(allowed, func(d Option) bool { return d.ID == o.DependsOn.Option })]
		got, set := Config{Options: options}.Lookup(dep.ID)
		if !set && dep.Default != nil {
			got, set = *dep.Default, true
		}
		if !set || got != o.DependsOn.Value {
			want, _ := o.DependsOn.Value.MarshalJSON()
			return fmt.Errorf("option %s applies only when %s is %s", o.ID, dep.ID, want)
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
