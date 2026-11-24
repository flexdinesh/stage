package storetest

import (
	"errors"
	"reflect"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/t3"
)

func init() {
	register("LegacyT3ConfigMatchesAdapter", caseLegacyT3ConfigMatchesAdapter)
	register("LegacyT3InputsStoreExecutionConfig", caseLegacyT3InputsStoreExecutionConfig)
	register("LegacyT3PatchMergesExecutionConfig", caseLegacyT3PatchMergesExecutionConfig)
	register("LegacyT3ScheduleInputsStoreExecutionConfig", caseLegacyT3ScheduleInputsStoreExecutionConfig)
}

// The storage translation of the legacy T3 model inputs is the T3 adapter's.
func caseLegacyT3ConfigMatchesAdapter(t *testing.T, _ Factory) {
	opts := []execution.ModelOption{{ID: "effort", Value: execution.StringValue("high")}}
	for _, c := range []struct {
		provider, model *string
		opts            []execution.ModelOption
		runtime, mode   string
	}{
		{ptr("codex"), ptr("gpt-6"), opts, "auto", "plan"},
		{ptr("claude"), ptr(`a"b<c>`), []execution.ModelOption{}, "full-access", "default"},
		{nil, nil, []execution.ModelOption{}, "approval-required", "default"},
		{ptr("codex"), nil, []execution.ModelOption{}, "full-access", "default"},
	} {
		provider, model := "", ""
		if c.provider != nil {
			provider = *c.provider
		}
		if c.model != nil {
			model = *c.model
		}
		got := store.LegacyT3Config(c.provider, c.model, c.opts, c.runtime, c.mode)
		want := t3.LegacyConfig(provider, model, c.opts, c.runtime, c.mode)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("LegacyT3Config(%q, %q) = %+v, t3.LegacyConfig = %+v", provider, model, got, want)
		}
	}
}

