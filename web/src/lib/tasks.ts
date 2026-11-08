import type { Task, TaskState } from "../api/types.ts";

const STARTABLE: readonly TaskState[] = ["backlog", "failed", "interrupted", "cancelled"];
const ACTIVE: readonly TaskState[] = ["launching", "running", "needs_input"];

/** POST /tasks/:id/start allowed. A backlog chain step is started only by its chain. */
export const canStart = (t: Task) => STARTABLE.includes(t.state) && !(t.state === "backlog" && t.chainId !== null);
/** Thread is live: stop allowed, delete refused. */
export const isActive = (t: Task) => ACTIVE.includes(t.state);
export const canStop = (t: Task) => t.t3ThreadId !== null && isActive(t);
/** Outside backlog only title, tags, notes may change. */
export const isFullyEditable = (t: Task) => t.state === "backlog";
