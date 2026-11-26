import type { Capabilities, ExecutionConfig, HarnessOption, HarnessOptionKind, ModelOption } from "../api/types.ts";
import { optionsFor } from "./capabilities.ts";

/** The option's kind; one declared without (an older harness) is boolean when every value is boolean, else choice. */
export function optionKind(o: Pick<HarnessOption, "kind" | "values">): HarnessOptionKind {
  if (o.kind !== undefined) return o.kind;
  return o.values.length > 0 && o.values.every((v) => typeof v === "boolean") ? "boolean" : "choice";
}

/** The option's name in the harness's words, else its id. */
export function optionLabel(o: Pick<HarnessOption, "id" | "label">): string {
  return o.label || o.id;
}

/** The value a config gives the option: its selection, else the option's default (undefined: neither). */
export function optionValue(o: HarnessOption, selection: readonly ModelOption[]): string | boolean | undefined {
  const picked = selection.find((v) => v.id === o.id);
  if (picked !== undefined) return picked.value;
  return o.default ?? undefined;
}

/**
 * Whether the option applies under a selection: no dependency, or its dependency (one of `all`) has the value it
 * needs, selected or by default. A dependency the harness does not declare never applies.
 */
export function optionApplies(o: HarnessOption, selection: readonly ModelOption[], all: readonly HarnessOption[]): boolean {
  if (o.dependsOn === undefined) return true;
  const { option, value } = o.dependsOn;
  const dep = all.find((d) => d.id === option);
  return dep !== undefined && optionValue(dep, selection) === value;
}

/** Whether a selected value fits the option: one of a choice's values, a boolean, or a non-empty text. */
export function optionAccepts(o: HarnessOption, value: string | boolean): boolean {
  switch (optionKind(o)) {
    case "boolean":
      return typeof value === "boolean" && (o.values.length === 0 || o.values.includes(value));
    case "text":
      return typeof value === "string" && value.trim() !== "";
    case "choice":
      return o.values.includes(value);
  }
}

/**
 * Sets one option (undefined clears it; a text is trimmed and empty clears it), then drops every selection whose
 * dependency no longer holds, so turning worktree off also clears its base ref and branch.
 */
export function setOption(
  selection: readonly ModelOption[],
  all: readonly HarnessOption[],
  id: string,
  value: string | boolean | undefined,
): ModelOption[] {
  const v = typeof value === "string" ? value.trim() : value;
  let next = [...selection.filter((o) => o.id !== id), ...(v === undefined || v === "" ? [] : [{ id, value: v }])];
  for (;;) {
    const kept = next.filter((s) => {
      const o = all.find((d) => d.id === s.id);
      return o === undefined || optionApplies(o, next, all);
    });
    if (kept.length === next.length) return kept;
    next = kept;
  }
}

/** The options a form shows under a selection, in declared order: those whose dependency holds. */
export function shownOptions(all: readonly HarnessOption[], selection: readonly ModelOption[]): HarnessOption[] {
  return all.filter((o) => optionApplies(o, selection, all));
}

/**
 * The saved choices the harness no longer offers: the model, mode or permission ids, and each option that is
 * undeclared, does not take its value, or whose dependency does not hold.
 */
export function incompatibleSettings(config: ExecutionConfig, caps: Capabilities | undefined): string[] {
  if (!caps) return [];
  const model = caps.models?.find((m) => m.id === config.model && m.available);
  const issues: string[] = [];
  if (config.model && !model) issues.push("model");
  if (config.mode && !caps.modes?.some((m) => m.id === config.mode && m.available)) issues.push("mode");
  if (config.permission && !(model?.permissions ?? caps.permissions)?.some((p) => p.id === config.permission && p.available))
    issues.push("permission");
  const allowed = optionsFor(caps, model);
  for (const o of config.options) {
    const option = allowed.find((v) => v.id === o.id);
    if (option === undefined || !optionAccepts(option, o.value) || !optionApplies(option, config.options, allowed)) issues.push(o.id);
  }
  return issues;
}
