import type { Thread } from "../api/types.ts";

export const sessionPath = (id: string) => `/sessions/${encodeURIComponent(id)}`;
/** Anchor of the session detail panel listing the tasks a session created. */
export const CREATED_TASKS_ANCHOR = "created-tasks";
export const batchPath = (id: string) => `${sessionPath(id)}#${CREATED_TASKS_ANCHOR}`;
export const canLinkSession = (thread: Thread) => thread.parentThreadId === null && !thread.archived;
