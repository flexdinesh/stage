import type { Task, Thread, TreeSummary } from "../api/types.ts";

/** One row of the Dashboard Settled group: an app task, an external T3 thread, or a completed tree. */
export type SettledItem = { kind: "task"; task: Task } | { kind: "thread"; thread: Thread } | { kind: "tree"; tree: TreeSummary };

const settledAt = (item: SettledItem): string | null =>
  item.kind === "task" ? item.task.settledAt : item.kind === "thread" ? item.thread.settledAt : item.tree.settledAt;

const settledMs = (item: SettledItem): number => {
  const iso = settledAt(item);
  const ms = iso === null ? Number.NaN : new Date(iso).getTime();
  return Number.isNaN(ms) ? Number.NEGATIVE_INFINITY : ms;
};

/** Tasks, external threads and completed trees interleaved, newest settle first; missing settle times sink to the end. */
export function mergeSettled(tasks: readonly Task[], threads: readonly Thread[], trees: readonly TreeSummary[] = []): SettledItem[] {
  const items: SettledItem[] = [
    ...tasks.map((task): SettledItem => ({ kind: "task", task })),
    ...threads.map((thread): SettledItem => ({ kind: "thread", thread })),
    ...trees.map((tree): SettledItem => ({ kind: "tree", tree })),
  ];
  return items.toSorted((a, b) => {
    const x = settledMs(a);
    const y = settledMs(b);
    return x === y ? 0 : y > x ? 1 : -1;
  });
}
