import type { Task, TaskState } from "../api/types.ts";

const STARTABLE: readonly TaskState[] = ["backlog", "failed", "interrupted", "cancelled"];
const ACTIVE: readonly TaskState[] = ["launching", "running", "needs_input"];

/** POST /tasks/:id/start allowed. On a waiting task it is "Start now" (unlinks it). */
export const canStart = (t: Task) => STARTABLE.includes(t.state);
/** Backlog task linked to run after another: it starts when that one settles. */
export const isWaiting = (t: Task) => t.state === "backlog" && (t.afterTaskId !== null || t.afterThreadId !== null);

/** What a waiting task waits on, for button copy: "#12" or "session <title>". */
export const waitTarget = (t: Task) =>
  t.afterTaskId !== null ? `#${t.afterTaskId}` : t.afterThreadId !== null ? `session ${t.afterThreadTitle ?? t.afterThreadId}` : "";
/** Thread is live: stop allowed, delete refused. */
export const isActive = (t: Task) => ACTIVE.includes(t.state);
export const canStop = (t: Task) => t.t3ThreadId !== null && isActive(t);
/** Outside backlog only title, tags, notes may change. */
export const isFullyEditable = (t: Task) => t.state === "backlog";
/** Settled: no longer gates anything and can't be linked. */
export const isFinal = (t: Task) => t.state === "settled";

// ---- Session batch (tasks one session created) ----

/** Can be picked for "link in order": an independent backlog task (not in a batch chain). */
export const canSelectForOrder = (t: Task) => t.state === "backlog";

/** Reorder is offered while no chain step has started. */
export const canReorder = (chain: readonly Task[]) => chain.length > 1 && chain.every((t) => t.state === "backlog");

/**
 * What a chain's Start does, from its head: "start" (followers already wait), "start_now" (head is timed),
 * "confirm" (head waits on a task or session: Start now unlinks it), or "none" (head already ran).
 */
export type ChainAction = "start" | "start_now" | "confirm" | "none";
export function chainAction(head: Task): ChainAction {
  if (head.state !== "backlog") return "none";
  if (isWaiting(head)) return "confirm";
  return head.startAt !== null ? "start_now" : "start";
}

/** "2 of 3 settled" once a step has settled, else null. */
export function chainProgress(chain: readonly Task[]): string | null {
  const settled = chain.filter((t) => t.state === "settled").length;
  return settled === 0 ? null : `${settled} of ${chain.length} settled`;
}

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
export const batchSize = (b: { chains: readonly (readonly Task[])[]; independent: readonly Task[] }) =>
  b.chains.reduce((n, c) => n + c.length, 0) + b.independent.length;
