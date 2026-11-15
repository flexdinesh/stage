import { useMutation } from "@tanstack/react-query";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { flushSync } from "react-dom";
import { Link, useLocation } from "react-router";
import { useApi } from "../platform/context.tsx";
import { useInvalidateWork } from "../api/queries.ts";
import type { CreatedTasks, Task, TaskDetail } from "../api/types.ts";
import { browserTimeZone, wallTime } from "../lib/schedules.ts";
import { CREATED_TASKS_ANCHOR, batchPath } from "../lib/sessions.ts";
import { batchSize, canReorder, canSelectForOrder, chainAction, chainProgress, moveItem, waitTarget } from "../lib/tasks.ts";
import { IconChevronDown, IconChevronUp } from "./icons.tsx";
import { TaskStateBadge } from "./StateBadge.tsx";
import { TaskFormModal } from "./TaskForm.tsx";
import { TaskRow } from "./TaskList.tsx";
import { Button, Checkbox, Drawer, ErrorText, Panel, buttonClass, cx } from "./ui.tsx";

type OrderMode = "link" | "reorder";

/** Section label inside the panel: Slate label, mono meta, actions right. */
function GroupHeader({ label, meta, actions }: { label: string; meta: string; actions?: ReactNode }) {
  return (
    <div className="flex min-h-10 flex-wrap items-center gap-x-2 gap-y-1 border-b border-line px-3 py-1 md:min-h-8">
      <h3 className="label-caps text-muted">{label}</h3>
      <span className="font-mono text-meta-sm text-muted tabular">{meta}</span>
      {actions === undefined ? null : <div className="ml-auto flex flex-wrap items-center gap-1.5">{actions}</div>}
    </div>
  );
}

/** Leading column of a batch row (step number or checkbox), centred on the row like its state chip. */
function Lead({ children }: { children?: ReactNode }) {
  return <div className="ml-3 flex w-4 shrink-0 items-start justify-center self-stretch pt-3 md:items-center md:pt-0">{children}</div>;
}

/**
 * Session detail panel: tasks this session created, chains in run order (head first) then independent tasks.
 * Start a chain, start a task, link selected tasks in order, reorder an unstarted chain, edit a backlog task.
 */
export function CreatedTasksPanel({ threadId, batch }: { threadId: string; batch: CreatedTasks }) {
  const [picked, setPicked] = useState<number[]>([]);
  const [ordering, setOrdering] = useState<{ mode: OrderMode; tasks: Task[] } | null>(null);
  const [editing, setEditing] = useState<Task | null>(null);
  const panel = useRef<HTMLDivElement>(null);
  const { hash } = useLocation();
  useEffect(() => {
    if (hash === `#${CREATED_TASKS_ANCHOR}`) panel.current?.scrollIntoView({ block: "start" });
  }, [hash]);

  const selectable = batch.independent.filter(canSelectForOrder);
  // Selection survives refetches only for tasks still selectable, in click order.
  const selected = picked.flatMap((id) => selectable.filter((t) => t.id === id));
  const total = batchSize(batch);
  if (total === 0) return null;

  const toggle = (id: number) => setPicked((cur) => (cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id]));
  const editButton = (t: Task) =>
    t.state === "backlog" ? (
      <Button small onClick={() => setEditing(t)}>
        Edit
      </Button>
    ) : undefined;

  return (
    <div ref={panel} id={CREATED_TASKS_ANCHOR} className="scroll-mt-16">
      <Panel
        title="Created tasks"
        meta={`${total}`}
        flush
        actions={
          selected.length >= 2 ? (
            <>
              <Button small variant="ghost" onClick={() => setPicked([])}>
                Clear
              </Button>
              <Button small variant="primary" onClick={() => setOrdering({ mode: "link", tasks: selected })}>
                Link {selected.length} in order…
              </Button>
            </>
          ) : selectable.length >= 2 ? (
            <span className="font-mono text-meta-sm text-muted max-md:hidden">select backlog tasks to link in order</span>
          ) : undefined
        }
      >
        {batch.chains.map((chain) => (
          <ChainGroup
            key={chain[0]?.id}
            chain={chain}
            threadId={threadId}
            extra={editButton}
            onReorder={() => setOrdering({ mode: "reorder", tasks: chain })}
          />
        ))}
        {batch.independent.length === 0 ? null : (
          <section className="border-b border-line last:border-b-0">
            <GroupHeader label="Independent" meta={`${batch.independent.length}`} />
            <ul>
              {batch.independent.map((t) => (
                <li key={t.id} className="flex border-b border-line last:border-b-0">
                  <Lead>
                    {canSelectForOrder(t) ? (
                      <Checkbox
                        checked={selected.some((s) => s.id === t.id)}
                        onChange={() => toggle(t.id)}
                        aria-label={`Select #${t.id} ${t.title} to link in order`}
                      />
                    ) : null}
                  </Lead>
                  <div className="min-w-0 flex-1">
                    <TaskRow task={t} contextThreadId={threadId} extra={editButton(t)} />
                  </div>
                </li>
              ))}
            </ul>
          </section>
        )}
      </Panel>
      {ordering === null ? null : (
        <OrderDrawer
          mode={ordering.mode}
          initial={ordering.tasks}
          onClose={() => setOrdering(null)}
          onSaved={() => setPicked([])}
        />
      )}
      {editing === null ? null : <TaskFormModal task={editing} onClose={() => setEditing(null)} />}
    </div>
  );
}

