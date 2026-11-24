import { useMutation } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { Link } from "react-router";
import { useApi } from "../platform/context.tsx";
import { useHarnesses, useInvalidateWork } from "../api/queries.ts";
import type { Task } from "../api/types.ts";
import { configModelText, dateTime, duration, relativeTime } from "../lib/format.ts";
import type { SettledItem } from "../lib/settled.ts";
import { canStart, isWaiting, waitTarget } from "../lib/tasks.ts";
import { HarnessName } from "./HarnessName.tsx";
import { taskClosedLabel, treeTaskPath } from "../lib/trees.ts";
import { browserTimeZone, wallTime } from "../lib/schedules.ts";
import { IconChainLink, IconClock, IconRepeat, IconTree } from "./icons.tsx";
import { CompletionButton } from "./Completion.tsx";
import { useCompletionLinger, useQuickComplete, useUndoComplete } from "./QuickComplete.tsx";
import { LastWordLine } from "./LastWord.tsx";
import { autoCompletedMeta } from "../lib/completion.ts";
import { BlockedReason, useLaunchGate } from "./LaunchGate.tsx";
import { NodeChip } from "./trees/TreeNode.tsx";
import { AppButton, Button, Chip, EmptyState, ErrorText, IdTag, InlineError, Meta, RelTime, Row, RowList, SkeletonBar, TextLink, cx, needEdgeClass } from "./ui.tsx";
import { useSessionDestination } from "../lib/hostState.ts";
import { RowCopyLink } from "./StageLinks.tsx";
import { TreeRow } from "./trees/TreeRow.tsx";

