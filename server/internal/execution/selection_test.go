package execution

import (
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
