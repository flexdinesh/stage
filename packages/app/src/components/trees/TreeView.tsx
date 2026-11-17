import { useMemo } from "react";
import type { TreeDetail } from "../../api/types.ts";
import { layoutLevels } from "../../lib/trees.ts";
import { TreeNode } from "./TreeNode.tsx";

/** Laptop columns: level, state, task, needs, in state, action (rows join the list's subgrid). */
const COLUMNS = "md:grid md:grid-cols-[44px_auto_minmax(0,1fr)_minmax(72px,auto)_auto_auto]";

const HEAD = ["L", "State", "Task", "Needs", "In state", ""];

/**
 * The tree as a graph list: one ruled row per member in level order (roots first, each level ordered by its needs),
 * a stronger rule between levels.
 */
export function TreeView({
  tree,
  selected,
  onSelect,
}: {
  tree: TreeDetail;
  selected: number | null;
  onSelect: (id: number) => void;
}) {
  const levels = useMemo(() => layoutLevels(tree.tasks), [tree.tasks]);
  const byId = useMemo(() => new Map(tree.tasks.map((t) => [t.id, t])), [tree.tasks]);

  return (
    <ul className={COLUMNS} aria-label="Tree tasks in level order">
      <li aria-hidden className="col-span-6 grid-cols-subgrid border-b border-line max-md:hidden md:grid">
        {HEAD.map((h, i) => (
          <span key={i} className={`label-caps py-1.5 text-muted ${i === 0 ? "pl-3" : "px-2"} ${i === 4 ? "text-right" : ""}`}>
            {h}
          </span>
        ))}
      </li>
      {levels.flatMap((band, l) =>
        band.map((id, i) => {
          const task = byId.get(id);
          if (task === undefined) return null;
          return (
            <TreeNode
              key={id}
              task={task}
              byId={byId}
              level={l}
              firstOfLevel={i === 0}
              endsLevel={i === band.length - 1 && l < levels.length - 1}
              selected={selected === id}
              onSelect={onSelect}
            />
          );
        }),
      )}
    </ul>
  );
}

/** Loading placeholder: pulsing rows. */
export function TreeViewSkeleton() {
  return (
    <div aria-busy="true" aria-label="Loading tree">
      {[0.6, 0.45, 0.7, 0.5, 0.4].map((w, i) => (
        <div key={i} className="flex h-[76px] items-center gap-3 border-b border-line px-3 last:border-b-0 md:h-[52px]">
          <div className="h-3 w-6 animate-pulse bg-panel" />
          <div className="h-4 w-20 animate-pulse bg-panel" />
          <div className="h-3.5 animate-pulse bg-panel" style={{ width: `${w * 60}%` }} />
        </div>
      ))}
    </div>
  );
}
