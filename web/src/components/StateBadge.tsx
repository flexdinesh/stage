import type { ChainState, TaskState, ThreadStatus } from "../api/types.ts";
import { Badge, type Tone } from "./ui.tsx";

const TASK_TONES: Record<TaskState, Tone> = {
  backlog: "gray",
  launching: "blue",
  running: "blue",
  needs_input: "amber",
  completed: "teal",
  settled: "green",
  failed: "red",
  interrupted: "red",
  cancelled: "gray",
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

const CHAIN_TONES: Record<ChainState, Tone> = { idle: "gray", running: "blue", paused: "amber", done: "green" };

const THREAD_TONES: Record<ThreadStatus, Tone> = {
  idle: "gray",
  preparing: "blue",
  queued: "blue",
  starting: "blue",
  running: "blue",
  waiting: "amber",
  completed: "teal",
  interrupted: "red",
  failed: "red",
  cancelled: "gray",
  rolled_back: "gray",
};

export const taskStateLabel = (s: TaskState) => TASK_LABELS[s];

export const TaskStateBadge = ({ state }: { state: TaskState }) => (
  <Badge tone={TASK_TONES[state]}>{TASK_LABELS[state]}</Badge>
);

export const ChainStateBadge = ({ state }: { state: ChainState }) => <Badge tone={CHAIN_TONES[state]}>{state}</Badge>;

export const ThreadStatusBadge = ({ status }: { status: ThreadStatus }) => (
  <Badge tone={THREAD_TONES[status]}>{status.replace("_", " ")}</Badge>
);
