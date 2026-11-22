import type { Task, TreeSummary } from "../api/types.ts";

/** Days the Completed groups (Tasks and Trees) look back. */
export const SETTLED_DAYS = 7;

/** Start of the Completed window, floored to the hour so a query key stays stable between renders. */
export function settledSince(now: number = Date.now()): string {
  const d = new Date(now - SETTLED_DAYS * 24 * 3600 * 1000);
  d.setMinutes(0, 0, 0);
  return d.toISOString();
}

/** One row of the Dashboard Settled group: an app task, an external T3 thread, or a completed tree. */
export type SettledItem = { kind: "task"; task: Task } | { kind: "tree"; tree: TreeSummary };

const settledAt = (item: SettledItem): string | null =>
  item.kind === "task" ? item.task.completedAt : item.tree.settledAt;

const settledMs = (item: SettledItem): number => {
  const iso = settledAt(item);
  const ms = iso === null ? Number.NaN : new Date(iso).getTime();
  return Number.isNaN(ms) ? Number.NEGATIVE_INFINITY : ms;
};

/** Tasks, external threads and completed trees interleaved, newest settle first; missing settle times sink to the end. */
export function mergeSettled(tasks: readonly Task[], trees: readonly TreeSummary[] = []): SettledItem[] {
  const items: SettledItem[] = [
    ...tasks.map((task): SettledItem => ({ kind: "task", task })),
    ...trees.map((tree): SettledItem => ({ kind: "tree", tree })),
  ];
  return items.toSorted((a, b) => {
    const x = settledMs(a);
    const y = settledMs(b);
    return x === y ? 0 : y > x ? 1 : -1;
  });
}
