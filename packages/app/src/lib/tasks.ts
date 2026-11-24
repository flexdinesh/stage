import type { Task, TaskState, UpdateTaskRequest } from "../api/types.ts";
import { changedFields } from "./changes.ts";
import { isRetired } from "./harnesses.ts";

const STARTABLE: readonly TaskState[] = ["backlog", "failed", "interrupted", "cancelled"];
const ACTIVE: readonly TaskState[] = ["launching", "running", "needs_input"];

/** POST /tasks/:id/start allowed (never for an adopted session or a replaced connection). Blocked tasks require user completion of every need. */
export const canStart = (t: Task) => !isRetired(t) && t.acceptanceId === null && !t.adopted && t.readiness !== "blocked" && (t.treeStatus === null || t.treeStatus === "open") && STARTABLE.includes(t.state);
/** Backlog tree member with a need not accepted yet: it starts when every need is completed. */
export const isWaiting = (t: Task) => t.readiness === "blocked";

/** "#12", or "#3, #5": the tasks a task needs, for copy. */
export const waitTarget = (t: Pick<Task, "needs">) => t.needs.map((id) => `#${id}`).join(", ");
/** Thread is live: stop allowed, delete refused. */
export const isActive = (t: Task) => ACTIVE.includes(t.state);
export const canStop = (t: Task) => !isRetired(t) && t.externalSessionId !== null && isActive(t);
/** Stage edits launch settings only for work it can start. */
export const isFullyEditable = (t: Task) => t.state === "backlog" && !t.adopted;
/** Accepted: satisfies dependents until explicitly reopened. */
export const isFinal = (t: Task) => t.acceptanceId !== null;

/** One status chip: acceptance ("completed", "to review"), readiness, or an execution state. */
export type StatusChip = TaskState | "completed" | "to_review" | "ready" | "blocked";

/** Execution states a completed task's chip leaves unsaid: the run it accepted, or no run at all. */
const QUIET_WHEN_COMPLETED: readonly TaskState[] = ["backlog", "done", "settled"];

/**
 * A task's status chips, acceptance first. Completed: "completed", plus the execution state only once execution moved
 * on (running again, needs input, failed...). A finished run not accepted yet reads "to review"; a backlog tree member
 * reads its readiness.
 */
export function statusChips(t: Pick<Task, "state" | "readiness" | "acceptanceId">, { blocked = false }: { blocked?: boolean } = {}): StatusChip[] {
  if (t.acceptanceId !== null) return QUIET_WHEN_COMPLETED.includes(t.state) ? ["completed"] : ["completed", t.state];
  if (t.state === "done" || t.state === "settled") return ["to_review"];
  // A ready task its connection cannot launch (launchBlock) is not ready: it reads backlog.
  if (t.state === "backlog" && t.readiness !== null) return [blocked && t.readiness === "ready" ? "backlog" : t.readiness];
  return [t.state];
}

// ---- Session batch (tasks one session created) ----

/** Copy of items with the one at `from` moved by `by` (clamped to the ends). */
export function moveItem<T>(items: readonly T[], from: number, by: number): T[] {
  const out = [...items];
  const to = Math.max(0, Math.min(out.length - 1, from + by));
  const [item] = out.splice(from, 1);
  if (item === undefined) return out;
  out.splice(to, 0, item);
  return out;
}

/** Number of tasks in a batch. */
export const batchSize = (b: { trees: readonly { tasks: readonly Task[] }[]; independent: readonly Task[] }) =>
  b.trees.reduce((n, t) => n + t.tasks.length, 0) + b.independent.length;

// ---- Editing ----

/** A task's editable fields as an edit form opened, in update-body shape: what `taskEditChanges` compares against. */
export function taskEditBaseline(task: Task): UpdateTaskRequest {
  const config = task.executionConfig;
  return {
    projectId: task.projectId,
    title: task.title,
    prompt: task.prompt,
    connectionId: task.connectionId,
    ...(config === null || config === undefined ? {} : { executionConfig: { ...config, options: config.options ?? [] } }),
    workspace: task.workspace,
    attachmentIds: task.attachments.map((a) => a.id),
  };
}

/**
 * The update body for a full edit: only the fields that differ from the baseline, so an untouched field never
 * reverts a change another client made while the form was open. A new project or harness also carries the
 * execution config and workspace, which the server checks against them.
 */
export function taskEditChanges(baseline: UpdateTaskRequest, next: UpdateTaskRequest): UpdateTaskRequest {
  const body = changedFields(baseline, next);
  if (body.projectId === undefined && body.connectionId === undefined) return body;
  return {
    ...body,
    ...(next.executionConfig === undefined ? {} : { executionConfig: next.executionConfig }),
    ...(next.workspace === undefined ? {} : { workspace: next.workspace }),
  };
}
