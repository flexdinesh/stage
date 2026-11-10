import type { Task, Thread } from "../api/types.ts";

/** One row of the Dashboard Settled group: an app task or an external T3 thread. */
export type SettledItem = { kind: "task"; task: Task } | { kind: "thread"; thread: Thread };

const settledMs = (item: SettledItem): number => {
  const iso = item.kind === "task" ? item.task.settledAt : item.thread.settledAt;
  const ms = iso === null ? Number.NaN : new Date(iso).getTime();
  return Number.isNaN(ms) ? Number.NEGATIVE_INFINITY : ms;
};

/** Tasks and external threads interleaved, newest settle first; missing settle times sink to the end. */
export function mergeSettled(tasks: readonly Task[], threads: readonly Thread[]): SettledItem[] {
  const items: SettledItem[] = [
    ...tasks.map((task): SettledItem => ({ kind: "task", task })),
    ...threads.map((thread): SettledItem => ({ kind: "thread", thread })),
  ];
  return items.toSorted((a, b) => {
    const x = settledMs(a);
    const y = settledMs(b);
    return x === y ? 0 : y > x ? 1 : -1;
  });
}
