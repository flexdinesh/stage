import type { Project, Task, TaskState } from "../api/types.ts";

/**
 * Open work the Dashboard, Tasks view and sidebar read: every state a group lists. Settled tasks are kept for review
 * (settled in the harness, not yet completed in Stage) and the Completed window.
 */
export const LIVE_STATES: readonly TaskState[] = [
  "needs_input",
  "done",
  "settled",
  "backlog",
  "launching",
  "running",
  "failed",
  "interrupted",
];

/** What a task asks of the user (the Dashboard groups), or that it runs; null when neither. */
export type Attention = "need" | "review" | "ready" | "failed" | "run";

export function attentionOf(t: Pick<Task, "state" | "readiness" | "acceptanceId">): Attention | null {
  if (t.state === "needs_input") return "need";
  if (t.state === "launching" || t.state === "running") return "run";
  if (t.acceptanceId !== null) return null;
  if (t.state === "done" || t.state === "settled") return "review";
  if (t.state === "failed" || t.state === "interrupted") return "failed";
  if (t.state === "backlog" && t.readiness === "ready") return "ready";
  return null;
}

/** One project's open work, for its sidebar row. */
export interface ProjectLoad {
  /** Needs input. */
  need: number;
  /** Awaiting review, ready or failed: the rest of what the Dashboard asks of the user. */
  waiting: number;
  /** Launching or running. */
  run: number;
  /** Anything not yet completed: the above plus backlog. */
  open: number;
}

export const EMPTY_LOAD: ProjectLoad = { need: 0, waiting: 0, run: 0, open: 0 };

/** Open work per project id. */
export function projectLoads(tasks: readonly Task[]): Map<number, ProjectLoad> {
  const out = new Map<number, ProjectLoad>();
  for (const t of tasks) {
    const kind = attentionOf(t);
    const backlog = t.state === "backlog" && t.acceptanceId === null;
    if (kind === null && !backlog) continue;
    const load = { ...(out.get(t.projectId) ?? EMPTY_LOAD) };
    load.open += 1;
    if (kind === "need") load.need += 1;
    else if (kind === "run") load.run += 1;
    else if (kind !== null) load.waiting += 1;
    out.set(t.projectId, load);
  }
  return out;
}

/** Sums loads (the All projects row). */
export function totalLoad(loads: Iterable<ProjectLoad>): ProjectLoad {
  const sum = { ...EMPTY_LOAD };
  for (const l of loads) {
    sum.need += l.need;
    sum.waiting += l.waiting;
    sum.run += l.run;
    sum.open += l.open;
  }
  return sum;
}

/** A sidebar row's mark: the loudest state's square (need over run) and what the user owes (need plus waiting). */
export function loadMark(load: ProjectLoad): { square: "need" | "run" | null; owed: number } {
  return {
    square: load.need > 0 ? "need" : load.run > 0 ? "run" : null,
    owed: load.need + load.waiting,
  };
}

/** The sidebar's project lists. */
export interface SidebarProjects {
  pinned: Project[];
  /** Not pinned, with open work (or the selected scope). */
  active: Project[];
  /** Not pinned, nothing open: folded away. */
  quiet: Project[];
}

const activity = (p: Project) => (p.lastActivityAt === null ? 0 : new Date(p.lastActivityAt).getTime());

/** Most recent activity first; never-active projects last, by title. */
export const byActivity = (a: Project, b: Project) => activity(b) - activity(a) || a.title.localeCompare(b.title);

export function sidebarProjects(
  projects: readonly Project[],
  loads: ReadonlyMap<number, ProjectLoad>,
  pinned: readonly number[],
  selected: number | null,
): SidebarProjects {
  const out: SidebarProjects = { pinned: [], active: [], quiet: [] };
  for (const p of projects.toSorted(byActivity)) {
    if (pinned.includes(p.id)) out.pinned.push(p);
    else if ((loads.get(p.id)?.open ?? 0) > 0 || p.id === selected) out.active.push(p);
    else out.quiet.push(p);
  }
  return out;
}

const ids = (v: unknown): number[] | null =>
  Array.isArray(v) ? v.filter((n): n is number => Number.isSafeInteger(n) && n > 0) : null;

/** Stored scope: a project id, or null (All projects) when missing or malformed. */
export function parseScope(raw: string | null): number | null {
  if (raw === null) return null;
  const n = Number(raw);
  return Number.isSafeInteger(n) && n > 0 ? n : null;
}

/** Stored pinned project ids; missing or malformed storage reads as none. */
export function parsePinned(raw: string | null): number[] {
  if (raw === null) return [];
  try {
    return ids(JSON.parse(raw)) ?? [];
  } catch {
    return [];
  }
}

export const togglePin = (pinned: readonly number[], id: number): number[] =>
  pinned.includes(id) ? pinned.filter((p) => p !== id) : [...pinned, id];