// A T3 create with legacy model inputs stores their executionConfig;
// duplicate copies it. Defaults apply when the modes are unset.
func caseLegacyT3InputsStoreExecutionConfig(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "legacy")
	opts := []execution.ModelOption{{ID: "effort", Value: execution.StringValue("high")}}
	task, err := s.CreateTask(ctx, db.NewTask{ProjectID: p.ID, Title: "legacy", Prompt: "p",
		ProviderInstanceID: ptr("codex"), Model: ptr("gpt-6"), ModelOptions: opts, RuntimeMode: "auto", InteractionMode: "plan"})
	if err != nil {
		t.Fatal(err)
	}
	want := execution.Config{Model: t3.ModelKey("codex", "gpt-6"), Options: opts, Permission: "auto", Mode: "plan"}
	if task.ExecutionConfig == nil || !reflect.DeepEqual(*task.ExecutionConfig, want) {
		t.Fatalf("create config %+v, want %+v", task.ExecutionConfig, want)
	}
	dup, err := s.DuplicateTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if dup.ExecutionConfig == nil || !reflect.DeepEqual(*dup.ExecutionConfig, want) {
		t.Fatalf("duplicate config %+v, want %+v", dup.ExecutionConfig, want)
	}
	bare, err := s.CreateTask(ctx, db.NewTask{ProjectID: p.ID, Title: "bare", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	defaults := execution.Config{Options: []execution.ModelOption{}, Permission: "full-access", Mode: "default"}
	if bare.ExecutionConfig == nil || !reflect.DeepEqual(*bare.ExecutionConfig, defaults) {
		t.Fatalf("bare config %+v, want %+v", bare.ExecutionConfig, defaults)
	}
	given := execution.Config{Model: "m", Options: []execution.ModelOption{}, Permission: "auto"}
	explicit, err := s.CreateTask(ctx, db.NewTask{ProjectID: p.ID, Title: "explicit", Prompt: "p", ExecutionConfig: &given, RuntimeMode: "full-access"})
	if err != nil {
		t.Fatal(err)
	}
	if explicit.ExecutionConfig == nil || !reflect.DeepEqual(*explicit.ExecutionConfig, given) {
		t.Fatalf("explicit config %+v, want %+v", explicit.ExecutionConfig, given)
	}
}

// A patch with some legacy model fields sets only their part of the stored
// executionConfig.
func caseLegacyT3PatchMergesExecutionConfig(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "legacy")
	opts := []execution.ModelOption{{ID: "effort", Value: execution.StringValue("high")}}
	task, err := s.CreateTask(ctx, db.NewTask{ProjectID: p.ID, Title: "legacy", Prompt: "p",
		ProviderInstanceID: ptr("codex"), Model: ptr("gpt-6"), ModelOptions: opts, RuntimeMode: "auto", InteractionMode: "plan"})
	if err != nil {
		t.Fatal(err)
	}
	set := func(v string) db.Optional[string] { return db.Optional[string]{Set: true, V: v} }
	steps := []struct {
		name  string
		patch db.TaskPatch
		want  execution.Config
	}{
		{"runtime mode only", db.TaskPatch{RuntimeMode: set("approval-required")},
			execution.Config{Model: t3.ModelKey("codex", "gpt-6"), Options: opts, Permission: "approval-required", Mode: "plan"}},
		{"model pair", db.TaskPatch{ProviderInstanceID: set("claude"), Model: set("haiku")},
			execution.Config{Model: t3.ModelKey("claude", "haiku"), Options: opts, Permission: "approval-required", Mode: "plan"}},
		{"options and mode", db.TaskPatch{ModelOptions: db.Optional[[]execution.ModelOption]{Set: true, Null: true}, InteractionMode: set("default")},
			execution.Config{Model: t3.ModelKey("claude", "haiku"), Options: []execution.ModelOption{}, Permission: "approval-required", Mode: "default"}},
		{"model cleared", db.TaskPatch{ProviderInstanceID: db.Optional[string]{Set: true, Null: true}, Model: db.Optional[string]{Set: true, Null: true}},
			execution.Config{Options: []execution.ModelOption{}, Permission: "approval-required", Mode: "default"}},
	}
	for _, st := range steps {
		got, err := s.UpdateTask(ctx, task.ID, st.patch)
		if err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}
		if got.ExecutionConfig == nil || !reflect.DeepEqual(*got.ExecutionConfig, st.want) {
			t.Fatalf("%s: config %+v, want %+v", st.name, got.ExecutionConfig, st.want)
		}
	}
	var invalid *db.InvalidError
	if _, err := s.UpdateTask(ctx, task.ID, db.TaskPatch{Model: set("m")}); !errors.As(err, &invalid) {
		t.Fatalf("model without provider: %v", err)
	}
}

// Schedules store the legacy model inputs as executionConfig, merge legacy
// edits into it, and spawn tasks with it.
func caseLegacyT3ScheduleInputsStoreExecutionConfig(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "legacy")
	sc, err := s.CreateSchedule(ctx, db.NewSchedule{ProjectID: p.ID, Title: "nightly", Prompt: "p", Cron: "0 2 * * *", Tz: "UTC",
		ProviderInstanceID: ptr("codex"), Model: ptr("gpt-6"), RuntimeMode: "auto", InteractionMode: "plan"})
	if err != nil {
		t.Fatal(err)
	}
	want := execution.Config{Model: t3.ModelKey("codex", "gpt-6"), Options: []execution.ModelOption{}, Permission: "auto", Mode: "plan"}
	if sc.ExecutionConfig == nil || !reflect.DeepEqual(*sc.ExecutionConfig, want) {
		t.Fatalf("schedule config %+v, want %+v", sc.ExecutionConfig, want)
	}
	sc, err = s.UpdateSchedule(ctx, sc.ID, db.SchedulePatch{InteractionMode: db.Optional[string]{Set: true, V: "default"}})
	if err != nil {
		t.Fatal(err)
	}
	want.Mode = "default"
	if sc.ExecutionConfig == nil || !reflect.DeepEqual(*sc.ExecutionConfig, want) {
		t.Fatalf("patched schedule config %+v, want %+v", sc.ExecutionConfig, want)
	}
	task, err := s.RunScheduleNow(ctx, sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.ExecutionConfig == nil || !reflect.DeepEqual(*task.ExecutionConfig, want) {
		t.Fatalf("spawned config %+v, want %+v", task.ExecutionConfig, want)
	}
}
