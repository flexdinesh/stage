import { useMutation } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { Link } from "react-router";
import { useApi } from "../platform/context.tsx";
import { useInvalidateWork } from "../api/queries.ts";
import type { Task } from "../api/types.ts";
import { dateTime, duration, modelText, relativeTime } from "../lib/format.ts";
import type { SettledItem } from "../lib/settled.ts";
import { canStart, isWaiting, waitTarget } from "../lib/tasks.ts";
import { HARNESS_NAME } from "../lib/harnesses.ts";
import { HarnessName } from "./HarnessName.tsx";
import { taskClosedLabel } from "../lib/trees.ts";
import { browserTimeZone, wallTime } from "../lib/schedules.ts";
import { IconChainLink, IconClock, IconRepeat, IconTree } from "./icons.tsx";
import { CompletionButton } from "./Completion.tsx";
import { useCompletionLinger, useQuickComplete, useUndoComplete } from "./QuickComplete.tsx";
import { LastWordLine } from "./LastWord.tsx";
import { autoCompletedMeta } from "../lib/completion.ts";
import { NodeChip } from "./trees/TreeNode.tsx";
import { AppButton, Button, Chip, ErrorText, Muted, cx } from "./ui.tsx";
import { useSessionDestination } from "../lib/hostState.ts";
import { RowCopyLink } from "./StageLinks.tsx";
import { TreeRow } from "./trees/TreeRow.tsx";

/** Standalone bordered task list. Inside a `Panel flush`, use `TaskRows` instead. */
export function TaskList({ tasks, emptyText = "No tasks." }: { tasks: Task[]; emptyText?: string }) {
  if (tasks.length === 0) return <Muted>{emptyText}</Muted>;
  return (
    <div className="border border-line bg-surface">
      <TaskRows tasks={tasks} emptyText={emptyText} />
    </div>
  );
}

/**
 * Hairline-separated rows with no outer border (for `Panel flush`). Empty renders a padded hint line.
 * `showProject` gives the project its own column (laptop) or line above the title (phone).
 */
export function TaskRows({
  tasks,
  emptyText = "No tasks.",
  showProject = false,
  showTree = false,
}: {
  tasks: Task[];
  emptyText?: string;
  showProject?: boolean;
  /** Tree members carry a chip naming their tree (Dashboard breakouts). */
  showTree?: boolean;
}) {
  if (tasks.length === 0) return <p className="px-3 py-2.5 text-small text-muted">{emptyText}</p>;
  return (
    <ul>
      {tasks.map((t) => (
        <li key={t.id} className="border-b border-line last:border-b-0">
          <TaskRow task={t} showProject={showProject} showTree={showTree} />
        </li>
      ))}
    </ul>
  );
}

/** Project name in Ink Text; links to the project when it is mirrored. Full name in `title` for truncation. */
function ProjectName({ projectId, title, className }: { projectId: number | null; title: string; className: string }) {
  const cls = cx("font-mono text-detail font-medium text-fg", className);
  if (projectId === null)
    return (
      <span title={title} className={cls}>
        {title}
      </span>
    );
  return (
    <Link to={`/projects/${projectId}`} title={title} className={cx(cls, "hover:underline")}>
      {title}
    </Link>
  );
}

/** Laptop-only project column cell; sits between the state chip and the title. */
function ProjectCell({ projectId, title }: { projectId: number | null; title: string }) {
  return (
    <div className="hidden min-w-0 md:order-2 md:block">
      <ProjectName projectId={projectId} title={title} className="block truncate" />
    </div>
  );
}

/** "settled 3h ago" (exact time on hover), then "ran 35m" when the run span is known. */
function SettledMeta({
  settledAt,
  runStartedAt,
  runEndedAt,
}: {
  settledAt: string;
  runStartedAt: string | null;
  runEndedAt: string | null;
}) {
  const ran = duration(runStartedAt, runEndedAt);
  return (
    <>
      <span title={`Completed ${dateTime(settledAt)}`} className="tabular">
        completed {relativeTime(settledAt)}
      </span>
      {ran === null ? null : (
        <span title={`Ran ${dateTime(runStartedAt)} to ${dateTime(runEndedAt)}`} className="tabular">
          ran {ran}
        </span>
      )}
    </>
  );
}

interface RowChip {
  to: string;
  label: string;
  title: string;
  icon: ReactNode;
}

/**
 * The row's one chip: closed rows lead with "archived" / "tree done" / "tree archived"; then the one-shot start time
 * while timed, its tree when `showTree` (Dashboard breakouts), "needs #3 #5" while blocked on its needs, else the
 * schedule that spawned it, else the agent session that created it. Viewed from a session (`contextThreadId`), its own
 * provenance chip is dropped.
 */
