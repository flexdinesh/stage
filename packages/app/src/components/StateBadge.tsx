import type { NotificationEvent, TaskState } from "../api/types.ts";
import { notificationEventLabel, notificationEventTone, type NotificationSubject } from "../lib/notification-events.ts";
import type { StatusChip } from "../lib/tasks.ts";
import { Chip, type ChipTone } from "./ui.tsx";

const TASK_TONES: Record<TaskState, ChipTone> = {
  backlog: "idle",
  launching: "run",
  running: "run",
  needs_input: "need",
  done: "done",
  failed: "bad",
  interrupted: "bad",
  cancelled: "idle",
};

const TASK_LABELS: Record<TaskState, string> = {
  backlog: "backlog",
  launching: "Launch unconfirmed",
  running: "running",
  needs_input: "needs input",
  done: "run finished",
  failed: "failed",
  interrupted: "interrupted",
  cancelled: "cancelled",
};

export const taskStateLabel = (s: TaskState) => TASK_LABELS[s];
export const taskStateTone = (s: TaskState) => TASK_TONES[s];

/** Label and tone of one status chip (see `statusChips`). */
export function statusChipView(c: StatusChip): { label: string; tone: ChipTone } {
  switch (c) {
    case "completed":
      return { label: "completed", tone: "ok" };
    case "to_review":
      return { label: "to review", tone: "done" };
    case "ready":
    case "blocked":
      return { label: c, tone: "idle" };
    default:
      return { label: taskStateLabel(c), tone: taskStateTone(c) };
  }
}

/** A notification event's chip (lib/notification-events.ts): `subject` tells a session's done from an older task's. */
export const NotificationEventBadge = ({ event, subject }: { event: NotificationEvent; subject?: NotificationSubject }) => (
  <Chip tone={notificationEventTone(event)} dot>
    {notificationEventLabel(event, subject)}
  </Chip>
);

export const TaskStateBadge = ({ state, className }: { state: TaskState; className?: string | undefined }) => (
  <Chip tone={TASK_TONES[state]} dot className={className}>
    {TASK_LABELS[state]}
  </Chip>
);
