import type { Task, TreeSummary } from "../api/types.ts";

/** Days the Completed groups (Tasks and Trees) look back. */
export const COMPLETED_DAYS = 7;

/** Start of the Completed window, floored to the hour so a query key stays stable between renders. */
export function completedWindowStart(now: number = Date.now()): string {
  const d = new Date(now - COMPLETED_DAYS * 24 * 3600 * 1000);
  d.setMinutes(0, 0, 0);
  return d.toISOString();
}

/** A completed task or tree. */
export type CompletedItem = { kind: "task"; task: Task } | { kind: "tree"; tree: TreeSummary };

const completedAt = (item: CompletedItem): string | null =>
  item.kind === "task" ? item.task.completedAt : item.tree.completedAt;

const completedMs = (item: CompletedItem): number => {
  const iso = completedAt(item);
  const ms = iso === null ? Number.NaN : new Date(iso).getTime();
  return Number.isNaN(ms) ? Number.NEGATIVE_INFINITY : ms;
};

/** Tasks and trees interleaved, newest completion first. Entries without a completion time come last. */
export function mergeCompleted(tasks: readonly Task[], trees: readonly TreeSummary[] = []): CompletedItem[] {
  const items: CompletedItem[] = [
    ...tasks.map((task): CompletedItem => ({ kind: "task", task })),
    ...trees.map((tree): CompletedItem => ({ kind: "tree", tree })),
  ];
  return items.toSorted((a, b) => {
    const x = completedMs(a);
    const y = completedMs(b);
    return x === y ? 0 : y > x ? 1 : -1;
  });
}
