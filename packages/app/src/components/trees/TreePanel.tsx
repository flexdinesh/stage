import { useState } from "react";
import { Link } from "react-router";
import type { Task, TreeDetail } from "../../api/types.ts";
import { closedLabel, countMembers, layoutLevels, nodeState, treePosition, treeTaskPath } from "../../lib/trees.ts";
import { IconBack, IconStep, IconTree } from "../icons.tsx";
import { taskStateLabel, taskStateTone } from "../StateBadge.tsx";
import { Button, Chip, EmptyState, Marker, Meta, Panel, TextLink, buttonClass, cx } from "../ui.tsx";
import { AddToTreeDrawer } from "./AddToTreeDrawer.tsx";
import { TreeNodeEditor } from "./TreeNodePanel.tsx";

/**
 * Compact level view: one inline run of levels in execution order (roots first, a step chevron between levels), each
 * cell a tone square and mono `#id` linking the task. Not-started members are dashed; `current` gets the Ink outline.
 */
export function MiniLevels({ tree, current }: { tree: Pick<TreeDetail, "tasks">; current: number }) {
  const byId = new Map(tree.tasks.map((t) => [t.id, t]));
  const levels = layoutLevels(tree.tasks);
  return (
    <ol aria-label="Tree levels" className="flex flex-wrap items-center gap-1.5">
      {levels.map((band, i) => (
        <li key={band.join("-")} aria-label={`Level ${i + 1}`} className="flex flex-wrap items-center gap-1.5">
          {i === 0 ? null : (
            <span aria-hidden className="text-muted">
              <IconStep />
            </span>
          )}
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
                  "inline-flex h-control-sm items-center gap-1.5 border border-line-strong bg-surface px-2 font-mono text-meta text-fg tabular transition-colors duration-150 hover:bg-panel md:px-1.5",
                  t.state === "backlog" && "border-dashed",
                  here && "outline outline-1 outline-offset-1 outline-fg",
                )}
              >
                <Marker tone={tone} />#{id}
              </Link>,
            ];
          })}
        </li>
      ))}
    </ol>
  );
}

/**
 * Task detail bar for a tree member, between the chips and the actions: the tree it sits in (linked), its place in
 * level order, the compact level view with this task outlined, and Back to tree (the tree view with this task selected).
 */
export function TreeBar({ task, tree }: { task: Task; tree: TreeDetail }) {
  const chip = closedLabel(tree, countMembers(tree.tasks));
  const position = treePosition(tree.tasks, task.id);
  const back = treeTaskPath(tree.id, task.id);
  return (
    <nav
      aria-label="Tree"
      className="flex flex-wrap items-center gap-x-3.5 gap-y-2.5 border border-line-strong bg-surface py-row pr-2.5 pl-3 md:pr-2"
    >
      <div className="flex min-w-0 items-center gap-2 max-md:basis-full">
        <span className="text-muted">
          <IconTree />
        </span>
        <span className="label-caps text-muted max-md:hidden">Tree</span>
        <TextLink to={back} className="min-w-0 text-body font-medium break-words">
          {tree.title}
        </TextLink>
        {chip === null ? null : <Chip>{chip}</Chip>}
        {position === null ? null : (
          <Meta className="shrink-0 whitespace-nowrap tabular">
            <span className="max-md:hidden">task </span>
            {position.index} of {position.total}
          </Meta>
        )}
      </div>
      <div className="min-w-0 max-md:flex-1">
        <MiniLevels tree={tree} current={task.id} />
      </div>
      <Link to={back} className={cx(buttonClass("secondary"), "ml-auto self-start md:self-center")}>
        <IconBack /> Back to tree
      </Link>
    </nav>
  );
}

/**
 * Task detail side panel (top of the side column) for a tree member: its tree controls (needs, dependents, start mode,
 * base, handoff, remove). The tree itself and the way back are in the TreeBar.
 */
export function TreePanel({ task, tree }: { task: Task; tree: TreeDetail }) {
  return (
    <Panel title="Tree" flush>
      <TreeNodeEditor tree={tree} task={task} />
    </Panel>
  );
}

/** Task detail side panel for a task outside any tree: says so, with Add to tree (not while archived). */
export function NoTreePanel({ task }: { task: Task }) {
  const [adding, setAdding] = useState(false);
  const archived = task.archivedAt !== null;
  return (
    <Panel
      title="Tree"
      actions={
        archived ? undefined : (
          <Button small onClick={() => setAdding(true)}>
            Add to tree
          </Button>
        )
      }
    >
      <EmptyState>
        {archived ? "Not in a tree. Unarchive it to add it to one." : "Not in a tree. Add it to one so it can need other tasks, or be needed."}
      </EmptyState>
      {adding ? <AddToTreeDrawer task={task} onClose={() => setAdding(false)} /> : null}
    </Panel>
  );
}
