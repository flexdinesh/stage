import type { HarnessConnection, Task, TreeSummary } from "../api/types.ts";
import type { ProjectLoad } from "./scope.ts";
import type { StatusItem } from "./status.ts";
import { treeGroup } from "./trees.ts";

/** Task groups, in Tasks view order; the Dashboard lists five of them (`DASHBOARD_GROUPS`). */
export type GroupKey = "needs" | "done" | "ready" | "failed" | "running" | "backlog" | "scheduled" | "completed";

export const GROUP_KEYS: readonly GroupKey[] = ["needs", "done", "ready", "failed", "running", "backlog", "scheduled", "completed"];

/** The Dashboard's groups, in its order: what asks for the user, then work in motion, then ready starts. */
export const DASHBOARD_GROUPS: readonly GroupKey[] = ["needs", "done", "failed", "running", "ready"];

export const parseGroupKey = (v: string | null): GroupKey | null => GROUP_KEYS.find((k) => k === v) ?? null;

type Grouped = Pick<Task, "id" | "state" | "readiness" | "acceptanceId" | "startAt" | "completedAt">;

/**
 * Whether a task lists in a group. Needs input and running hold every such task; the rest only work not yet completed
 * in Stage. Backlog splits into ready, timed (scheduled) and the rest. Completed is the window since `since`; a task
 * completed here lingers in Awaiting review instead.
 */
export function inGroup(key: GroupKey, t: Grouped, since: string, lingering: ReadonlySet<number>): boolean {
  switch (key) {
    case "needs":
      return t.state === "needs_input";
    case "running":
      return t.state === "launching" || t.state === "running";
    case "done":
      return awaitingReview(t, lingering);
    case "completed":
      return completedSince(t, since, lingering);
  }
  if (t.acceptanceId !== null) return false;
  switch (key) {
    case "failed":
      return t.state === "failed" || t.state === "interrupted";
    case "ready":
      return t.state === "backlog" && t.readiness === "ready";
    case "backlog":
      return t.state === "backlog" && t.startAt === null && t.readiness !== "ready";
    case "scheduled":
      return t.state === "backlog" && t.startAt !== null;
  }
}

type DashboardTask = Grouped & Pick<Task, "treeId">;
type DashboardTree = Pick<TreeSummary, "status" | "counts" | "completedAt">;

/** One Dashboard group: its open trees, then its tasks outside any tree. */
export interface DashboardGroup<T extends DashboardTask, R extends DashboardTree> {
  key: GroupKey;
  trees: R[];
  tasks: T[];
}

/**
 * The Dashboard's non-empty groups in `DASHBOARD_GROUPS` order. A tree member never lists on its own: its open tree
 * lists once, under its most pressing step as on the Trees view (`treeGroup`); waiting and completed trees, and closed
 * trees with their members, stay off. Input order is kept within each list.
 */
export function dashboardGroups<T extends DashboardTask, R extends DashboardTree>(
  tasks: readonly T[],
  trees: readonly R[],
  since: string,
  lingering: ReadonlySet<number>,
): DashboardGroup<T, R>[] {
  const loose = tasks.filter((t) => t.treeId === null);
  const open = trees.filter((t) => t.status === "open");
  return DASHBOARD_GROUPS.map((key) => ({
    key,
    trees: open.filter((t) => treeGroup(t) === key),
    tasks: loose.filter((t) => inGroup(key, t, since, lingering)),
  })).filter((g) => g.trees.length + g.tasks.length > 0);
}

const rows = (g: Pick<DashboardGroup<DashboardTask, DashboardTree>, "trees" | "tasks">) => g.trees.length + g.tasks.length;

/** Rows that ask for the user: every group but Running, a tree counting once. */
export const needsYouCount = (groups: readonly DashboardGroup<DashboardTask, DashboardTree>[]) =>
  groups.reduce((n, g) => n + (g.key === "running" ? 0 : rows(g)), 0);

/** The Dashboard's rows as a sidebar load (a tree counts once; no backlog). */
export function dashboardLoad(groups: readonly DashboardGroup<DashboardTask, DashboardTree>[]): ProjectLoad {
  const load = { need: 0, waiting: 0, run: 0, open: 0 };
  for (const g of groups) {
    const n = rows(g);
    load.open += n;
    if (g.key === "needs") load.need += n;
    else if (g.key === "running") load.run += n;
    else load.waiting += n;
  }
  return load;
}