function rowChip(task: Task, contextThreadId?: string, showTree = false): RowChip | null {
  const closed = taskClosedLabel(task);
  if (closed !== null)
    return task.archivedAt !== null || task.treeId === null
      ? { to: `/tasks/${task.id}`, label: closed, title: `Archived ${dateTime(task.archivedAt ?? "")}`, icon: null }
      : { to: `/trees/${task.treeId}`, label: closed, title: `In ${closed}: ${task.treeTitle ?? task.treeId}`, icon: <IconTree /> };
  if (task.state === "backlog" && task.startAt !== null) {
    const tz = browserTimeZone();
    const when = wallTime(task.startAt, tz, { short: true });
    return {
      to: `/tasks/${task.id}`,
      label: when.length <= 5 ? `at ${when}` : when,
      title: `Starts ${wallTime(task.startAt, task.startTz ?? tz, { zone: true })}`,
      icon: <IconClock />,
    };
  }
  if (showTree && task.treeId !== null)
    return {
      to: `/trees/${task.treeId}`,
      label: task.treeTitle ?? `tree ${task.treeId}`,
      title: `In tree: ${task.treeTitle ?? task.treeId}`,
      icon: <IconTree />,
    };
  const [firstNeed] = task.needs;
  if (isWaiting(task) && firstNeed !== undefined)
    return {
      to: `/tasks/${firstNeed}`,
      label: `needs ${task.needs.map((id) => `#${id}`).join(" ")}`,
      title: `Ready after user completion of ${waitTarget(task)}`,
      icon: <IconChainLink />,
    };
  if (task.scheduleId !== null)
    return {
      to: `/schedules/${task.scheduleId}`,
      label: "schedule",
      title: `Started by schedule: ${task.scheduleTitle ?? `#${task.scheduleId}`}`,
      icon: <IconRepeat />,
    };
  if (task.originSession && task.originSession.externalSessionId !== contextThreadId)
    return {
      to: `/tasks/${task.id}?batch=1`,
      label: "by agent",
      title: `Created by session ${task.originThreadTitle ?? task.originSession.externalSessionId}`,
      icon: <IconChainLink />,
    };
  return null;
}

/**
 * Waiting tasks get no Start here (Start now, which unlinks, lives on task detail). `contextThreadId`: the session
 * page showing the row (relative run-order chip copy).
 */
