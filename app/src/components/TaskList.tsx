import { useMutation } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { Link } from "react-router";
import { useApi } from "../platform/context.tsx";
import { useInvalidateWork } from "../api/queries.ts";
import type { Task, Thread } from "../api/types.ts";
import { dateTime, duration, modelText, relativeTime } from "../lib/format.ts";
import type { SettledItem } from "../lib/settled.ts";
import { sessionPath } from "../lib/sessions.ts";
import { canStart, isWaiting, waitTarget } from "../lib/tasks.ts";
import { browserTimeZone, wallTime } from "../lib/schedules.ts";
import { IconChainLink, IconClock, IconRepeat } from "./icons.tsx";
import { LastWordLine } from "./LastWord.tsx";
import { PrChips } from "./PullRequests.tsx";
import { TaskStateBadge } from "./StateBadge.tsx";
import { AnswerButton } from "./NeedsInputPanel.tsx";
import { AppButton, Button, Chip, ErrorText, Muted, cx } from "./ui.tsx";
import { RowCopyLink } from "./StageLinks.tsx";

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
}: {
  tasks: Task[];
  emptyText?: string;
  showProject?: boolean;
}) {
  if (tasks.length === 0) return <p className="px-3 py-2.5 text-small text-muted">{emptyText}</p>;
  return (
    <ul>
      {tasks.map((t) => (
        <li key={t.id} className="border-b border-line last:border-b-0">
          <TaskRow task={t} showProject={showProject} />
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
      <span title={`Settled ${dateTime(settledAt)}`} className="tabular">
        settled {relativeTime(settledAt)}
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
 * The row's one chip: the one-shot start time while timed, "needs #3 #5" while blocked on its needs, else the
 * schedule that spawned it, else the agent session that created it. Viewed from a session (`contextThreadId`), its
 * own provenance chip is dropped.
 */
function rowChip(task: Task, contextThreadId?: string): RowChip | null {
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
      title: `Starts when ${waitTarget(task)} settle${task.needs.length === 1 ? "s" : ""}`,
      icon: <IconChainLink />,
    };
  if (task.scheduleId !== null)
    return {
      to: `/schedules/${task.scheduleId}`,
      label: "schedule",
      title: `Started by schedule: ${task.scheduleTitle ?? `#${task.scheduleId}`}`,
      icon: <IconRepeat />,
    };
  if (task.originThreadId !== null && task.originThreadId !== contextThreadId)
    return {
      to: sessionPath(task.originThreadId),
      label: "by agent",
      title: `Created by session ${task.originThreadTitle ?? task.originThreadId}`,
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
  contextThreadId,
  hideStart = false,
}: {
  task: Task;
  extra?: ReactNode;
  showProject?: boolean;
  contextThreadId?: string;
  /** The Start lives elsewhere (a chain head's Start chain). */
  hideStart?: boolean;
}) {
  const api = useApi();
  const invalidate = useInvalidateWork();
  const start = useMutation({ mutationFn: () => api.startTask(task.id), onSettled: invalidate });
  const since = task.launchedAt ?? task.createdAt;
  const showStart = !hideStart && canStart(task) && !isWaiting(task);
  const chip = rowChip(task, contextThreadId);
  const needsInput = task.state === "needs_input";
  const answerable = needsInput && task.pendingQuestionCount > 0;
  const hasActions = showStart || answerable || task.t3DeepLink !== null || extra !== undefined;
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
        <TaskStateBadge state={task.state} />
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
          <span>{modelText(task.model, task.modelOptions)}</span>
          {task.state === "settled" && task.settledAt !== null ? (
            <SettledMeta settledAt={task.settledAt} runStartedAt={task.runStartedAt} runEndedAt={task.runEndedAt} />
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
              {chip.label}
            </Link>
          )}
          {task.tags.map((tag) => (
            <span key={tag}>#{tag}</span>
          ))}
          <PrChips prs={task.pullRequests} detailTo={`/tasks/${task.id}`} />
        </div>
        <LastWordLine state={task.state} preview={task.lastWordPreview} className="mt-1 hidden md:block" />
      </div>

      <LastWordLine state={task.state} preview={task.lastWordPreview} className="order-3 col-span-2 md:hidden" />

      <div className="hidden md:order-3 md:block">
        {chip === null ? null : (
          <Link to={chip.to} title={chip.title} className="hover:opacity-80">
            <Chip tone="plain" className="tabular">
              {chip.icon}
              {chip.label}
            </Chip>
          </Link>
        )}
      </div>

      <div className={cx("order-4 col-span-2 flex-wrap items-center gap-1.5 md:col-span-1 md:flex md:justify-end", hasActions ? "flex" : "hidden")}>
        {showStart ? (
          <Button small variant="primary" busy={start.isPending} onClick={() => start.mutate()}>
            {task.state !== "backlog" ? "Restart" : task.startAt !== null ? "Start now" : "Start"}
          </Button>
        ) : null}
        {answerable ? <AnswerButton to={`/tasks/${task.id}`} /> : null}
        {task.t3DeepLink === null ? null : (
          <AppButton small variant={needsInput && !answerable ? "primary" : "secondary"} href={task.t3DeepLink}>
            Open in app
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
    </div>
  );
}

/** Observed state of an external thread the Dashboard lists. */
export type ExternalThreadState = "needs_input" | "running" | "settled";

/**
 * External (non-task) threads needing input, running or settled, styled as TaskRows in that state with the project
 * column (Dashboard only). Title opens History filtered to it.
 */
export function ExternalThreadRows({ threads, state }: { threads: Thread[]; state: ExternalThreadState }) {
  return (
    <ul>
      {threads.map((t) => (
        <li key={t.t3ThreadId} className="border-b border-line last:border-b-0">
          <ExternalThreadRow thread={t} state={state} />
        </li>
      ))}
    </ul>
  );
}

/** Dashboard Settled group: tasks and external threads in one list (already ordered by settle time). */
export function SettledRows({ items, emptyText }: { items: SettledItem[]; emptyText: string }) {
  if (items.length === 0) return <p className="px-3 py-2.5 text-small text-muted">{emptyText}</p>;
  return (
    <ul>
      {items.map((item) => (
        <li
          key={item.kind === "task" ? `task-${item.task.id}` : `thread-${item.thread.t3ThreadId}`}
          className="border-b border-line last:border-b-0"
        >
          {item.kind === "task" ? (
            <TaskRow task={item.task} showProject />
          ) : (
            <ExternalThreadRow thread={item.thread} state="settled" />
          )}
        </li>
      ))}
    </ul>
  );
}

function ExternalThreadRow({ thread, state }: { thread: Thread; state: ExternalThreadState }) {
  const href = thread.t3DeepLink;
  const project = thread.projectTitle ?? thread.t3ProjectId;
  const needsInput = state === "needs_input";
  const answerable = needsInput && thread.pendingQuestionCount > 0;
  return (
    <div
      className={cx(
        "group/row grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-2.5 gap-y-1.5 border-l-2 px-3 py-2.5 md:grid-cols-[118px_150px_minmax(0,1fr)_auto] md:gap-x-3.5 md:py-2",
        needsInput ? "border-l-need" : "border-l-transparent",
      )}
    >
      <div className="order-2 justify-self-end md:order-1 md:justify-self-start">
        <TaskStateBadge state={state} />
      </div>

      <ProjectCell projectId={thread.projectId} title={project} />

      <div className="order-1 min-w-0 md:order-2">
        <ProjectName projectId={thread.projectId} title={project} className="mb-0.5 block break-words md:hidden" />
        <Link to={sessionPath(thread.t3ThreadId)} className="text-body font-medium break-words hover:underline">
          {thread.title}
        </Link>
        <div className="mt-0.5 flex flex-wrap gap-x-2.5 gap-y-0.5 font-mono text-meta text-muted">
          {state === "settled" && thread.settledAt !== null ? (
            <SettledMeta settledAt={thread.settledAt} runStartedAt={thread.runStartedAt} runEndedAt={thread.runEndedAt} />
          ) : (
            <span title={dateTime(thread.updatedAt)} className="tabular">
              {relativeTime(thread.updatedAt)}
            </span>
          )}
          <span>external thread</span>
          <PrChips prs={thread.pullRequests} detailTo={sessionPath(thread.t3ThreadId)} />
        </div>
        <LastWordLine state={state} preview={thread.lastWordPreview} className="mt-1 hidden md:block" />
      </div>

      <LastWordLine state={state} preview={thread.lastWordPreview} className="order-3 col-span-2 md:hidden" />

      <div className={cx("order-3 col-span-2 flex-wrap items-center gap-1.5 md:col-span-1 md:flex md:justify-end", href === null && !answerable ? "hidden" : "flex")}>
        {answerable ? <AnswerButton to={sessionPath(thread.t3ThreadId)} /> : null}
        {href === null ? null : (
          <AppButton small variant={needsInput && !answerable ? "primary" : "secondary"} href={href}>
            Open in app
          </AppButton>
        )}
        <RowCopyLink subject={{ kind: "session", id: thread.t3ThreadId, title: thread.title }} />
      </div>
    </div>
  );
}