/**
 * The harness notice above the Dashboard: `connect` when no connection of any harness is set up anywhere, else none
 * (a set-up connection's problems are the status button's: dashboardProblems). None while connections load (null).
 */
export function harnessNotice(connections: readonly Pick<HarnessConnection, "active" | "configured">[] | null): "connect" | null {
  if (connections === null) return null;
  return connections.some((c) => c.active && c.configured) ? null : "connect";
}

/** The status button's problems (yellow or red items, worst first) as the Dashboard's alerts. */
export function dashboardProblems(items: readonly StatusItem[]): (StatusItem & { tone: "need" | "bad" })[] {
  return items.flatMap((i) => (i.tone === "need" || i.tone === "bad" ? [{ ...i, tone: i.tone }] : []));
}

/** Tasks view tree filter (`?tree=`): every task, or only tasks outside any tree. */
export type TreeFilter = "any" | "none";

export const parseTreeFilter = (v: string | null): TreeFilter => (v === "none" ? "none" : "any");

/** Tasks the filter keeps, input order. */
export const filterByTree = <T extends Pick<Task, "treeId">>(tasks: readonly T[], filter: TreeFilter): T[] =>
  filter === "none" ? tasks.filter((t) => t.treeId === null) : [...tasks];

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
  t.state === "done" && (t.acceptanceId === null || lingering.has(t.id));

/** Completed in the window, unless still lingering in Awaiting review. */
export const completedSince = (t: Pick<Task, "id" | "completedAt">, since: string, lingering: ReadonlySet<number>) =>
  !lingering.has(t.id) && t.completedAt !== null && t.completedAt >= since;

/** Per-device Dashboard group disclosure, kept in preference storage. */
export interface GroupFold {
  /** Groups the user collapsed, by key. */
  collapsed: string[];
  /** Needs-input episodes (`needsEpisode`) listed when Needs input was collapsed; any other one reopens it. */
  seen: string[];
}

/** Only Completed starts collapsed. */
export const DEFAULT_FOLD: GroupFold = { collapsed: ["completed"], seen: [] };

/** One needs-input episode: re-entering needs input (a new question) is a new episode. */
export const needsEpisode = (t: Pick<Task, "id" | "stateSince">) => `${t.id}:${t.stateSince ?? ""}`;

const strings = (v: unknown): string[] | null =>
  Array.isArray(v) ? v.filter((s): s is string => typeof s === "string") : null;

/** Reads a stored fold; missing or malformed storage reads as the default. */
export function parseFold(raw: string | null): GroupFold {
  if (raw === null) return DEFAULT_FOLD;
  let v: unknown;
  try {
    v = JSON.parse(raw);
  } catch {
    return DEFAULT_FOLD;
  }
  if (typeof v !== "object" || v === null) return DEFAULT_FOLD;
  const collapsed = strings("collapsed" in v ? v.collapsed : null);
  if (collapsed === null) return DEFAULT_FOLD;
  return { collapsed: collapsed.map((key) => key === "settled" ? "completed" : key), seen: strings("seen" in v ? v.seen : null) ?? [] };
}

/**
 * Whether a group renders collapsed. With `episodes` (Needs input's listed episodes), a collapsed group reopens while
 * it lists an episode not seen when it was collapsed.
 */
export const isFolded = (fold: GroupFold, key: string, episodes?: readonly string[]) =>
  fold.collapsed.includes(key) && (episodes?.every((e) => fold.seen.includes(e)) ?? true);

/** Flips a group as rendered. Collapsing with `episodes` records them as seen. */
export function toggleFold(fold: GroupFold, key: string, episodes?: readonly string[]): GroupFold {
  if (isFolded(fold, key, episodes)) return { ...fold, collapsed: fold.collapsed.filter((k) => k !== key) };
  return {
    collapsed: fold.collapsed.includes(key) ? fold.collapsed : [...fold.collapsed, key],
    seen: episodes === undefined ? fold.seen : [...episodes],
  };
}
