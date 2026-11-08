import { useMutation } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { Link } from "react-router";
import { api } from "../api/client.ts";
import { useInvalidateWork } from "../api/queries.ts";
import type { Task } from "../api/types.ts";
import { dateTime, modelText, relativeTime } from "../lib/format.ts";
import { canStart } from "../lib/tasks.ts";
import { TaskStateBadge } from "./StateBadge.tsx";
import { Button, ErrorText, ExternalButton, Muted } from "./ui.tsx";

export function TaskList({ tasks, emptyText = "No tasks." }: { tasks: Task[]; emptyText?: string }) {
  if (tasks.length === 0) return <Muted>{emptyText}</Muted>;
  return (
    <ul className="divide-y divide-neutral-200 rounded-lg border border-neutral-200 bg-white dark:divide-neutral-800 dark:border-neutral-800 dark:bg-neutral-900">
      {tasks.map((t) => (
        <li key={t.id}>
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

  return (
    <div className="px-3 py-2">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
        <div className="min-w-0 flex-1 basis-60">
          <Link to={`/tasks/${task.id}`} className="font-medium hover:underline">
            {task.title}
          </Link>
          <div className="mt-0.5 flex flex-wrap gap-x-2 text-xs text-neutral-500">
            <Link to={`/projects/${task.projectId}`} className="hover:underline">
              {task.projectTitle}
            </Link>
            <span>{modelText(task.model, task.modelOptions)}</span>
            <span title={dateTime(since)}>{relativeTime(since)}</span>
            {task.tags.map((tag) => (
              <span key={tag} className="text-violet-600 dark:text-violet-400">
                #{tag}
              </span>
            ))}
          </div>
        </div>
        <TaskStateBadge state={task.state} />
        <div className="flex flex-wrap items-center gap-1.5">
          {canStart(task) && (allowBacklogStart || task.state !== "backlog") ? (
            <Button small variant="primary" busy={start.isPending} onClick={() => start.mutate()}>
              {task.state === "backlog" ? "Start" : "Restart"}
            </Button>
          ) : null}
          {task.t3WebUrl === null ? null : (
            <ExternalButton small href={task.t3WebUrl}>
              Open in T3
            </ExternalButton>
          )}
          {extra}
        </div>
      </div>
      {task.lastError === null ? null : (
        <p className="mt-1 truncate text-xs text-red-600 dark:text-red-400" title={task.lastError}>
          {task.lastError}
        </p>
      )}
      {start.error === null ? null : (
        <div className="mt-1.5">
          <ErrorText error={start.error} />
        </div>
      )}
    </div>
  );
}
