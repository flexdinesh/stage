import type { Notification } from "../api/types.ts";

const entityKey = (n: Notification) => {
  if (n.taskId !== null) return `task:${n.taskId}`;
  if (n.t3ThreadId !== null) return `thread:${n.t3ThreadId}`;
  return `notification:${n.id}`;
};

/** Refresh queued events from live state, removing needs-input alerts that resolved. */
export function reconcileToasts(queued: Notification[], latest: Notification[]): Notification[] {
  const live = new Map<string, Notification>();
  for (const n of latest) {
    const key = entityKey(n);
    const previous = live.get(key);
    if (previous === undefined || n.id > previous.id) live.set(key, n);
  }

  return queued.flatMap((n) => {
    const current = live.get(entityKey(n));
    const refreshed = current === undefined ||
      (current.currentState === n.currentState && current.pendingQuestionCount === n.pendingQuestionCount)
      ? n
      : { ...n, currentState: current.currentState, pendingQuestionCount: current.pendingQuestionCount };
    if (refreshed.event === "needs_input" && refreshed.currentState !== null && refreshed.currentState !== "needs_input") {
      return [];
    }
    return [refreshed];
  });
}
