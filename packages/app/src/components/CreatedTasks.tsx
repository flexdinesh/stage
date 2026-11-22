import { useEffect, useRef, useState, type ReactNode } from "react";
import { Link, useLocation, useSearchParams } from "react-router";
import type { CreatedTasks, Task, TaskDetail, TreeRef } from "../api/types.ts";
import { CREATED_TASKS_ANCHOR } from "../lib/sessions.ts";
import { batchSize } from "../lib/tasks.ts";
import { countMembers, nextActionText, treeRollup } from "../lib/trees.ts";
import { TaskFormModal } from "./TaskForm.tsx";
import { TaskRow } from "./TaskList.tsx";
import { NewTreeDrawer } from "./trees/NewTreeDrawer.tsx";
import { NodeChip } from "./trees/TreeNode.tsx";
import { TreeStateBar } from "./trees/TreeRow.tsx";
import { Button, Checkbox, Drawer, IdTag, Panel, PanelHeader, Row, RowList, TextLink, activeRowClass, buttonClass, cx } from "./ui.tsx";

/** Section header for a tree: its title linking the tree view, mono roll-up of the batch's members, state bar. */
function TreeHeader({ tree, tasks }: { tree: TreeRef; tasks: readonly Task[] }) {
  const rollup = treeRollup(countMembers(tasks));
  const n = tasks.length;
  return (
    <PanelHeader
      as="h3"
      title={<TextLink to={`/trees/${tree.id}`}>{tree.title}</TextLink>}
      meta={
        <span className="tabular">
          {n} task{n === 1 ? "" : "s"} · {nextActionText(rollup.next)} · {tree.startMode}
        </span>
      }
      actions={
        <div className="w-24">
          <TreeStateBar rollup={rollup} />
        </div>
      }
    />
  );
}

/** "Independent" section header, mono count. */
function IndependentHeader({ count }: { count: number }) {
  return <PanelHeader as="h3" title="Independent" meta={<span className="tabular">{count}</span>} />;
}

/**
 * Session detail panel: tasks this session created, grouped by tree (headers open the tree), then independent
 * tasks. Start a task, edit a backlog task; check independent backlog tasks to make a tree of them.
 */
