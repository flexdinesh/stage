import type { Thread } from "../api/types.ts";

/** Anchor of the task batch panel listing the tasks a session created. */
export const CREATED_TASKS_ANCHOR = "created-tasks";
export const canLinkSession = (thread: Thread) => thread.parentThreadId === null && !thread.archived;
