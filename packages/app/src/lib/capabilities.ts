import type { Capabilities, ExecutionConfig, HarnessConnection, OperationId } from "../api/types.ts";
import { modelName } from "./format.ts";
import { optionLabel, optionValueLabel, optionsFor } from "./options.ts";

export { optionsFor } from "./options.ts";

/** New-task prefill from a connection's pin: model and options only (mode and permission start at the harness default). */
export function prefillConfig(connection: Pick<HarnessConnection, "newTaskConfig"> | undefined): ExecutionConfig | undefined {
  const c = connection?.newTaskConfig;
  if (c === null || c === undefined) return undefined;
  return { model: c.model, options: c.options ?? [], permission: "", mode: "" };
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
  steer: "Steer running sessions",
  queue: "Queue messages",
  queueManage: "Manage queued messages",
  activity: "Recent activity",
  sessionRename: "Rename sessions with the task title",
  workspaceAdd: "Add workspaces from Stage",
  directoryBrowse: "Browse folders for paths",
};

const OPERATIONS: readonly OperationId[] = ["launch", "stop", "adopt", "images", "respond", "send", "steer", "queue", "queueManage", "activity", "sessionRename"];

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
 * then explicit boolean overrides and other non-default options (harness-wide, then the model's own; undeclared ones
 * by id), permission and mode when set. Nothing for a config with no overrides.
 */
export function launchParts(config: ExecutionConfig | null | undefined, caps: Pick<Capabilities, "models" | "options" | "permissions" | "modes" | "modeLabel"> | undefined): LaunchPart[] {
  if (config === null || config === undefined) return [];
  const model = caps?.models?.find((m) => m.id === config.model);
  const declared = optionsFor(caps, model);
  const options = config.options ?? [];
  const parts: LaunchPart[] = [];
  if (config.model !== "") parts.push({ label: "Model", value: model?.label || (modelName(config.model) ?? config.model) });
  const ordered = [...declared.flatMap((o) => options.filter((v) => v.id === o.id)), ...options.filter((v) => !declared.some((o) => o.id === v.id))];
  for (const v of ordered) {
    const o = declared.find((d) => d.id === v.id);
    if (typeof v.value !== "boolean" && o?.default === v.value) continue;
    parts.push({ label: o === undefined ? v.id : optionLabel(o), value: optionValueLabel(o, v.value) });
  }
  const permission = (model?.permissions ?? caps?.permissions)?.find((p) => p.id === config.permission);
  if (config.permission !== "") parts.push({ label: "Permission", value: permission?.label || config.permission });
  const mode = caps?.modes?.find((m) => m.id === config.mode);
  if (config.mode !== "") parts.push({ label: caps?.modeLabel || "Interaction mode", value: mode?.label || config.mode });
  return parts;
}
