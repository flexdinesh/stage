import { useEffect, useRef, useState, type ReactNode } from "react";
import { Link, useLocation } from "react-router";
import type { CreatedTasks, Task, TaskDetail } from "../api/types.ts";
import { CREATED_TASKS_ANCHOR, batchPath } from "../lib/sessions.ts";
import { batchSize } from "../lib/tasks.ts";
import { TaskStateBadge } from "./StateBadge.tsx";
import { TaskFormModal } from "./TaskForm.tsx";
import { TaskRow } from "./TaskList.tsx";
import { Button, Panel, buttonClass, cx } from "./ui.tsx";

/** Section header inside the panel: a tree's title (or the Slate "Independent" label), mono meta. */
function GroupHeader({ label, meta, caps = false }: { label: string; meta: string; caps?: boolean }) {
  return (
    <div className="flex min-h-10 flex-wrap items-center gap-x-2 gap-y-1 border-b border-line px-3 py-1 md:min-h-8">
      <h3 className={caps ? "label-caps text-muted" : "min-w-0 truncate text-small font-medium"}>{label}</h3>
      <span className="font-mono text-meta-sm text-muted tabular">{meta}</span>
    </div>
  );
}

/**
 * Session detail panel: tasks this session created, grouped by tree (each tree's tasks by id), then independent
 * tasks. Start a task, edit a backlog task.
 */
export function CreatedTasksPanel({ threadId, batch }: { threadId: string; batch: CreatedTasks }) {
  const [editing, setEditing] = useState<Task | null>(null);
  const panel = useRef<HTMLDivElement>(null);
  const { hash } = useLocation();
  useEffect(() => {
    if (hash === `#${CREATED_TASKS_ANCHOR}`) panel.current?.scrollIntoView({ block: "start" });
  }, [hash]);

  const total = batchSize(batch);
  if (total === 0) return null;

  const editButton = (t: Task) =>
    t.state === "backlog" ? (
      <Button small onClick={() => setEditing(t)}>
        Edit
      </Button>
    ) : undefined;
  const rows = (tasks: Task[]): ReactNode =>
    tasks.map((t) => (
      <li key={t.id} className="border-b border-line last:border-b-0">
        <TaskRow task={t} contextThreadId={threadId} extra={editButton(t)} />
      </li>
    ));

  return (
    <div ref={panel} id={CREATED_TASKS_ANCHOR} className="scroll-mt-16">
      <Panel title="Created tasks" meta={`${total}`} flush>
        {batch.trees.map(({ tree, tasks }) => (
          <section key={tree.id} className="border-b border-line last:border-b-0">
            <GroupHeader label={tree.title} meta={`${tasks.length} tasks · ${tree.startMode}`} />
            <ul aria-label={`Tree ${tree.title}`}>{rows(tasks)}</ul>
          </section>
        ))}
        {batch.independent.length === 0 ? null : (
          <section className="border-b border-line last:border-b-0">
            <GroupHeader label="Independent" meta={`${batch.independent.length}`} caps />
            <ul>{rows(batch.independent)}</ul>
          </section>
        )}
      </Panel>
      {editing === null ? null : <TaskFormModal task={editing} onClose={() => setEditing(null)} />}
    </div>
  );
}

/** Task detail side panel: the batch this task's origin session created, with a link to act on it there. */
export function BatchPanel({ task }: { task: TaskDetail }) {
  const batch = task.batch;
  if (batch === null || task.originThreadId === null || batchSize(batch) < 2) return null;
  const row = (t: Task) => {
    const current = t.id === task.id;
    return (
      <li
        key={t.id}
        className={cx(
          "grid grid-cols-[minmax(0,1fr)_auto] items-center gap-2 border-b border-line px-3 py-2 last:border-b-0 md:py-1.5",
          current && "outline outline-1 -outline-offset-2 outline-fg",
        )}
      >
        <Link
          to={`/tasks/${t.id}`}
          aria-current={current ? "page" : undefined}
          className={cx("min-w-0 truncate text-small hover:underline", current ? "font-semibold" : "font-medium")}
          title={t.title}
        >
          <span className="mr-1.5 font-mono text-meta font-normal text-muted tabular">#{t.id}</span>
          {t.title}
        </Link>
        <TaskStateBadge state={t.state} />
      </li>
    );
  };
  return (
    <Panel
      title="Batch"
      meta={`${batchSize(batch)}`}
      flush
      actions={
        <Link to={batchPath(task.originThreadId)} className={buttonClass("ghost", true)}>
          Open in session
        </Link>
      }
    >
      {batch.trees.map(({ tree, tasks }) => (
        <section key={tree.id} className="border-b border-line last:border-b-0">
          <h3 className="truncate border-b border-line px-3 py-1.5 text-small font-medium">{tree.title}</h3>
          <ul>{tasks.map(row)}</ul>
        </section>
      ))}
      {batch.independent.length === 0 ? null : (
        <section className="border-b border-line last:border-b-0">
          <h3 className="label-caps border-b border-line px-3 py-1.5 text-muted">Independent</h3>
          <ul>{batch.independent.map(row)}</ul>
        </section>
      )}
    </Panel>
  );
}
