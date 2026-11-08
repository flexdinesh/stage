import type { ChainState, TaskState, ThreadStatus } from "../api/types.ts";
import { Chip, type ChipTone } from "./ui.tsx";

const TASK_TONES: Record<TaskState, ChipTone> = {
  backlog: "idle",
  launching: "run",
  running: "run",
  needs_input: "need",
  completed: "done",
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
  completed: "completed",
  settled: "settled",
  failed: "failed",
  interrupted: "interrupted",
  cancelled: "cancelled",
};

const CHAIN_TONES: Record<ChainState, ChipTone> = { idle: "idle", running: "run", paused: "need", done: "ok" };

const THREAD_TONES: Record<ThreadStatus, ChipTone> = {
  idle: "idle",
  preparing: "run",
  queued: "run",
  starting: "run",
  running: "run",
  waiting: "need",
  completed: "done",
  interrupted: "bad",
  failed: "bad",
  cancelled: "idle",
  rolled_back: "idle",
};

export const taskStateLabel = (s: TaskState) => TASK_LABELS[s];

export const TaskStateBadge = ({ state }: { state: TaskState }) => (
  <Chip tone={TASK_TONES[state]} dot>
    {TASK_LABELS[state]}
  </Chip>
);

export const ChainStateBadge = ({ state }: { state: ChainState }) => (
  <Chip tone={CHAIN_TONES[state]} dot>
    {state}
  </Chip>
);

export const ThreadStatusBadge = ({ status }: { status: ThreadStatus }) => (
  <Chip tone={THREAD_TONES[status]} dot>
    {status.replace("_", " ")}
  </Chip>
);