/** One chain: header with Start chain / Reorder, then numbered steps. */
function ChainGroup({
  chain,
  threadId,
  extra,
  onReorder,
}: {
  chain: Task[];
  threadId: string;
  extra: (t: Task) => ReactNode;
  onReorder: () => void;
}) {
  const api = useApi();
  const invalidate = useInvalidateWork();
  const head = chain[0];
  const start = useMutation({ mutationFn: (id: number) => api.startTask(id), onSettled: invalidate });
  const [armed, setArmed] = useState(false);
  if (head === undefined) return null;
  const action = chainAction(head);
  const progress = chainProgress(chain);
  const startButton =
    action === "none" ? null : action === "confirm" ? (
      <Button
        small
        variant={armed ? "primary" : "secondary"}
        busy={start.isPending}
        title={`Unlinks #${head.id} from ${waitTarget(head)} and starts it now; the rest follow in order`}
        onClick={() => (armed ? start.mutate(head.id) : setArmed(true))}
        onBlur={() => setArmed(false)}
      >
        {armed ? `Confirm: unlink from ${waitTarget(head)} and start` : "Start chain now"}
      </Button>
    ) : (
      <Button
        small
        busy={start.isPending}
        title={action === "start_now" ? `Starts #${head.id} now instead of at its start time; the rest follow in order` : `Starts #${head.id}; the rest follow in order`}
        onClick={() => start.mutate(head.id)}
      >
        {action === "start_now" ? "Start chain now" : "Start chain"}
      </Button>
    );

  return (
    <section className="border-b border-line last:border-b-0">
      <GroupHeader
        label="Chain"
        meta={[`${chain.length} tasks`, progress].filter((x) => x !== null).join(" · ")}
        actions={
          <>
            {startButton}
            {canReorder(chain) ? (
              <Button small variant="ghost" onClick={onReorder}>
                Reorder…
              </Button>
            ) : null}
          </>
        }
      />
      <ol aria-label={`Chain of ${chain.length} tasks, run order`}>
        {chain.map((t, i) => (
          <li key={t.id} className="flex border-b border-line last:border-b-0">
            <Lead>
              <span className="font-mono text-meta text-muted tabular" aria-label={`Step ${i + 1}`}>
                {i + 1}
              </span>
            </Lead>
            <div className="min-w-0 flex-1">
              <TaskRow task={t} contextThreadId={threadId} extra={extra(t)} hideStart={i === 0 && chainAction(t) !== "none"} />
            </div>
          </li>
        ))}
      </ol>
      {start.error === null ? null : (
        <div className="px-3 pb-2.5">
          <ErrorText error={start.error} />
        </div>
      )}
    </section>
  );
}

/** A task's own wait, for the order drawer: "at 02:00", "after #12", "after session …". */
function waitLabel(t: Task): string | null {
  if (t.startAt !== null) return `at ${wallTime(t.startAt, t.startTz ?? browserTimeZone(), { short: true })}`;
  const target = waitTarget(t);
  return target === "" ? null : `after ${target}`;
}

