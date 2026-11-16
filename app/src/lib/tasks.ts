import type { Task, TaskState } from "../api/types.ts";

const STARTABLE: readonly TaskState[] = ["backlog", "failed", "interrupted", "cancelled"];
const ACTIVE: readonly TaskState[] = ["launching", "running", "needs_input"];

/** POST /tasks/:id/start allowed (never for an adopted session). On a blocked task it starts early and keeps its needs. */
export const canStart = (t: Task) => !t.adopted && STARTABLE.includes(t.state);
/** Backlog tree member with a need not settled yet: it starts when every need settles. */
export const isWaiting = (t: Task) => t.readiness === "blocked";

/** "#12", or "#3, #5": the tasks a task needs, for copy. */
export const waitTarget = (t: Pick<Task, "needs">) => t.needs.map((id) => `#${id}`).join(", ");
/** Thread is live: stop allowed, delete refused. */
export const isActive = (t: Task) => ACTIVE.includes(t.state);
export const canStop = (t: Task) => t.t3ThreadId !== null && isActive(t);
/** Outside backlog only title, tags, notes may change. */
export const isFullyEditable = (t: Task) => t.state === "backlog";
/** Settled: no longer gates anything. */
export const isFinal = (t: Task) => t.state === "settled";

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
