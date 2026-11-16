import { useState } from "react";
import { Link } from "react-router";
import type { Task, TreeDetail } from "../../api/types.ts";
import { dateTime, relativeTime, shortDate } from "../../lib/format.ts";
import { layoutLevels, nodeState, treeMeta } from "../../lib/trees.ts";
import { taskStateLabel, taskStateTone } from "../StateBadge.tsx";
import { Button, Muted, Panel, buttonClass, cx, toneClass } from "../ui.tsx";
import { AddToTreeDrawer } from "./AddToTreeDrawer.tsx";
import { TreeNodeEditor } from "./TreeNodePanel.tsx";

/**
 * Compact level view: one row of cells per level (roots first), each a tone square and mono `#id` linking the task.
 * Not-started members are dashed; `current` gets the Ink outline.
 */
export function MiniLevels({ tree, current }: { tree: Pick<TreeDetail, "tasks">; current: number }) {
  const byId = new Map(tree.tasks.map((t) => [t.id, t]));
  const levels = layoutLevels(tree.tasks);
  return (
    <ol aria-label="Tree levels" className="flex flex-col gap-1.5">
      {levels.map((band, i) => (
        <li key={band.join("-")} aria-label={`Level ${i + 1}`} className="flex flex-wrap gap-1.5">
          {band.flatMap((id) => {
            const t = byId.get(id);
            if (t === undefined) return [];
            const s = nodeState(t);
            const label = s === "ready" || s === "blocked" ? s : taskStateLabel(s);
            const tone = s === "ready" || s === "blocked" ? "idle" : taskStateTone(s);
            const here = t.id === current;
            return [
              <Link
                key={id}
                to={`/tasks/${id}`}
                aria-current={here ? "page" : undefined}
                aria-label={`Task #${id}: ${t.title}, ${label}`}
                title={`#${id} ${t.title} · ${label}`}
                className={cx(
                  toneClass(tone),
                  "inline-flex h-8 items-center gap-1.5 border border-line-strong bg-surface px-2 font-mono text-meta text-fg tabular transition-colors duration-150 hover:bg-panel md:h-6 md:px-1.5",
                  t.state === "backlog" && "border-dashed",
                  here && "outline outline-1 outline-offset-1 outline-fg",
                )}
              >
                <span aria-hidden className="size-1.5 bg-[var(--c)]" />#{id}
              </Link>,
            ];
          })}
        </li>
      ))}
    </ol>
  );
}

/**
 * Task detail side panel (top of the side column) for a tree member: the tree's title and meta, the compact level
 * view with this task outlined, then the member's tree controls (needs, dependents, start mode, base, handoff, remove).
 */
export function TreePanel({ task, tree }: { task: Task; tree: TreeDetail }) {
  return (
    <Panel
      title="Tree"
      flush
      actions={
        <Link to={`/trees/${tree.id}`} className={buttonClass("ghost", true)}>
          Open tree
        </Link>
      }
    >
      <div className="flex min-w-0 flex-col gap-2.5 border-b border-line px-3 py-3">
        <div className="min-w-0">
          <Link to={`/trees/${tree.id}`} className="text-body font-medium break-words hover:underline">
            {tree.title}
          </Link>
          <p className="mt-0.5 font-mono text-meta text-muted tabular">{treeMeta(tree)}</p>
          <p className="font-mono text-meta text-muted tabular">
            <span title={dateTime(tree.createdAt)}>created {shortDate(tree.createdAt)}</span> ·{" "}
            <span title={dateTime(tree.lastActivityAt)} className="text-fg">
              active {relativeTime(tree.lastActivityAt)}
            </span>
          </p>
        </div>
        <MiniLevels tree={tree} current={task.id} />
      </div>
      <TreeNodeEditor tree={tree} task={task} />
    </Panel>
  );
}

/** Task detail side panel for a task outside any tree: says so, with Add to tree. */
export function NoTreePanel({ task }: { task: Task }) {
  const [adding, setAdding] = useState(false);
  return (
    <Panel
      title="Tree"
      actions={
        <Button small onClick={() => setAdding(true)}>
          Add to tree
        </Button>
      }
    >
      <Muted>Not in a tree. Add it to one so it can need other tasks, or be needed.</Muted>
      {adding ? <AddToTreeDrawer task={task} onClose={() => setAdding(false)} /> : null}
    </Panel>
  );
}
