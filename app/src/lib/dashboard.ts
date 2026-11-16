import type { Task, TreeSummary } from "../api/types.ts";

/** How the Dashboard shows tree members: rolled up into tree rows, or on their own row. */
export interface TreeSplit {
  /** Trees with unsettled work (the Trees group), in list order. */
  active: TreeSummary[];
  /** Completed trees (every member settled), shown as rows in Settled. */
  settled: TreeSummary[];
  /** Tasks the state groups list on their own row, input order. */
  rows: Task[];
  /** Tree members represented by their tree row instead (still counted in the stat strip). */
  rolledUp: Task[];
}

/** A member that also breaks out of its tree row: it waits on the user (needs input) or on a start (ready). */
export const breaksOut = (t: Pick<Task, "state" | "readiness">) => t.state === "needs_input" || t.readiness === "ready";

/**
 * Splits Dashboard tasks against the listed trees. A member of a listed tree rolls up into that tree's row unless it
 * needs input or is ready (those keep their own row, with a tree chip). A settled member rolls up only when its whole
 * tree is complete; settled members of an active tree stay in Settled. Members of trees not listed keep their rows.
 */
export function splitTreeMembers(tasks: readonly Task[], trees: readonly TreeSummary[]): TreeSplit {
  const active = trees.filter((t) => t.settledAt === null);
  const settled = trees.filter((t) => t.settledAt !== null);
  const activeIds = new Set(active.map((t) => t.id));
  const settledIds = new Set(settled.map((t) => t.id));
  const rows: Task[] = [];
  const rolledUp: Task[] = [];
  for (const t of tasks) {
    const tree = t.treeId;
    const rolls =
      tree !== null &&
      !breaksOut(t) &&
      (t.state === "settled" ? settledIds.has(tree) : activeIds.has(tree) || settledIds.has(tree));
    (rolls ? rolledUp : rows).push(t);
  }
  return { active, settled, rows, rolledUp };
}
