import { useMutation } from "@tanstack/react-query";
import { type ReactNode } from "react";
import { useHarnesses, useInvalidateWork } from "../../api/queries.ts";
import type { Task } from "../../api/types.ts";
import { useApi } from "../../platform/context.tsx";
import { canStart, statusChips } from "../../lib/tasks.ts";
import { dateTime, duration } from "../../lib/format.ts";
import { inState } from "../../lib/trees.ts";
import { statusChipView } from "../StateBadge.tsx";
import { AppButton, Button, Chip, IdTag, InlineError, Meta, TextButton, TextLink, cx, errorText, needEdgeClass } from "../ui.tsx";
import { CompletionButton } from "../Completion.tsx";
import { isRetired } from "../../lib/harnesses.ts";
import { HarnessName } from "../HarnessName.tsx";
import { BlockedReason, useLaunchGate } from "../LaunchGate.tsx";
import { useSessionDestination } from "../../lib/hostState.ts";

const ids = (list: readonly number[]) => list.map((id) => `#${id}`).join(", ");

/** The task's status chips (see `statusChips`). `className` styles an open task's chip (a completed one ignores it). */
export function NodeChip({
  task,
  className,
}: {
  task: Pick<Task, "state" | "readiness" | "acceptanceId" | "connectionId" | "projectId" | "projectTitle">;
  className?: string | undefined;
}) {
  // A ready task its connection cannot launch reads backlog.
  const blocked = useLaunchGate()(task) !== null;
  const chips = statusChips(task, { blocked }).map((c) => {
    const { label, tone } = statusChipView(c);
    return (
      <Chip key={c} tone={tone} dot className={task.acceptanceId === null ? className : undefined}>
        {label}
      </Chip>
    );
  });
  return chips.length === 1 ? <>{chips}</> : <span className="inline-flex flex-wrap gap-1">{chips}</span>;
}

/** Needs that keep a task blocked (not accepted yet), and those among them that failed. */
export function pendingNeeds(task: Pick<Task, "needs">, byId: ReadonlyMap<number, Task>): { pending: number[]; failed: number[] } {
  const pending = task.needs.filter((id) => byId.get(id)?.acceptanceId == null);
  const failed = pending.filter((id) => {
    const s = byId.get(id)?.state;
    return s === "failed" || s === "interrupted" || s === "cancelled";
  });
  return { pending, failed };
}

/**
 * The node's one action: Open in T3 while it needs input > Review (a finished run to complete; off with
 * `review={false}` beside a State picker) > Start (ready; a blocked task cannot start) > Retry (failed) > Open in T3.
 * A done or archived tree offers no Start or Retry.
 */
export function NodeAction({ task, className, review = true }: { task: Task; pending: readonly number[]; className?: string; review?: boolean }) {
  const api = useApi();
  const { kindName } = useHarnesses();
  const invalidate = useInvalidateWork();
  const start = useMutation({ mutationFn: () => api.startTask(task.id), onSettled: invalidate });
  const cls = cx("relative z-[2]", className);
  const error = errorText(start.error);
  const startable = canStart(task) && (task.treeStatus === null || task.treeStatus === "open");
  const block = useLaunchGate()(task);
  const appLink = useSessionDestination()(task);
  let action: ReactNode = null;
  if (task.state === "needs_input") {
    action =
      appLink === null ? null : (
        <AppButton small variant="primary" href={appLink} className={cls}>
          Open in {kindName(task.harness)}
        </AppButton>
      );
  } else if (review && task.acceptanceId === null && task.state === "done") {
    action = <CompletionButton task={task} review />;
  } else if (startable) {
    action = (
      <BlockedReason reason={block?.reason} className={cls}>
        <Button
          small
          variant={task.state === "backlog" ? "primary" : "secondary"}
          busy={start.isPending}
          disabled={block !== null}
          onClick={() => start.mutate()}
          className={cls}
        >
          {task.state === "backlog" ? (task.startAt === null ? "Start" : "Start now") : "Retry"}
        </Button>
      </BlockedReason>
    );
  } else if (appLink !== null) {
    action = (
      <AppButton small href={appLink} className={cls}>
        Open in {kindName(task.harness)}
      </AppButton>
    );
  }
  if (action === null && error === null) return null;
  return (
    <div className="flex min-w-0 flex-col items-start gap-1">
      {action}
      {error === null ? null : (
        <InlineError truncate className="relative z-[2] w-full">
          {error}
        </InlineError>
      )}
    </div>
  );
}

/**
 * One member as a graph list row: level (first row of a level only; laptop), state chip, `#id` and title with project
 * beneath, needs (failed ones red), time in state, one action. Laptop rows sit on the list's subgrid;
 * phone rows wrap. Needs input takes the yellow edge; the selected row an inset Ink outline. The title
 * selects the row on lg+ (side panel) and opens the task below lg.
 */
export function TreeNode({
  task,
  byId,
  level,
  firstOfLevel,
  endsLevel,
  selected,
  onSelect,
}: {
  task: Task;
  byId: ReadonlyMap<number, Task>;
  /** 0-based level. */
  level: number;
  firstOfLevel: boolean;
  /** The next row starts another level (stronger rule under this one). */
  endsLevel: boolean;
  selected: boolean;
  onSelect: (id: number) => void;
}) {
  const { pending, failed } = pendingNeeds(task, byId);
  const blocked = useLaunchGate()(task) !== null;
  const when = inState(task, undefined, { blocked });
  const ran = task.state === "done" ? duration(task.runStartedAt, task.runEndedAt) : null;
  const titleCls = "truncate text-ui font-medium after:absolute after:inset-0";
  const label = `#${task.id} ${task.title} · Project: ${task.projectTitle}`;
  return (
    <li
      data-node={task.id}
      className={cx(
        "relative flex flex-wrap items-center gap-x-2 gap-y-1.5 border-b px-3 py-2 last:border-b-0 md:col-span-6 md:grid md:min-h-[52px] md:grid-cols-subgrid md:gap-0 md:px-0 md:py-1.5",
        endsLevel ? "border-line-strong" : "border-line",
        needEdgeClass(task.state === "needs_input"),
        selected && "outline outline-1 -outline-offset-2 outline-fg",
      )}
    >
      <Meta small className="tabular max-md:hidden md:pl-3">{firstOfLevel ? `L${level + 1}` : null}</Meta>
      <span className="order-3 md:order-none md:px-2">
        <NodeChip task={task} className={task.state === "backlog" ? "border-dashed" : undefined} />
      </span>
      <span className="order-1 flex min-w-0 basis-full flex-col gap-0.5 md:order-none md:basis-auto md:px-2">
        <span className="flex min-w-0 items-baseline gap-1.5">
          <IdTag id={task.id} />
          <TextButton aria-label={label} aria-pressed={selected} onClick={() => onSelect(task.id)} className={cx(titleCls, "max-lg:hidden")}>
            {task.title}
          </TextButton>
          <TextLink to={`/tasks/${task.id}`} aria-label={label} className={cx(titleCls, "min-w-0 text-left lg:hidden")}>
            {task.title}
          </TextLink>
        </span>
        <span className="flex min-w-0 flex-wrap items-center gap-x-1.5 font-mono text-meta text-fg [overflow-wrap:anywhere]">
          {task.projectTitle}
          {isRetired(task) ? <HarnessName task={task} className="text-muted" /> : null}
        </span>
      </span>
      <Meta className="order-4 tabular md:order-none md:truncate md:px-2" {...(failed.length > 0 ? { title: `blocked by failed ${ids(failed)}` } : {})}>
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
      </Meta>
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
