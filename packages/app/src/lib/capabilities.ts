import type { Capabilities, ExecutionConfig, Harness, HarnessModel, HarnessOption, ModelDefaults } from "../api/types.ts";

/** What a config may set: the harness-wide options, then the model's own. */
export function optionsFor(caps: Pick<Capabilities, "options"> | undefined, model: Pick<HarnessModel, "options"> | undefined): HarnessOption[] {
  return [...(caps?.options ?? []), ...(model?.options ?? [])];
}

/** The harness the saved model presets (Settings pin, last used) describe; presets are not per connection yet. */
const PRESET_HARNESS = "t3";

/** New-task model prefill from a preset, for the harness it describes only: model and options (mode and permission start at the harness default). */
export function prefillConfig(defaults: ModelDefaults | undefined, harness: Harness | null): ExecutionConfig | undefined {
  if (defaults === undefined || harness !== PRESET_HARNESS) return undefined;
  const c = defaults.executionConfig;
  if (c !== undefined) return { model: c.model, options: c.options ?? [], permission: "", mode: "" };
  const model = defaults.providerInstanceId !== null && defaults.model !== null ? JSON.stringify([defaults.providerInstanceId, defaults.model]) : "";
  return { model, options: defaults.modelOptions, permission: "", mode: "" };
}
