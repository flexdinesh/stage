import { useMutation } from "@tanstack/react-query";
import { useId, type ReactNode } from "react";
import { Link } from "react-router";
import { api, toQuery } from "../api/client.ts";
import { useInvalidateWork } from "../api/queries.ts";
import type { Task, Thread } from "../api/types.ts";
import { dateTime, modelText, relativeTime } from "../lib/format.ts";
import { canStart, isWaiting } from "../lib/tasks.ts";
import { IconArrowRight, IconChainLink, IconChevronDown } from "./icons.tsx";
import { TaskStateBadge } from "./StateBadge.tsx";
import { AppButton, Button, Chip, ErrorText, Muted, cx } from "./ui.tsx";

/** Standalone bordered task list. Inside a `Panel flush`, use `TaskRows` instead. */
export function TaskList({ tasks, emptyText = "No tasks." }: { tasks: Task[]; emptyText?: string }) {
  if (tasks.length === 0) return <Muted>{emptyText}</Muted>;
  return (
    <div className="border border-line bg-surface">
      <TaskRows tasks={tasks} emptyText={emptyText} />
    </div>
  );
}

/** Hairline-separated rows with no outer border (for `Panel flush`). Empty renders a padded hint line. */
export function TaskRows({ tasks, emptyText = "No tasks." }: { tasks: Task[]; emptyText?: string }) {
  if (tasks.length === 0) return <p className="px-3 py-2.5 text-[12.5px] text-muted">{emptyText}</p>;
  return (
    <ul>
      {tasks.map((t) => (
        <li key={t.id} className="border-b border-line last:border-b-0">
          <TaskRow task={t} />
        </li>
      ))}
    </ul>
  );
}

interface ProjectGroup {
  key: string;
  title: string;
  projectId: number | null;
  tasks: Task[];
  threads: Thread[];
}

/** Tasks and external threads bucketed by project, in first-appearance order (tasks first). */
function groupByProject(tasks: Task[], threads: Thread[]): ProjectGroup[] {
  const groups = new Map<string, ProjectGroup>();
  const bucket = (key: string, title: string, projectId: number | null) => {
    const found = groups.get(key);
    if (found !== undefined) return found;
    const g: ProjectGroup = { key, title, projectId, tasks: [], threads: [] };
    groups.set(key, g);
    return g;
  };
  for (const t of tasks) bucket(`p${t.projectId}`, t.projectTitle, t.projectId).tasks.push(t);
  for (const t of threads) {
    const g =
      t.projectId === null
        ? bucket(`t3:${t.t3ProjectId}`, t.projectTitle ?? t.t3ProjectId, null)
        : bucket(`p${t.projectId}`, t.projectTitle ?? t.t3ProjectId, t.projectId);
    g.threads.push(t);
  }
  return [...groups.values()];
}

/**
 * Rows split under one collapsible sub-header per project (for `Panel flush`). `collapsed` holds keys
 * `${scope}:${project key}`; `scope` is the state group so each panel collapses independently.
 */
export function ProjectGroupedRows({
  scope,
  tasks,
  threads = [],
  emptyText,
  isCollapsed,
  onToggle,
}: {
  scope: string;
  tasks: Task[];
  threads?: Thread[];
  emptyText: string;
  isCollapsed: (key: string) => boolean;
  onToggle: (key: string) => void;
}) {
  const id = useId();
  const groups = groupByProject(tasks, threads);
  if (groups.length === 0) return <p className="px-3 py-2.5 text-[12.5px] text-muted">{emptyText}</p>;
  return (
    <ul>
      {groups.map((g) => {
        const key = `${scope}:${g.key}`;
        const collapsed = isCollapsed(key);
        const bodyId = `${id}-${g.key}`;
        const count = g.tasks.length + g.threads.length;
        return (
          <li key={g.key} className="border-b border-line last:border-b-0">
            <div
              className={cx(
                "flex h-7 items-center gap-2 bg-[color-mix(in_srgb,var(--panel)_55%,var(--surface))] pr-3",
                !collapsed && "border-b border-line",
              )}
            >
              <button
                type="button"
                aria-expanded={!collapsed}
                aria-controls={bodyId}
                onClick={() => onToggle(key)}
                className="flex h-full min-w-0 flex-1 items-center gap-2 pl-3 text-left font-mono text-[11.5px] text-fg transition-colors duration-150 hover:bg-panel"
              >
                <span
                  aria-hidden
                  className={cx("inline-flex text-muted transition-transform duration-150", collapsed && "-rotate-90")}
                >
                  <IconChevronDown size={12} />
                </span>
                <span className="truncate">{g.title}</span>
                <span className="tabular text-[11px] text-muted">{count}</span>
              </button>
              {g.projectId === null ? null : (
                <Link
                  to={`/projects/${g.projectId}`}
                  className="inline-flex shrink-0 items-center gap-1 font-mono text-[11px] text-muted hover:text-fg hover:underline"
                >
                  project
                  <IconArrowRight size={12} />
                </Link>
              )}
            </div>
            {collapsed ? null : (
              <div id={bodyId}>
                {g.tasks.length === 0 ? null : (
                  <div className={cx(g.threads.length > 0 && "border-b border-line")}>
                    <TaskRows tasks={g.tasks} />
                  </div>
                )}
                {g.threads.length === 0 ? null : <ExternalThreadRows threads={g.threads} />}
              </div>
            )}
          </li>
        );
      })}
    </ul>
  );
}