export function CreatedTasksPanel({ threadId, batch }: { threadId: string; batch: CreatedTasks }) {
  // The id only: the form edits the live task from the batch, never a copy from when it opened.
  const [editingId, setEditingId] = useState<number | null>(null);
  const editing = [...batch.trees.flatMap((t) => t.tasks), ...batch.independent].find((t) => t.id === editingId) ?? null;
  // Selection order matters: a chain runs in the order tasks were checked.
  const [selected, setSelected] = useState<number[]>([]);
  const [planting, setPlanting] = useState(false);
  const panel = useRef<HTMLDivElement>(null);
  const { hash } = useLocation();
  useEffect(() => {
    if (hash === `#${CREATED_TASKS_ANCHOR}`) panel.current?.scrollIntoView({ block: "start" });
  }, [hash]);

  const total = batchSize(batch);
  if (total === 0) return null;

  const editButton = (t: Task) =>
    t.state === "backlog" ? (
      <Button small onClick={() => setEditingId(t.id)}>
        Edit
      </Button>
    ) : undefined;
  const rows = (tasks: Task[]): ReactNode =>
    tasks.map((t) => (
      <Row key={t.id} bare>
        <TaskRow task={t} contextThreadId={threadId} extra={editButton(t)} />
      </Row>
    ));

  // Only backlog tasks can be given needs, so only they can be checked.
  const selectable = batch.independent.filter((t) => t.state === "backlog");
  const picked = selected.flatMap((id) => selectable.find((t) => t.id === id) ?? []);
  const toggle = (id: number, on: boolean) => setSelected((prev) => (on ? [...prev.filter((n) => n !== id), id] : prev.filter((n) => n !== id)));
  const independentRows = batch.independent.map((t) => {
    const canPick = t.state === "backlog";
    return (
      <Row key={t.id} bare className="flex items-start md:items-center">
        {selectable.length === 0 ? null : (
          <div className="flex w-4 shrink-0 pt-3 pl-3 box-content md:pt-0">
            {canPick ? (
              <Checkbox
                aria-label={`Select #${t.id} for a new tree`}
                checked={picked.some((p) => p.id === t.id)}
                onChange={(e) => toggle(t.id, e.target.checked)}
              />
            ) : null}
          </div>
        )}
        <div className="min-w-0 flex-1">
          <TaskRow task={t} contextThreadId={threadId} extra={editButton(t)} />
        </div>
      </Row>
    );
  });

  return (
    <div ref={panel} id={CREATED_TASKS_ANCHOR} className="scroll-mt-16">
      <Panel
        title="Created tasks"
        meta={`${total}`}
        flush
        {...(picked.length === 0
          ? {}
          : {
              actions: (
                <>
                  <Button variant="ghost" small onClick={() => setSelected([])}>
                    Clear
                  </Button>
                  <Button variant="primary" small onClick={() => setPlanting(true)}>
                    New tree from {picked.length}
                  </Button>
                </>
              ),
            })}
      >
        {batch.trees.map(({ tree, tasks }) => (
          <section key={tree.id} className="border-b border-line last:border-b-0">
            <TreeHeader tree={tree} tasks={tasks} />
            <RowList label={`Tree ${tree.title}`}>{rows(tasks)}</RowList>
          </section>
        ))}
        {batch.independent.length === 0 ? null : (
          <section className="border-b border-line last:border-b-0">
            <IndependentHeader count={batch.independent.length} />
            <RowList>{independentRows}</RowList>
          </section>
        )}
      </Panel>
      {editing === null ? null : <TaskFormModal task={editing} onClose={() => setEditingId(null)} />}
      {planting ? (
        <NewTreeDrawer
          tasks={picked}
          onClose={() => setPlanting(false)}
          onCreated={() => setSelected([])}
        />
      ) : null}
    </div>
  );
}

/** Task detail side panel: the batch this task's origin session created, with a link to act on it there. */
export function BatchPanel({ task }: { task: TaskDetail }) {
  const [params, setParams] = useSearchParams();
  const batch = task.batch;
  const close = () => setParams((previous) => { const next = new URLSearchParams(previous); next.delete("batch"); return next; });
  if (batch === null || task.originSession === null || batchSize(batch) < 1) return null;
  const row = (t: Task) => {
    const current = t.id === task.id;
    return (
      <Row key={t.id} className={cx("grid grid-cols-[minmax(0,1fr)_auto] items-center gap-2", current && activeRowClass)}>
        <TextLink
          to={`/tasks/${t.id}`}
          aria-current={current ? "page" : undefined}
          className={cx("min-w-0 truncate text-small", current ? "font-semibold" : "font-medium")}
          title={t.title}
        >
          <IdTag id={t.id} className="mr-1.5 font-normal" />
          {t.title}
        </TextLink>
        <NodeChip task={t} />
      </Row>
    );
  };
  return (
    <>
    {params.has("batch") ? <Drawer title="Created tasks" wide onClose={close}><CreatedTasksPanel threadId={task.originSession.externalSessionId} batch={batch} /></Drawer> : null}
    <Panel
      title="Batch"
      meta={`${batchSize(batch)}`}
      flush
      actions={
        <Link to={`/tasks/${task.id}?batch=1`} className={buttonClass("ghost", true)}>
          View batch
        </Link>
      }
    >
      {batch.trees.map(({ tree, tasks }) => (
        <section key={tree.id} className="border-b border-line last:border-b-0">
          <PanelHeader as="h3" title={<TextLink to={`/trees/${tree.id}`}>{tree.title}</TextLink>} />
          <RowList>{tasks.map(row)}</RowList>
        </section>
      ))}
      {batch.independent.length === 0 ? null : (
        <section className="border-b border-line last:border-b-0">
          <PanelHeader as="h3" title="Independent" />
          <RowList>{batch.independent.map(row)}</RowList>
        </section>
      )}
    </Panel>
    </>
  );
}
