// Notification events by subject (docs/api.md "Notification rules"): what each event is called, where its row
// opens, and the account filter's groups. Each label names its subject; "done" never stands alone.
import type { Notification, NotificationEvent } from "../api/types.ts";
import type { ChipTone } from "../components/ui/tone.tsx";

/** What a notification is about. An identity-less row reads as a session's. */
export type NotificationSubject = "task" | "tree" | "schedule" | "session";

const TASK_URL = /^\/tasks\/\d+$/;
const SUBJECT_URL = /^\/(trees|schedules)\/\d+$/;

export function notificationSubject(n: Pick<Notification, "event" | "taskId" | "url">): NotificationSubject {
  if (n.event === "tree_completed") return "tree";
  if (n.event === "schedule_started" || n.event === "schedule_skipped") return "schedule";
  // A deleted task's rows keep its URL.
  return n.taskId !== null || TASK_URL.test(n.url) ? "task" : "session";
}

const LABELS: Record<NotificationEvent, string> = {
  needs_input: "needs input",
  failed: "failed",
  ready: "ready",
  completed: "completed",
  tree_completed: "tree completed",
  schedule_started: "schedule started",
  schedule_skipped: "schedule skipped",
  done: "session finished",
};

const TONES: Record<NotificationEvent, ChipTone> = {
  needs_input: "need",
  failed: "bad",
  ready: "idle",
  completed: "ok",
  tree_completed: "ok",
  schedule_started: "run",
  schedule_skipped: "idle",
  done: "done",
};

/** The event's chip copy. `done` is a session's finished run; on an older task row, the task's ("run finished"). */
export const notificationEventLabel = (event: NotificationEvent, subject: NotificationSubject = "session") =>
  event === "done" && subject === "task" ? "run finished" : LABELS[event];

export const notificationEventTone = (event: NotificationEvent): ChipTone => TONES[event];

/** Where a row opens: its task, tree or schedule (a deleted tree or schedule keeps its stored page), else the inbox. */
export function notificationPath(n: Pick<Notification, "taskId" | "treeId" | "scheduleId" | "url">): string {
  if (n.taskId !== null) return `/tasks/${n.taskId}`;
  if (n.treeId !== null) return `/trees/${n.treeId}`;
  if (n.scheduleId !== null) return `/schedules/${n.scheduleId}`;
  return SUBJECT_URL.test(n.url) ? n.url : "/?inbox";
}

/** One event in the account filter: its label and a short hint naming the trigger (and who does not cause it). */
export interface NotificationEventChoice {
  event: NotificationEvent;
  label: string;
  hint: string;
}

/** The account filter, grouped by subject, in display order (the server's order). */
export const NOTIFICATION_EVENT_GROUPS: readonly { title: string; note?: string; events: readonly NotificationEventChoice[] }[] = [
  {
    title: "Tasks",
    note: "needs input and failed also cover untracked sessions",
    events: [
      { event: "needs_input", label: "Needs input", hint: "waits on you" },
      { event: "failed", label: "Failed", hint: "run failed" },
      { event: "ready", label: "Ready to start", hint: "needs completed" },
      { event: "completed", label: "Completed", hint: "by harness or agent, not you" },
    ],
  },
  {
    title: "Trees",
    events: [{ event: "tree_completed", label: "Tree completed", hint: "all tasks completed, not by you" }],
  },
  {
    title: "Schedules",
    events: [
      { event: "schedule_started", label: "Run started", hint: "a cron fire started a task" },
      { event: "schedule_skipped", label: "Run skipped", hint: "skipped or missed" },
    ],
  },
  {
    title: "Untracked sessions",
    events: [{ event: "done", label: "Session finished", hint: "run finished" }],
  },
];

/** Every event, in display order. */
export const NOTIFICATION_EVENTS: readonly NotificationEvent[] = NOTIFICATION_EVENT_GROUPS.flatMap((g) => g.events.map((e) => e.event));
