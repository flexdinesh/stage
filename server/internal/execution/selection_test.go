package execution

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHarnessWideOptions(t *testing.T) {
	ok := Availability{Available: true}
	caps := Capabilities{
		Options: []Option{{ID: "sandbox", Values: []OptionValue{StringValue("read-only"), StringValue("workspace")}}},
		Models: []Model{
			{Choice: Choice{ID: "fast", Availability: ok}, Options: []Option{{ID: "effort", Values: []OptionValue{StringValue("low"), StringValue("high")}}}},
			{Choice: Choice{ID: "plain", Availability: ok}},
		},
	}
	for _, c := range []struct {
		name string
		cfg  Config
		want string // "" valid, else an error substring
	}{
		{"harness option with model", Config{Model: "plain", Options: []ModelOption{{ID: "sandbox", Value: StringValue("workspace")}}}, ""},
		{"union of both", Config{Model: "fast", Options: []ModelOption{{ID: "sandbox", Value: StringValue("read-only")}, {ID: "effort", Value: StringValue("high")}}}, ""},
		{"harness option without model", Config{Options: []ModelOption{{ID: "sandbox", Value: StringValue("workspace")}}}, ""},
		{"model option without model", Config{Options: []ModelOption{{ID: "effort", Value: StringValue("high")}}}, "choose a model"},
		{"other model's option", Config{Model: "plain", Options: []ModelOption{{ID: "effort", Value: StringValue("high")}}}, "unavailable"},
		{"unknown value", Config{Model: "fast", Options: []ModelOption{{ID: "sandbox", Value: StringValue("anything")}}}, "unavailable"},
		{"duplicate", Config{Model: "fast", Options: []ModelOption{{ID: "sandbox", Value: StringValue("workspace")}, {ID: "sandbox", Value: StringValue("read-only")}}}, "duplicate"},
	} {
		err := caps.ValidateConfig(c.cfg)
		if (c.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
	}
	if got := caps.OptionsFor(&caps.Models[0]); len(got) != 2 || got[0].ID != "sandbox" || got[1].ID != "effort" {
		t.Fatalf("OptionsFor %+v", got)
	}
}

func TestOptionIDsNeverCollide(t *testing.T) {
	ok := Availability{Available: true}
	opt := func(id string) Option { return Option{ID: id, Values: []OptionValue{BoolValue(true)}} }
	for name, caps := range map[string]Capabilities{
		"harness twice":        {Options: []Option{opt("a"), opt("a")}},
		"model reuses harness": {Options: []Option{opt("a")}, Models: []Model{{Choice: Choice{ID: "m", Availability: ok}, Options: []Option{opt("a")}}}},
		"model twice":          {Models: []Model{{Choice: Choice{ID: "m", Availability: ok}, Options: []Option{opt("b"), opt("b")}}}},
	} {
		if err := caps.ValidateConfig(Config{Model: "m"}); err == nil || !strings.Contains(err.Error(), "malformed capabilities") {
			t.Errorf("%s: %v", name, err)
		}
	}
	distinct := Capabilities{Options: []Option{opt("a")}, Models: []Model{{Choice: Choice{ID: "m", Availability: ok}, Options: []Option{opt("b")}}, {Choice: Choice{ID: "n", Availability: ok}, Options: []Option{opt("b")}}}}
	if err := distinct.Valid(); err != nil {
		t.Fatal("models may share an id between themselves", err)
	}
}

func TestLaunchOptionKinds(t *testing.T) {
	ok := Availability{Available: true}
	off := BoolValue(false)
	worktree := BoolValue(true)
	caps := Capabilities{
		Options: []Option{
			{ID: "worktree", Kind: OptionBoolean, Label: "New worktree", Values: []OptionValue{BoolValue(true), BoolValue(false)}, Default: &off},
			{ID: "baseRef", Kind: OptionText, Label: "Base ref", DependsOn: &OptionDependency{Option: "worktree", Value: worktree}},
		},
		Models: []Model{{Choice: Choice{ID: "m", Availability: ok}, Options: []Option{{ID: "effort", Kind: OptionChoice, Values: []OptionValue{StringValue("low")}}}}},
	}
	opt := func(id string, v OptionValue) ModelOption { return ModelOption{ID: id, Value: v} }
	for _, c := range []struct {
		name string
		cfg  Config
		want string
	}{
		{"boolean", Config{Options: []ModelOption{opt("worktree", BoolValue(true))}}, ""},
		{"boolean takes no string", Config{Options: []ModelOption{opt("worktree", StringValue("true"))}}, "takes true or false"},
		{"text with its dependency", Config{Options: []ModelOption{opt("worktree", BoolValue(true)), opt("baseRef", StringValue("origin/main"))}}, ""},
		{"text, any string", Config{Model: "m", Options: []ModelOption{opt("worktree", BoolValue(true)), opt("baseRef", StringValue("feat/x"))}}, ""},
		{"text takes no boolean", Config{Options: []ModelOption{opt("worktree", BoolValue(true)), opt("baseRef", BoolValue(true))}}, "non-empty text"},
		{"empty text", Config{Options: []ModelOption{opt("worktree", BoolValue(true)), opt("baseRef", StringValue("  "))}}, "non-empty text"},
		{"dependency unset reads its default", Config{Options: []ModelOption{opt("baseRef", StringValue("origin/main"))}}, "applies only when worktree is true"},
		{"dependency off", Config{Options: []ModelOption{opt("worktree", BoolValue(false)), opt("baseRef", StringValue("origin/main"))}}, "applies only when worktree is true"},
		{"unknown option", Config{Options: []ModelOption{opt("branch", StringValue("x"))}}, "choose a model"},
		{"unknown option with model", Config{Model: "m", Options: []ModelOption{opt("branch", StringValue("x"))}}, "unavailable"},
	} {
		err := caps.ValidateConfig(c.cfg)
		if (c.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
	}
}

func TestMalformedOptionDeclarations(t *testing.T) {
	ok := Availability{Available: true}
	yes := BoolValue(true)
	text := StringValue("x")
	for name, caps := range map[string]Capabilities{
		"unknown kind":        {Options: []Option{{ID: "a", Kind: "slider"}}},
		"text with values":    {Options: []Option{{ID: "a", Kind: OptionText, Values: []OptionValue{text}}}},
		"boolean with string": {Options: []Option{{ID: "a", Kind: OptionBoolean, Values: []OptionValue{text}}}},
		"bad default":         {Options: []Option{{ID: "a", Kind: OptionBoolean, Default: &text}}},
		"unknown dependency":  {Options: []Option{{ID: "a", Kind: OptionText, DependsOn: &OptionDependency{Option: "b", Value: yes}}}},
		"self dependency":     {Options: []Option{{ID: "a", Kind: OptionBoolean, DependsOn: &OptionDependency{Option: "a", Value: yes}}}},
		"dependency value":    {Options: []Option{{ID: "a", Kind: OptionBoolean}, {ID: "b", Kind: OptionText, DependsOn: &OptionDependency{Option: "a", Value: text}}}},
		"other model's option": {Models: []Model{
			{Choice: Choice{ID: "m", Availability: ok}, Options: []Option{{ID: "a", Kind: OptionBoolean}}},
			{Choice: Choice{ID: "n", Availability: ok}, Options: []Option{{ID: "b", Kind: OptionText, DependsOn: &OptionDependency{Option: "a", Value: yes}}}},
		}},
	} {
		if err := caps.Valid(); err == nil || !strings.Contains(err.Error(), "malformed capabilities") {
			t.Errorf("%s: %v", name, err)
		}
	}
	wide := Capabilities{Options: []Option{{ID: "a", Kind: OptionBoolean}}, Models: []Model{{Choice: Choice{ID: "m", Availability: ok}, Options: []Option{{ID: "b", Kind: OptionText, DependsOn: &OptionDependency{Option: "a", Value: yes}}}}}}
	if err := wide.Valid(); err != nil {
		t.Fatal("a model option may depend on a harness-wide one:", err)
	}
}

func TestOptionKindDefaultsForOlderHarnesses(t *testing.T) {
	var o Option
	if err := json.Unmarshal([]byte(`{"id":"fast","values":[true,false],"default":false}`), &o); err != nil {
		t.Fatal(err)
	}
	if o.EffectiveKind() != OptionBoolean || o.Title() != "fast" {
		t.Fatalf("boolean values read as %q %q", o.EffectiveKind(), o.Title())
	}
	if (Option{ID: "effort", Values: []OptionValue{StringValue("low")}}).EffectiveKind() != OptionChoice {
		t.Fatal("string values read as choice")
	}
	b, _ := json.Marshal(Option{ID: "baseRef", Kind: OptionText, Label: "Base ref", Values: []OptionValue{}, DependsOn: &OptionDependency{Option: "worktree", Value: BoolValue(true)}})
	if string(b) != `{"id":"baseRef","kind":"text","label":"Base ref","values":[],"default":null,"dependsOn":{"option":"worktree","value":true}}` {
		t.Fatalf("json %s", b)
	}
}
