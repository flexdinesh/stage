import type { NotificationEvent, TaskState, ThreadStatus } from "../api/types.ts";
import { Chip, type ChipTone } from "./ui.tsx";

const TASK_TONES: Record<TaskState, ChipTone> = {
  backlog: "idle",
  launching: "run",
  running: "run",
  needs_input: "need",
  done: "done",
  settled: "ok",
  failed: "bad",
  interrupted: "bad",
  cancelled: "idle",
};

const TASK_LABELS: Record<TaskState, string> = {
  backlog: "backlog",
  launching: "launching",
  running: "running",
  needs_input: "needs input",
  done: "done",
  settled: "settled",
  failed: "failed",
  interrupted: "interrupted",
  cancelled: "cancelled",
};

const THREAD_TONES: Record<ThreadStatus, ChipTone> = {
  idle: "idle",
  preparing: "run",
  queued: "run",
  starting: "run",
  running: "run",
  waiting: "run",
  completed: "done",
  interrupted: "bad",
  failed: "bad",
  cancelled: "idle",
  rolled_back: "idle",
};

export const taskStateLabel = (s: TaskState) => TASK_LABELS[s];
export const taskStateTone = (s: TaskState) => TASK_TONES[s];

/** Notification event copy: task states, plus "ready" (a manual tree task whose needs settled; idle tone). */
export const notificationEventLabel = (e: NotificationEvent) => (e === "ready" ? "ready" : TASK_LABELS[e]);
export const notificationEventTone = (e: NotificationEvent): ChipTone => (e === "ready" ? "idle" : TASK_TONES[e]);

export const NotificationEventBadge = ({ event }: { event: NotificationEvent }) => (
  <Chip tone={notificationEventTone(event)} dot>
    {notificationEventLabel(event)}
  </Chip>
);

/** T3 `completed` reads "done" (the run finished; settling happens in T3). */
export const threadStatusLabel = (s: ThreadStatus) => (s === "completed" ? "done" : s.replace("_", " "));

export const TaskStateBadge = ({ state, className }: { state: TaskState; className?: string | undefined }) => (
  <Chip tone={TASK_TONES[state]} dot className={className}>
    {TASK_LABELS[state]}
  </Chip>
);

/** A settled thread shows "settled" whatever its run status. */
export const ThreadStatusBadge = ({ status, settled }: { status: ThreadStatus; settled: boolean }) =>
  settled ? (
    <Chip tone="ok" dot>
      settled
    </Chip>
  ) : (
    <Chip tone={THREAD_TONES[status]} dot>
      {threadStatusLabel(status)}
    </Chip>
  );
