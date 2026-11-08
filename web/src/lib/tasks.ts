import type { Task, TaskState } from "../api/types.ts";

const STARTABLE: readonly TaskState[] = ["backlog", "failed", "interrupted", "cancelled"];
const ACTIVE: readonly TaskState[] = ["launching", "running", "needs_input"];

/** POST /tasks/:id/start allowed. On a waiting task it is "Start now" (unlinks it). */
export const canStart = (t: Task) => STARTABLE.includes(t.state);
/** Backlog task linked to run after another: it starts when that one settles. */
export const isWaiting = (t: Task) => t.state === "backlog" && t.afterTaskId !== null;
/** Thread is live: stop allowed, delete refused. */
export const isActive = (t: Task) => ACTIVE.includes(t.state);
export const canStop = (t: Task) => t.t3ThreadId !== null && isActive(t);
/** Outside backlog only title, tags, notes may change. */
export const isFullyEditable = (t: Task) => t.state === "backlog";
/** Settled or cancelled: no longer gates anything and can't be linked. */
export const isFinal = (t: Task) => t.state === "settled" || t.state === "cancelled";
