import type { Capabilities, ExecutionConfig, HarnessConnection, HarnessModel, HarnessOption, ModelOption, OperationId, ProjectBinding } from "../api/types.ts";
import { modelName } from "./format.ts";

/** What a config may set: the harness-wide options, then the model's own. */
export function optionsFor(caps: Pick<Capabilities, "options"> | undefined, model: Pick<HarnessModel, "options"> | undefined): HarnessOption[] {
  return [...(caps?.options ?? []), ...(model?.options ?? [])];
}

/** New-task prefill from a connection's pin: model and options only (mode and permission start at the harness default). */
export function prefillConfig(connection: Pick<HarnessConnection, "newTaskConfig"> | undefined): ExecutionConfig | undefined {
  const c = connection?.newTaskConfig;
  if (c === null || c === undefined) return undefined;
  return { model: c.model, options: c.options ?? [], permission: "", mode: "" };
}

/**
 * The connection a new task on a project starts on (0: none; the user picks), as the server picks one for a request
 * without it: the default when the project is available there, else the project's only active binding, else (with a
 * default set) its first available binding, the main machine's first, then by machine and connection id.
 */
export function newTaskConnection(
  bindings: readonly Pick<ProjectBinding, "connectionId" | "connectionActive" | "status">[],
  defaultId: number | null,
  machineOf: (connectionId: number) => number,
): number {
  const active = bindings.filter((b) => b.connectionActive);
  const available = active.filter((b) => b.status === "available");
  if (defaultId !== null && available.some((b) => b.connectionId === defaultId)) return defaultId;
  const only = active.length === 1 ? active[0] : undefined;
  if (only !== undefined) return only.connectionId;
  if (defaultId === null) return 0;
  const first = available.toSorted((a, b) => machineOf(a.connectionId) - machineOf(b.connectionId) || a.connectionId - b.connectionId)[0];
  return first?.connectionId ?? 0;
}

/** What each declared operation lets a user do, in Stage's words (one map for every harness; never by harness). */
export const OPERATION_LABELS: Record<OperationId, string> = {
  launch: "Start tasks",
  stop: "Stop from Stage",
  adopt: "Track existing sessions",
  reply: "Show the final reply",
  opening: "Show a tracked session's first prompt",
  attention: "Waiting sessions in the inbox",
  images: "Prompt images",
  respond: "Answer approvals and questions in Stage",
  send: "Send follow-ups",
};

const OPERATIONS: readonly OperationId[] = ["launch", "stop", "adopt", "reply", "opening", "attention", "images", "respond", "send"];

/** One line of a connection's capability list. */
export interface CapabilityLine {
  op: OperationId;
  label: string;
  available: boolean;
  /** The harness's reason when unavailable; null when it gave none. */
  reason: string | null;
}

/** The operations a connection declares, in Stage's fixed order with their labels; undeclared ones are left out. */
export function capabilityLines(operations: Capabilities["operations"]): CapabilityLine[] {
  return OPERATIONS.flatMap((op) => {
    const o = operations[op];
    if (o === undefined) return [];
    const reason = o.available || o.reason === undefined || o.reason === "" ? null : o.reason;
    return [{ op, label: OPERATION_LABELS[op], available: o.available, reason }];
  });
}

/** One part of a task's launch line: what was chosen, in the harness's words. */
export interface LaunchPart {
  label: string;
  value: string;
}

/**
 * A task's chosen launch options in the harness's own labels and order: the model (its label, else the saved id),
 * then each option set to something other than its default (harness-wide, then the model's own; undeclared ones by
 * id), permission and mode when set. Nothing for a config at the harness defaults.
 */
export function launchParts(config: ExecutionConfig | null | undefined, caps: Pick<Capabilities, "models" | "options" | "permissions" | "modes" | "modeLabel"> | undefined): LaunchPart[] {
  if (config === null || config === undefined) return [];
  const model = caps?.models?.find((m) => m.id === config.model);
  const declared = optionsFor(caps, model);
  const parts: LaunchPart[] = [];
  if (config.model !== "") parts.push({ label: "Model", value: model?.label || (modelName(config.model) ?? config.model) });
  const ordered = [...declared.flatMap((o) => config.options.filter((v) => v.id === o.id)), ...config.options.filter((v) => !declared.some((o) => o.id === v.id))];
  for (const v of ordered) {
    const o = declared.find((d) => d.id === v.id);
    if (o !== undefined && o.default !== undefined && o.default !== null && o.default === v.value) continue;
    parts.push({ label: o?.label || v.id, value: optionText(v) });
  }
  const permission = (model?.permissions ?? caps?.permissions)?.find((p) => p.id === config.permission);
  if (config.permission !== "") parts.push({ label: "Permission", value: permission?.label || config.permission });
  const mode = caps?.modes?.find((m) => m.id === config.mode);
  if (config.mode !== "") parts.push({ label: caps?.modeLabel || "Interaction mode", value: mode?.label || config.mode });
  return parts;
}

const optionText = (v: ModelOption): string => (typeof v.value === "boolean" ? (v.value ? "on" : "off") : v.value);
