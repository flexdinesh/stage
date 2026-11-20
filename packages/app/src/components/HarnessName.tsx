import { useHarnesses } from "../api/queries.ts";
import type { Task } from "../api/types.ts";
import { isRetired } from "../lib/harnesses.ts";
import { Chip, cx } from "./ui.tsx";

/** `replaced`: the task's connection was replaced by another instance of its harness (read-only history). */
export function ReplacedChip({ task, className }: { task: Pick<Task, "harness" | "connectionActive" | "connectionLabel">; className?: string }) {
  const { kindName } = useHarnesses();
  if (!isRetired(task)) return null;
  const label = task.connectionLabel === "" ? kindName(task.harness) : `${kindName(task.harness)} '${task.connectionLabel}'`;
  return (
    <Chip tone="idle" title={`Ran on ${label}, which was replaced. Read-only.`} className={className}>
      replaced
    </Chip>
  );
}

/** The task's harness name, with the `replaced` chip when its connection was replaced. */
export function HarnessName({ task, className }: { task: Pick<Task, "harness" | "connectionActive" | "connectionLabel">; className?: string }) {
  const { kindName } = useHarnesses();
  return (
    <span className={cx("inline-flex items-center gap-1.5", className)}>
      {kindName(task.harness)}
      <ReplacedChip task={task} />
    </span>
  );
}