/** A link chip: "after #X" while waiting on a predecessor, else "then #Y" when something runs after it. */
function linkChip(task: Task): { to: number; label: string; title: string } | null {
  if (isWaiting(task) && task.afterTaskId !== null)
    return { to: task.afterTaskId, label: `after #${task.afterTaskId}`, title: "Starts when this task settles" };
  if (task.nextTaskId !== null)
    return { to: task.nextTaskId, label: `then #${task.nextTaskId}`, title: "Runs next when this task settles" };
  return null;
}

/** Waiting tasks get no Start here (Start now, which unlinks, lives on task detail). */
export function TaskRow({ task, extra }: { task: Task; extra?: ReactNode }) {
  const invalidate = useInvalidateWork();
  const start = useMutation({ mutationFn: () => api.startTask(task.id), onSettled: invalidate });
  const since = task.launchedAt ?? task.createdAt;
  const showStart = canStart(task) && !isWaiting(task);
  const chip = linkChip(task);
  const needsInput = task.state === "needs_input";
  const hasActions = showStart || task.t3DeepLink !== null || extra !== undefined;

  return (
    <div
      className={cx(
        "grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-2.5 gap-y-1.5 border-l-2 px-3 py-2.5 md:grid-cols-[118px_minmax(0,1fr)_auto_auto] md:gap-x-3.5 md:py-2",
        needsInput ? "border-l-need" : "border-l-transparent",
      )}
    >
      <div className="order-2 justify-self-end md:order-1 md:justify-self-start">
        <TaskStateBadge state={task.state} />
      </div>

      <div className="order-1 min-w-0 md:order-2">
        <Link to={`/tasks/${task.id}`} className="text-[13.5px] font-medium break-words hover:underline">
          {task.title}
        </Link>
        <div className="mt-0.5 flex flex-wrap gap-x-2.5 gap-y-0.5 font-mono text-[11.5px] text-muted">
          <Link to={`/projects/${task.projectId}`} className="hover:text-fg hover:underline">
            {task.projectTitle}
          </Link>
          <span>{modelText(task.model, task.modelOptions)}</span>
          <span title={dateTime(since)} className="tabular">
            {relativeTime(since)}
          </span>
          {chip === null ? null : (
            <Link
              to={`/tasks/${chip.to}`}
              title={chip.title}
              className="inline-flex items-center gap-1 hover:text-fg hover:underline md:hidden"
            >
              <IconChainLink />
              {chip.label}
            </Link>
          )}
          {task.tags.map((tag) => (
            <span key={tag}>#{tag}</span>
          ))}
        </div>
      </div>

      <div className="hidden md:order-3 md:block">
        {chip === null ? null : (
          <Link to={`/tasks/${chip.to}`} title={chip.title} className="hover:opacity-80">
            <Chip tone="plain">
              <IconChainLink />
              {chip.label}
            </Chip>
          </Link>
        )}
      </div>

      {hasActions ? (
        <div className="order-4 col-span-2 flex flex-wrap items-center gap-1.5 md:col-span-1 md:justify-end">
          {showStart ? (
            <Button small variant="primary" busy={start.isPending} onClick={() => start.mutate()}>
              {task.state === "backlog" ? "Start" : "Restart"}
            </Button>
          ) : null}
          {task.t3DeepLink === null ? null : (
            <AppButton small variant={needsInput ? "primary" : "secondary"} href={task.t3DeepLink}>
              Open in app
            </AppButton>
          )}
          {extra}
        </div>
      ) : null}

      {task.lastError === null ? null : (
        <p
          className="order-5 col-span-2 truncate font-mono text-[11.5px] text-bad md:col-span-3 md:col-start-2"
          title={task.lastError}
        >
          {task.lastError}
        </p>
      )}
      {start.error === null ? null : (
        <div className="order-6 col-span-2 md:col-span-3 md:col-start-2">
          <ErrorText error={start.error} />
        </div>
      )}
    </div>
  );
}

/** External (non-task) threads needing input, styled as needs-input TaskRows. Title opens History filtered to it. */
export function ExternalThreadRows({ threads }: { threads: Thread[] }) {
  return (
    <ul>
      {threads.map((t) => (
        <li key={t.t3ThreadId} className="border-b border-line last:border-b-0">
          <ExternalThreadRow thread={t} />
        </li>
      ))}
    </ul>
  );
}

function ExternalThreadRow({ thread }: { thread: Thread }) {
  const href = thread.t3DeepLink;
  const project = thread.projectTitle ?? thread.t3ProjectId;
  return (
    <div className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-2.5 gap-y-1.5 border-l-2 border-l-need px-3 py-2.5 md:grid-cols-[118px_minmax(0,1fr)_auto] md:gap-x-3.5 md:py-2">
      <div className="order-2 justify-self-end md:order-1 md:justify-self-start">
        <TaskStateBadge state="needs_input" />
      </div>

      <div className="order-1 min-w-0 md:order-2">
        <Link to={`/history${toQuery({ q: thread.title })}`} className="text-[13.5px] font-medium break-words hover:underline">
          {thread.title}
        </Link>
        <div className="mt-0.5 flex flex-wrap gap-x-2.5 gap-y-0.5 font-mono text-[11.5px] text-muted">
          {thread.projectId === null ? (
            <span>{project}</span>
          ) : (
            <Link to={`/projects/${thread.projectId}`} className="hover:text-fg hover:underline">
              {project}
            </Link>
          )}
          <span title={dateTime(thread.updatedAt)} className="tabular">
            {relativeTime(thread.updatedAt)}
          </span>
          <span>external thread</span>
        </div>
      </div>

      {href === null ? null : (
        <div className="order-3 col-span-2 flex flex-wrap items-center gap-1.5 md:col-span-1 md:justify-end">
          <AppButton small variant="primary" href={href}>
            Open in app
          </AppButton>
        </div>
      )}
    </div>
  );
}
