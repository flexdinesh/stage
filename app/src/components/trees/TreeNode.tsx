import { useMutation } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import { Link } from "react-router";
import { useInvalidateWork } from "../../api/queries.ts";
import type { Task } from "../../api/types.ts";
import { useApi } from "../../platform/context.tsx";
import { canStart } from "../../lib/tasks.ts";
import { dateTime, duration } from "../../lib/format.ts";
import { inState, nodeState } from "../../lib/trees.ts";
import { AnswerButton } from "../NeedsInputPanel.tsx";
import { TaskStateBadge } from "../StateBadge.tsx";
import { AppButton, Button, Chip, cx, errorText } from "../ui.tsx";

const ids = (list: readonly number[]) => list.map((id) => `#${id}`).join(", ");

/** Ready / blocked for backlog members with readiness, else the task state chip. */
export function NodeChip({ task, className }: { task: Pick<Task, "state" | "readiness">; className?: string | undefined }) {
  const s = nodeState(task);
  if (s === "ready" || s === "blocked")
    return (
      <Chip tone="idle" dot className={className}>
        {s}
      </Chip>
    );
  return <TaskStateBadge state={s} className={className} />;
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
 * One member as a graph list row: level (first row of a level only; laptop), state chip, `#id` and title (project when the
 * tree spans projects), needs (failed ones red), time in state, one action. Laptop rows sit on the list's subgrid;
 * phone rows wrap to two lines. Needs input takes the yellow edge; the selected row an inset Ink outline. The title
 * selects the row on lg+ (side panel) and opens the task below lg.
 */
export function TreeNode({
  task,
  byId,
  level,
  firstOfLevel,
  endsLevel,
  showProject,
  selected,
  dimmed,
  onSelect,
  onHot,
}: {
  task: Task;
  byId: ReadonlyMap<number, Task>;
  /** 0-based level. */
  level: number;
  firstOfLevel: boolean;
  /** The next row starts another level (stronger rule under this one). */
  endsLevel: boolean;
  showProject: boolean;
  selected: boolean;
  dimmed: boolean;
  onSelect: (id: number) => void;
  /** Hover / focus enters (id) or leaves (null). */
  onHot: (kind: "hover" | "focus", id: number | null) => void;
}) {
  const { pending, failed } = pendingNeeds(task, byId);
  const when = inState(task);
  const ran = task.state === "done" || task.state === "settled" ? duration(task.runStartedAt, task.runEndedAt) : null;
  const titleCls = "min-w-0 truncate text-left text-ui font-medium after:absolute after:inset-0 hover:underline";
  return (
    <li
      data-node={task.id}
      onMouseEnter={() => onHot("hover", task.id)}
      onMouseLeave={() => onHot("hover", null)}
      onFocus={() => onHot("focus", task.id)}
      onBlur={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget)) onHot("focus", null);
      }}
      className={cx(
        "relative flex flex-wrap items-center gap-x-2 gap-y-1.5 border-b px-3 py-2 last:border-b-0 md:col-span-6 md:grid md:min-h-[34px] md:grid-cols-subgrid md:gap-0 md:px-0 md:py-0",
        "transition-opacity duration-150 motion-reduce:transition-none",
        endsLevel ? "border-line-strong" : "border-line",
        task.state === "needs_input" && "shadow-[inset_2px_0_var(--need)]",
        selected && "outline outline-1 -outline-offset-2 outline-fg",
        dimmed && "opacity-45",
      )}
    >
      <span className="font-mono text-meta-sm text-muted tabular max-md:hidden md:pl-3">{firstOfLevel ? `L${level + 1}` : null}</span>
      <span className="order-3 md:order-none md:px-2">
        <NodeChip task={task} className={task.state === "backlog" ? "border-dashed" : undefined} />
      </span>
      <span className="order-1 flex min-w-0 basis-full items-baseline gap-1.5 md:order-none md:basis-auto md:px-2">
        <span className="shrink-0 font-mono text-meta text-muted tabular">#{task.id}</span>
        <button type="button" aria-pressed={selected} onClick={() => onSelect(task.id)} className={cx(titleCls, "max-lg:hidden")}>
          {task.title}
        </button>
        <Link to={`/tasks/${task.id}`} className={cx(titleCls, "lg:hidden")}>
          {task.title}
        </Link>
        {showProject ? (
          <span className="shrink-0 truncate font-mono text-meta text-muted max-md:hidden" title={task.projectTitle}>
            {task.projectTitle}
          </span>
        ) : null}
      </span>
      <span
        className="order-4 font-mono text-meta text-muted tabular md:order-none md:truncate md:px-2"
        title={failed.length > 0 ? `blocked by failed ${ids(failed)}` : undefined}
      >
        {task.needs.length === 0 ? (
          <span className="max-md:hidden">root</span>
        ) : (
          <>
            <span className="md:hidden">needs </span>
            {task.needs.map((id, i) => (
              <span key={id} className={cx(failed.includes(id) && "text-bad")}>
                {i === 0 ? "" : " "}#{id}
              </span>
            ))}
          </>
        )}
      </span>
      <span
        className={cx(
          "order-5 font-mono text-meta tabular md:order-none md:px-2 md:text-right",
          task.state === "needs_input" ? "text-need-fg" : "text-fg",
        )}
        title={`${dateTime(when.at)}${ran === null ? "" : ` · ran ${ran}`}`}
      >
        {when.text}
      </span>
      <span className="order-6 ml-auto flex justify-end md:order-none md:ml-0 md:pr-3 md:pl-2">
        <NodeAction task={task} pending={pending} />
      </span>
    </li>
  );
}