/** Arrange tasks with up/down, then save the order in one call (POST /api/tasks/order). */
function OrderDrawer({
  mode,
  initial,
  onClose,
  onSaved,
}: {
  mode: OrderMode;
  initial: Task[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const api = useApi();
  const invalidate = useInvalidateWork();
  const [order, setOrder] = useState(initial);
  const list = useRef<HTMLOListElement>(null);
  const save = useMutation({
    mutationFn: () => api.orderTasks(order.map((t) => t.id)),
    onSuccess: () => {
      onSaved();
      onClose();
    },
    onSettled: invalidate,
  });
  // Keep focus on the moved row's control (or its other arrow once it reaches an end).
  const move = (t: Task, i: number, dir: "up" | "down") => {
    flushSync(() => setOrder((cur) => moveItem(cur, i, dir === "up" ? -1 : 1)));
    const button = (d: string) => list.current?.querySelector<HTMLButtonElement>(`[data-move="${t.id}:${d}"]`);
    const el = button(dir);
    if (el?.disabled === true) button(dir === "up" ? "down" : "up")?.focus();
    else el?.focus();
  };
  const changed = order.some((t, i) => t.id !== initial[i]?.id);

  return (
    <Drawer
      title={mode === "link" ? "Link in order" : "Reorder chain"}
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button
            variant="primary"
            className="ml-auto"
            busy={save.isPending}
            disabled={mode === "reorder" && !changed}
            onClick={() => save.mutate()}
          >
            Save order
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <p className="text-small text-muted">
          Each task starts when the one above it settles.{" "}
          {mode === "link"
            ? "The first keeps its own wait; start times and session waits of the others are replaced by the link."
            : "The chain's wait moves to whichever task runs first."}
        </p>
        <ol ref={list} aria-label="Run order" className="border border-line">
          {order.map((t, i) => {
            // Link: each task's own wait (kept on the first, replaced on the others). Reorder: the chain's wait,
            // on whichever task runs first.
            const wait = mode === "link" ? waitLabel(t) : i === 0 && initial[0] !== undefined ? waitLabel(initial[0]) : null;
            const replaced = mode === "link" && i > 0;
            return (
              <li key={t.id} className="flex items-center gap-3 border-b border-line px-3 py-2 last:border-b-0">
                <span className="w-4 shrink-0 text-right font-mono text-meta text-muted tabular">{i + 1}</span>
                <div className="min-w-0 flex-1">
                  <div className="truncate text-body font-medium">
                    <span className="mr-1.5 font-mono text-meta font-normal text-muted tabular">#{t.id}</span>
                    {t.title}
                  </div>
                  <div className="flex flex-wrap gap-x-2.5 font-mono text-meta text-muted">
                    <span className="truncate">{t.projectTitle}</span>
                    {wait === null ? null : (
                      <span title={replaced ? "Replaced by the link" : undefined}>
                        <span className={cx(replaced && "line-through")}>{wait}</span>
                        {replaced ? " · replaced" : null}
                      </span>
                    )}
                  </div>
                </div>
                <div className="flex shrink-0 gap-1">
                  <Button
                    small
                    variant="ghost"
                    data-move={`${t.id}:up`}
                    aria-label={`Move #${t.id} ${t.title} up`}
                    disabled={i === 0}
                    onClick={() => move(t, i, "up")}
                  >
                    <IconChevronUp />
                  </Button>
                  <Button
                    small
                    variant="ghost"
                    data-move={`${t.id}:down`}
                    aria-label={`Move #${t.id} ${t.title} down`}
                    disabled={i === order.length - 1}
                    onClick={() => move(t, i, "down")}
                  >
                    <IconChevronDown />
                  </Button>
                </div>
              </li>
            );
          })}
        </ol>
        <ErrorText error={save.error} />
      </div>
    </Drawer>
  );
}

/** Task detail side panel: the batch this task's origin session created, with a link to act on it there. */
export function BatchPanel({ task }: { task: TaskDetail }) {
  const batch = task.batch;
  if (batch === null || task.originThreadId === null || batchSize(batch) < 2) return null;
  const row = (t: Task, step: number | null) => {
    const current = t.id === task.id;
    return (
      <li
        key={t.id}
        className={cx(
          "grid grid-cols-[1rem_minmax(0,1fr)_auto] items-center gap-2 border-b border-line px-3 py-2 last:border-b-0 md:py-1.5",
          current && "outline outline-1 -outline-offset-2 outline-fg",
        )}
      >
        <span className="text-right font-mono text-meta text-muted tabular">{step ?? ""}</span>
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
      {batch.chains.map((chain) => (
        <section key={chain[0]?.id} className="border-b border-line last:border-b-0">
          <h3 className="label-caps border-b border-line px-3 py-1.5 text-muted">Chain</h3>
          <ol>{chain.map((t, i) => row(t, i + 1))}</ol>
        </section>
      ))}
      {batch.independent.length === 0 ? null : (
        <section className="border-b border-line last:border-b-0">
          <h3 className="label-caps border-b border-line px-3 py-1.5 text-muted">Independent</h3>
          <ul>{batch.independent.map((t) => row(t, null))}</ul>
        </section>
      )}
    </Panel>
  );
}
