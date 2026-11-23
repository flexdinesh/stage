import type { Capabilities, ExecutionConfig, HarnessConnection, HarnessModel, HarnessOption, ProjectBinding } from "../api/types.ts";

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