/** Standalone bordered task list. Inside a `Panel flush`, use `TaskRows` instead. */
export function TaskList({ tasks, emptyText = "No tasks." }: { tasks: Task[]; emptyText?: string }) {
  if (tasks.length === 0) return <EmptyState>{emptyText}</EmptyState>;
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
  /** Tree members name their tree in the meta line, linking it with the task selected. */
  showTree?: boolean;
}) {
  if (tasks.length === 0) return <EmptyState inset>{emptyText}</EmptyState>;
  return (
    <RowList>
      {tasks.map((t) => (
        <Row key={t.id} bare>
          <TaskRow task={t} showProject={showProject} showTree={showTree} />
        </Row>
      ))}
    </RowList>
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
    <TextLink to={`/projects/${projectId}`} title={title} className={cls}>
      {title}
    </TextLink>
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

/** "completed 3h ago" (exact time on hover), "via T3 settle" when inferred, then "ran 35m" when the run span is known. */
function SettledMeta({
  settledAt,
  via,
  runStartedAt,
  runEndedAt,
}: {
  settledAt: string;
  via: string | null;
  runStartedAt: string | null;
  runEndedAt: string | null;
}) {
  const ran = duration(runStartedAt, runEndedAt);
  return (
    <>
      <span title={`Completed ${dateTime(settledAt)}`} className="tabular">
        completed {relativeTime(settledAt)}
        {via === null ? null : ` ${via}`}
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
 * while timed, "needs #3 #5" while blocked on its needs, else the schedule that spawned it, else the agent session that
 * created it. Viewed from a session (`contextThreadId`), its own provenance chip is dropped.
 */
function rowChip(task: Task, contextThreadId?: string): RowChip | null {
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
      title: `Created by session ${task.originSessionTitle ?? task.originSession.externalSessionId}`,
      icon: <IconChainLink />,
    };
  return null;
}

/**
 * Waiting tasks get no Start here (Start now, which unlinks, lives on task detail). `contextThreadId`: the session
 * page showing the row (relative run-order chip copy).
 */
/** Task row grid (laptop columns: chip, project, title, actions, copy), shared with its skeleton. */
function taskRowGrid(showProject: boolean): string {
  return cx(
    "grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-2.5 gap-y-1.5 px-3 py-row md:gap-x-3.5",
    showProject ? "md:grid-cols-[118px_150px_minmax(0,1fr)_auto_auto]" : "md:grid-cols-[118px_minmax(0,1fr)_auto_auto]",
  );
}

const TITLE_WIDTHS = ["w-3/5", "w-2/5", "w-1/2", "w-2/3", "w-1/3"];

/** Loading rows in the task row grid: chip, project, title over meta and last word, an action. */
export function TaskRowsSkeleton({ count = 3, showProject = false }: { count?: number; showProject?: boolean }) {
  return (
    <RowList>
      {Array.from({ length: count }, (_, i) => (
        <Row key={i} bare>
          <div className={cx(taskRowGrid(showProject), needEdgeClass(false))}>
            <div className="order-2 justify-self-end md:order-1 md:justify-self-start">
              <SkeletonBar className="h-[22px] w-20" />
            </div>
            {showProject ? (
              <div className="hidden md:order-2 md:block">
                <SkeletonBar className="h-3 w-16" />
              </div>
            ) : null}
            <div className="order-1 flex min-w-0 flex-col gap-1.5 py-0.5 md:order-2">
              {showProject ? <SkeletonBar className="h-3 w-16 md:hidden" /> : null}
              <SkeletonBar className={cx("h-4", TITLE_WIDTHS[i % TITLE_WIDTHS.length])} />
              <SkeletonBar className="h-3 w-56 max-w-full" />
              <SkeletonBar className="hidden h-3 w-4/5 md:block" />
            </div>
            <div className="order-4 col-span-2 flex md:col-span-1 md:justify-end">
              <SkeletonBar className="h-control-sm w-24" />
            </div>
          </div>
        </Row>
      ))}
    </RowList>
  );
}

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
  const block = useLaunchGate()(task);
  const chip = rowChip(task, contextThreadId);
  // A closed tree's row chip already links it.
  const treeLink = showTree && task.treeId !== null && taskClosedLabel(task) === null ? task.treeId : null;
  const needsInput = task.state === "needs_input";
  const appLink = useSessionDestination()(task);
  const { kindName, descriptor } = useHarnesses();
  // Completed here a moment ago: the row stays in place with a completed chip and Undo (Dashboard linger).
  const justCompleted = useCompletionLinger().lingering.has(task.id);
  const autoMeta = autoCompletedMeta(task.completionProvenance, descriptor(task.harness));
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
      className={cx("group/row", taskRowGrid(showProject), needEdgeClass(needsInput))}
    >
      <div className="order-2 justify-self-end md:order-1 md:justify-self-start">
        <NodeChip task={task} />
      </div>

      {showProject ? <ProjectCell projectId={task.projectId} title={task.projectTitle} /> : null}

      <div className="order-1 min-w-0 md:order-2">
        {showProject ? (
          <ProjectName projectId={task.projectId} title={task.projectTitle} className="mb-0.5 block break-words md:hidden" />
        ) : null}
        <IdTag id={task.id} title={`Task #${task.id}`} className="mr-1.5" />
        <TextLink to={`/tasks/${task.id}`} className="text-body font-medium break-words">
          {task.title}
        </TextLink>
        <div className="mt-0.5 flex flex-wrap gap-x-2.5 gap-y-0.5 font-mono text-meta text-muted">
          {showProject ? null : (
            <TextLink to={`/projects/${task.projectId}`} variant="muted">
              {task.projectTitle}
            </TextLink>
          )}
          <span className="inline-flex flex-wrap items-center gap-x-1"><HarnessName task={task} /> · {configModelText(task.executionConfig)}</span>
          {task.completedAt !== null ? (
            <SettledMeta settledAt={task.completedAt} via={autoMeta} runStartedAt={task.runStartedAt} runEndedAt={task.runEndedAt} />
          ) : (
            <RelTime at={since} className="tabular" />
          )}
          {treeLink === null ? null : (
            <TextLink
              to={treeTaskPath(treeLink, task.id)}
              variant="rest"
              title={`In tree: ${task.treeTitle ?? treeLink}. Open it with this task selected`}
              className="inline-flex min-w-0 items-center gap-1 text-fg"
            >
              <IconTree />
              <span className="max-w-[26ch] truncate">{task.treeTitle ?? `tree ${treeLink}`}</span>
            </TextLink>
          )}
          {chip === null ? null : (
            <TextLink to={chip.to} variant="muted" title={chip.title} className="inline-flex items-center gap-1 tabular md:hidden">
              {chip.icon}
              <span className="max-w-[24ch] truncate">{chip.label}</span>
            </TextLink>
          )}
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
            <Meta className="tabular">{confirming.starts}</Meta>
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
          <BlockedReason reason={block?.reason}>
            <Button small variant="primary" busy={start.isPending} disabled={block !== null} onClick={() => start.mutate()}>
              {task.state !== "backlog" ? "Restart" : task.startAt !== null ? "Start now" : "Start"}
            </Button>
          </BlockedReason>
        ) : null}
        {appLink === null || justCompleted || confirming !== null ? null : (
          <AppButton small variant={needsInput ? "primary" : "secondary"} href={appLink}>
            Open in {kindName(task.harness)}
          </AppButton>
        )}
        {extra}
        <RowCopyLink subject={{ kind: "task", id: task.id, title: task.title }} />
      </div>

      {task.lastError === null ? null : (
        <InlineError truncate className={cx("order-5 col-span-2", underTitle)}>
          {task.lastError}
        </InlineError>
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
  if (items.length === 0) return <EmptyState inset>{emptyText}</EmptyState>;
  return (
    <RowList>
      {items.map((item) => (
        <Row key={item.kind === "task" ? `task-${item.task.id}` : `tree-${item.tree.id}`} bare>
          {item.kind === "task" ? (
            <TaskRow task={item.task} showProject={showProject} />
          ) : (
            <TreeRow tree={item.tree} />
          )}
        </Row>
      ))}
    </RowList>
  );
}
