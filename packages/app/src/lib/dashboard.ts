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
export const breaksOut = (t: Pick<Task, "state" | "readiness" | "acceptanceId">) => t.state === "needs_input" || (t.acceptanceId === null && (t.state === "done" || t.state === "settled")) || (t.acceptanceId !== null && (t.state === "running" || t.state === "launching")) || t.readiness === "ready";

/**
 * Splits Dashboard tasks against the listed trees. A member of a listed tree rolls up into that tree's row unless it
 * needs input or is ready (those keep their own row, with a tree chip). A settled member rolls up only when its whole
 * tree is complete; settled members of an active tree stay in Settled. Members of trees not listed keep their rows.
 * Done and archived trees are left out (their members are closed and not listed either). Ids in `keep` (tasks just
 * completed here, still lingering in Awaiting review) always keep their own row.
 */
export function splitTreeMembers(
  tasks: readonly Task[],
  trees: readonly TreeSummary[],
  keep: ReadonlySet<number> = new Set(),
): TreeSplit {
  const open = trees.filter((t) => t.status === "open");
  const active = open.filter((t) => t.settledAt === null);
  const settled = open.filter((t) => t.settledAt !== null);
  const activeIds = new Set(active.map((t) => t.id));
  const settledIds = new Set(settled.map((t) => t.id));
  const rows: Task[] = [];
  const rolledUp: Task[] = [];
  for (const t of tasks) {
    const tree = t.treeId;
    const rolls =
      tree !== null &&
      !keep.has(t.id) &&
      !breaksOut(t) &&
      (t.acceptanceId !== null ? settledIds.has(tree) : activeIds.has(tree) || settledIds.has(tree));
    (rolls ? rolledUp : rows).push(t);
  }
  return { active, settled, rows, rolledUp };
}

/** How long a task completed from the Dashboard keeps its Awaiting review row (with Undo). */
export const LINGER_MS = 6000;

/** Task id -> epoch ms its linger ends. */
export type Linger = ReadonlyMap<number, number>;

export const startLinger = (linger: Linger, id: number, now: number): Linger => new Map(linger).set(id, now + LINGER_MS);

export function endLinger(linger: Linger, id: number): Linger {
  const next = new Map(linger);
  next.delete(id);
  return next;
}

/** Drops ended lingers; returns the same map when none ended. */
export function pruneLinger(linger: Linger, now: number): Linger {
  const live = [...linger].filter(([, until]) => until > now);
  return live.length === linger.size ? linger : new Map(live);
}

/** When the next linger ends; null when none is running. */
export const nextLingerEnd = (linger: Linger): number | null => (linger.size === 0 ? null : Math.min(...linger.values()));

/** Awaiting review: a finished run without a current acceptance, or one completed here that still lingers. */
export const awaitingReview = (t: Pick<Task, "id" | "state" | "acceptanceId">, lingering: ReadonlySet<number>) =>
  (t.state === "done" || t.state === "settled") && (t.acceptanceId === null || lingering.has(t.id));

/** Completed in the window, unless still lingering in Awaiting review. */
export const completedSince = (t: Pick<Task, "id" | "completedAt">, since: string, lingering: ReadonlySet<number>) =>
  !lingering.has(t.id) && t.completedAt !== null && t.completedAt >= since;