export function TaskRow({
  task,
  extra,
  showProject = false,
  showTree = false,
  contextThreadId,
  hideStart = false,
}: {
  task: Task;
  extra?: ReactNode;
  showProject?: boolean;
  showTree?: boolean;
  contextThreadId?: string;
  /** The Start lives elsewhere (a chain head's Start chain). */
  hideStart?: boolean;
}) {
  const api = useApi();
  const invalidate = useInvalidateWork();
  const start = useMutation({ mutationFn: () => api.startTask(task.id), onSettled: invalidate });
  const since = task.launchedAt ?? task.createdAt;
  const showStart = !hideStart && canStart(task) && !isWaiting(task);
  const chip = rowChip(task, contextThreadId, showTree);
  const needsInput = task.state === "needs_input";
  const appLink = useSessionDestination()(task);
  // Completed here a moment ago: the row stays in place with a completed chip and Undo (Dashboard linger).
  const justCompleted = useCompletionLinger().lingering.has(task.id);
  const autoMeta = autoCompletedMeta(task.completionProvenance);
  const review = !justCompleted && task.acceptanceId === null && (task.state === "done" || task.state === "settled");
  const quick = useQuickComplete(task);
  const undo = useUndoComplete(task);
  const confirming = review ? quick.confirm : null;
  const rowError = review ? quick.error : justCompleted ? undo.error : null;
  const hasActions = justCompleted || review || showStart || appLink !== null || extra !== undefined;
  // Full-width lines under the title start at the title column.
  const underTitle = showProject ? "md:col-span-3 md:col-start-3" : "md:col-span-3 md:col-start-2";

  return (
    <div
      className={cx(
        "group/row grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-2.5 gap-y-1.5 border-l-2 px-3 py-2.5 md:gap-x-3.5 md:py-2",
        showProject
          ? "md:grid-cols-[118px_150px_minmax(0,1fr)_auto_auto]"
          : "md:grid-cols-[118px_minmax(0,1fr)_auto_auto]",
        needsInput ? "border-l-need" : "border-l-transparent",
      )}
    >
      <div className="order-2 justify-self-end md:order-1 md:justify-self-start">
        <NodeChip task={task} />
      </div>

      {showProject ? <ProjectCell projectId={task.projectId} title={task.projectTitle} /> : null}

      <div className="order-1 min-w-0 md:order-2">
        {showProject ? (
          <ProjectName projectId={task.projectId} title={task.projectTitle} className="mb-0.5 block break-words md:hidden" />
        ) : null}
        <span title={`Task #${task.id}`} className="mr-1.5 font-mono text-meta whitespace-nowrap text-muted tabular">
          #{task.id}
        </span>
        <Link to={`/tasks/${task.id}`} className="text-body font-medium break-words hover:underline">
          {task.title}
        </Link>
        <div className="mt-0.5 flex flex-wrap gap-x-2.5 gap-y-0.5 font-mono text-meta text-muted">
          {showProject ? null : (
            <Link to={`/projects/${task.projectId}`} className="hover:text-fg hover:underline">
              {task.projectTitle}
            </Link>
          )}
          <span className="inline-flex flex-wrap items-center gap-x-1"><HarnessName task={task} /> · {task.harness === "t3" ? modelText(task.model, task.modelOptions) : task.executionConfig?.model || "Default model"}</span>
          {task.completedAt !== null ? (
            <>
              <SettledMeta settledAt={task.completedAt} runStartedAt={task.runStartedAt} runEndedAt={task.runEndedAt} />
              {autoMeta === null ? null : <span>{autoMeta}</span>}
            </>
          ) : (
            <span title={dateTime(since)} className="tabular">
              {relativeTime(since)}
            </span>
          )}
          {chip === null ? null : (
            <Link
              to={chip.to}
              title={chip.title}
              className="inline-flex items-center gap-1 tabular hover:text-fg hover:underline md:hidden"
            >
              {chip.icon}
              <span className="max-w-[24ch] truncate">{chip.label}</span>
            </Link>
          )}
          {task.tags.map((tag) => (
            <span key={tag}>#{tag}</span>
          ))}
        </div>
        <LastWordLine state={task.state} preview={task.lastWordPreview} className="mt-1 hidden md:block" />
      </div>

      <LastWordLine state={task.state} preview={task.lastWordPreview} className="order-3 col-span-2 md:hidden" />

      <div className="hidden md:order-3 md:block">
        {chip === null ? null : (
          <Link to={chip.to} title={chip.title} className="hover:opacity-80">
            <Chip tone="plain" className="max-w-[22ch] tabular">
              {chip.icon}
              <span className="min-w-0 truncate">{chip.label}</span>
            </Chip>
          </Link>
        )}
      </div>

      <div className={cx("order-4 col-span-2 flex-wrap items-center gap-1.5 md:col-span-1 md:flex md:justify-end", hasActions ? "flex" : "hidden")}>
        {justCompleted ? (
          <>
            <Chip tone="ok">completed</Chip>
            <Button small variant="ghost" busy={undo.isPending} onClick={() => undo.mutate()}>
              Undo
            </Button>
          </>
        ) : confirming !== null ? (
          <>
            <span className="font-mono text-meta text-muted tabular">{confirming.starts}</span>
            <Button small variant="primary" busy={quick.busy} onClick={confirming.submit}>
              Complete
            </Button>
            <Button small variant="ghost" disabled={quick.busy} onClick={confirming.cancel}>
              Cancel
            </Button>
          </>
        ) : review ? (
          <>
            <Button small variant="primary" busy={quick.busy} onClick={quick.start}>
              Complete
            </Button>
            <CompletionButton task={task} review />
          </>
        ) : null}
        {showStart ? (
          <Button small variant="primary" busy={start.isPending} onClick={() => start.mutate()}>
            {task.state !== "backlog" ? "Restart" : task.startAt !== null ? "Start now" : "Start"}
          </Button>
        ) : null}
        {appLink === null || justCompleted || confirming !== null ? null : (
          <AppButton small variant={needsInput ? "primary" : "secondary"} href={appLink}>
            Open in {HARNESS_NAME[task.harness]}
          </AppButton>
        )}
        {extra}
        <RowCopyLink subject={{ kind: "task", id: task.id, title: task.title }} />
      </div>

      {task.lastError === null ? null : (
        <p className={cx("order-5 col-span-2 truncate font-mono text-meta text-bad", underTitle)} title={task.lastError}>
          {task.lastError}
        </p>
      )}
      {start.error === null ? null : (
        <div className={cx("order-6 col-span-2", underTitle)}>
          <ErrorText error={start.error} />
        </div>
      )}
      {rowError === null ? null : (
        <div className={cx("order-6 col-span-2", underTitle)}>
          <ErrorText error={rowError} />
        </div>
      )}
    </div>
  );
}

/** Completed group: tasks and trees in one list (already ordered by settle time). */
export function SettledRows({ items, emptyText, showProject = true }: { items: SettledItem[]; emptyText: string; showProject?: boolean }) {
  if (items.length === 0) return <p className="px-3 py-2.5 text-small text-muted">{emptyText}</p>;
  return (
    <ul>
      {items.map((item) => (
        <li
          key={item.kind === "task" ? `task-${item.task.id}` : `tree-${item.tree.id}`}
          className="border-b border-line last:border-b-0"
        >
          {item.kind === "task" ? (
            <TaskRow task={item.task} showProject={showProject} />
          ) : (
            <TreeRow tree={item.tree} />
          )}
        </li>
      ))}
    </ul>
  );
}
