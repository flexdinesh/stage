import { useMutation } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { Link } from "react-router";
import { api } from "../api/client.ts";
import { useInvalidateWork } from "../api/queries.ts";
import type { Task } from "../api/types.ts";
import { dateTime, modelText, relativeTime } from "../lib/format.ts";
import { canStart } from "../lib/tasks.ts";
import { IconChainLink, IconExternal } from "./icons.tsx";
import { TaskStateBadge } from "./StateBadge.tsx";
import { Button, Chip, ErrorText, ExternalButton, Muted, cx } from "./ui.tsx";

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

/** `allowBacklogStart` false: hide Start for backlog (chain steps are launched by the chain runner). */
export function TaskRow({
  task,
  extra,
  allowBacklogStart = true,
}: {
  task: Task;
  extra?: ReactNode;
  allowBacklogStart?: boolean;
}) {
  const invalidate = useInvalidateWork();
  const start = useMutation({ mutationFn: () => api.startTask(task.id), onSettled: invalidate });
  const since = task.launchedAt ?? task.createdAt;
  const showStart = canStart(task) && (allowBacklogStart || task.state !== "backlog");
  const needsInput = task.state === "needs_input";
  const hasActions = showStart || task.t3WebUrl !== null || extra !== undefined;

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
          {task.chainId === null ? null : (
            <Link
              to={`/chains/${task.chainId}`}
              className="inline-flex items-center gap-1 hover:text-fg hover:underline md:hidden"
            >
              <IconChainLink />
              chain
            </Link>
          )}
          {task.tags.map((tag) => (
            <span key={tag}>#{tag}</span>
          ))}
        </div>
      </div>

      <div className="hidden md:order-3 md:block">
        {task.chainId === null ? null : (
          <Link to={`/chains/${task.chainId}`} title="Open chain" className="hover:opacity-80">
            <Chip tone="plain">
              <IconChainLink />
              chain
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
          {task.t3WebUrl === null ? null : (
            <ExternalButton small variant={needsInput ? "primary" : "secondary"} href={task.t3WebUrl}>
              Open in T3
              <IconExternal size={12} />
            </ExternalButton>
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
