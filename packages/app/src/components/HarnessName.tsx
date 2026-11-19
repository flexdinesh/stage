import type { Task } from "../api/types.ts";
import { HARNESS_NAME, isRetired } from "../lib/harnesses.ts";
import { Chip, cx } from "./ui.tsx";

/** `replaced`: the task's connection was replaced by another instance of its harness (read-only history). */
export function ReplacedChip({ task, className }: { task: Pick<Task, "harness" | "connectionActive" | "connectionLabel">; className?: string }) {
  if (!isRetired(task)) return null;
  const label = task.connectionLabel === "" ? HARNESS_NAME[task.harness] : `${HARNESS_NAME[task.harness]} '${task.connectionLabel}'`;
  return (
    <Chip tone="idle" title={`Ran on ${label}, which was replaced. Read-only.`} className={className}>
      replaced
    </Chip>
  );
}

/** The task's harness name, with the `replaced` chip when its connection was replaced. */
export function HarnessName({ task, className }: { task: Pick<Task, "harness" | "connectionActive" | "connectionLabel">; className?: string }) {
  return (
    <span className={cx("inline-flex items-center gap-1.5", className)}>
      {HARNESS_NAME[task.harness]}
      <ReplacedChip task={task} />
    </span>
  );
}
