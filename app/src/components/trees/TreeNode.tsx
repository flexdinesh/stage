import { useMutation } from "@tanstack/react-query";
import { useState, type ReactNode, type Ref } from "react";
import { Link } from "react-router";
import { useInvalidateWork } from "../../api/queries.ts";
import type { Task } from "../../api/types.ts";
import { useApi } from "../../platform/context.tsx";
import { canStart } from "../../lib/tasks.ts";
import { nodeState } from "../../lib/trees.ts";
import { AnswerButton } from "../NeedsInputPanel.tsx";
import { TaskStateBadge } from "../StateBadge.tsx";
import { AppButton, Button, Chip, cx, errorText } from "../ui.tsx";

const ids = (list: readonly number[]) => list.map((id) => `#${id}`).join(", ");

/** Ready / blocked for backlog members with readiness, else the task state chip. */
export function NodeChip({ task }: { task: Pick<Task, "state" | "readiness"> }) {
  const s = nodeState(task);
  if (s === "ready" || s === "blocked")
    return (
      <Chip tone="idle" dot>
        {s}
      </Chip>
    );
  return <TaskStateBadge state={s} />;
}

/** Needs that keep a task blocked (not settled yet), and those among them that failed. */
export function pendingNeeds(task: Pick<Task, "needs">, byId: ReadonlyMap<number, Task>): { pending: number[]; failed: number[] } {
  const pending = task.needs.filter((id) => byId.get(id)?.state !== "settled");
  const failed = pending.filter((id) => {
    const s = byId.get(id)?.state;
    return s === "failed" || s === "interrupted" || s === "cancelled";
  });
  return { pending, failed };
}

/**
 * The node's one action: Answer (questions waiting; else Open in app while it needs input) > Start (ready; a blocked
 * task arms "Start before #N settles" first) > Retry (failed) > Open in app.
 */
export function NodeAction({ task, pending, className }: { task: Task; pending: readonly number[]; className?: string }) {
  const api = useApi();
  const invalidate = useInvalidateWork();
  const start = useMutation({ mutationFn: () => api.startTask(task.id), onSettled: invalidate });
  const [armed, setArmed] = useState(false);
  const cls = cx("relative z-[2]", className);
  const error = errorText(start.error);
  let action: ReactNode = null;
  if (task.state === "needs_input") {
    action =
      task.pendingQuestionCount > 0 ? (
        <AnswerButton to={`/tasks/${task.id}`} className={cls} />
      ) : task.t3DeepLink === null ? null : (
        <AppButton small variant="primary" href={task.t3DeepLink} className={cls}>
          Open in app
        </AppButton>
      );
  } else if (canStart(task) && task.readiness === "blocked") {
    const verb = `settle${pending.length === 1 ? "s" : ""}`;
    action = (
      <Button
        small
        variant={armed ? "primary" : "secondary"}
        busy={start.isPending}
        title={`Starts it now, before ${ids(pending)} ${verb}; its needs are kept`}
        onClick={() => (armed ? start.mutate() : setArmed(true))}
        onBlur={() => setArmed(false)}
        className={cls}
      >
        {armed ? `Start before ${ids(pending)} ${verb}` : "Start"}
      </Button>
    );
  } else if (canStart(task)) {
    action = (
      <Button small variant={task.state === "backlog" ? "primary" : "secondary"} busy={start.isPending} onClick={() => start.mutate()} className={cls}>
        {task.state === "backlog" ? (task.startAt === null ? "Start" : "Start now") : "Retry"}
      </Button>
    );
  } else if (task.t3DeepLink !== null) {
    action = (
      <AppButton small href={task.t3DeepLink} className={cls}>
        Open in app
      </AppButton>
    );
  }
  if (action === null && error === null) return null;
  return (
    <div className="flex min-w-0 flex-col items-start gap-1">
      {action}
      {error === null ? null : (
        <p className="relative z-[2] w-full truncate font-mono text-meta-sm text-bad" title={error}>
          {error}
        </p>
      )}
    </div>
  );
}

/**
 * Square card for one member: state chip and mono id, title (2 lines), mono project, a plain `manual` chip when it
 * starts by hand, one action. Needs input takes the yellow edge; not-started members are dashed. The whole card selects
 * the node on lg+ (side panel) and opens the task below lg.
 */
export function TreeNode({
  task,
  byId,
  selected,
  dimmed,
  onSelect,
  onHot,
  nodeRef,
}: {
  task: Task;
  byId: ReadonlyMap<number, Task>;
  selected: boolean;
  dimmed: boolean;
  onSelect: (id: number) => void;
  /** Hover / focus enters (id) or leaves (null). */
  onHot: (kind: "hover" | "focus", id: number | null) => void;
  nodeRef: Ref<HTMLLIElement>;
}) {
  const { pending, failed } = pendingNeeds(task, byId);
  const notStarted = task.state === "backlog";
  const manual = task.effectiveStartMode === "manual";
  return (
    <li
      ref={nodeRef}
      data-node={task.id}
      onMouseEnter={() => onHot("hover", task.id)}
      onMouseLeave={() => onHot("hover", null)}
      onFocus={() => onHot("focus", task.id)}
      onBlur={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget)) onHot("focus", null);
      }}
      className={cx(
        "relative z-[1] min-w-0 border bg-surface px-3 py-2.5 transition-[border-color,outline-color] duration-150 motion-reduce:transition-none md:w-[232px]",
        notStarted && "border-dashed",
        dimmed ? "border-line" : "border-line-strong",
        task.state === "needs_input" && "shadow-[inset_2px_0_var(--need)]",
        selected ? "outline outline-1 outline-offset-1 outline-fg" : "outline-transparent",
      )}
    >
      {/* Dimming fades the content, not the card, so connectors behind it stay hidden. */}
      <div className={cx("flex min-w-0 flex-col gap-2 transition-opacity duration-150 motion-reduce:transition-none", dimmed && "opacity-45")}>
        <div className="flex items-center justify-between gap-2">
          <NodeChip task={task} />
          <span className="font-mono text-meta text-muted tabular">#{task.id}</span>
        </div>
        <button
          type="button"
          aria-pressed={selected}
          onClick={() => onSelect(task.id)}
          className="line-clamp-2 text-left text-ui leading-snug font-medium break-words after:absolute after:inset-0 hover:underline max-lg:hidden"
        >
          {task.title}
        </button>
        <Link
          to={`/tasks/${task.id}`}
          className="line-clamp-2 text-ui leading-snug font-medium break-words after:absolute after:inset-0 hover:underline lg:hidden"
        >
          {task.title}
        </Link>
        <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 font-mono text-meta text-muted">
          <span className="min-w-0 truncate" title={task.projectTitle}>
            {task.projectTitle}
          </span>
          {manual ? <Chip className="text-meta-sm leading-4">manual</Chip> : null}
          {task.needs.length === 0 ? null : (
            <Chip className="text-meta-sm leading-4 tabular md:hidden">needs {task.needs.map((id) => `#${id}`).join(" ")}</Chip>
          )}
        </div>
        {notStarted && failed.length > 0 ? (
          <p className="font-mono text-meta text-bad tabular">blocked by failed {ids(failed)}</p>
        ) : null}
        <NodeAction task={task} pending={pending} />
      </div>
    </li>
  );
}
