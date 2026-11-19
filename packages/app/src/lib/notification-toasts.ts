import type { Notification } from "../api/types.ts";

const entityKey = (n: Notification) => {
  if (n.taskId !== null) return `task:${n.taskId}`;
  // Older payloads may omit the connection: they key by session alone (never assume a fixed connection id).
  if (n.externalSessionId !== null) return `thread:${n.connectionId ?? ""}:${n.externalSessionId}`;
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
      current.currentState === n.currentState
      ? n
      : { ...n, currentState: current.currentState };
    if (refreshed.event === "needs_input" && refreshed.currentState !== null && refreshed.currentState !== "needs_input") {
      return [];
    }
    return [refreshed];
  });
}
