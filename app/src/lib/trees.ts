import type { StartMode, Task, TreeCounts, TreeDetail, TreeMemberRequest, TreeSummary } from "../api/types.ts";
import { age, relativeTime, shortDate } from "./format.ts";

/** What layout and graph helpers need of a member. */
type Node = Pick<Task, "id" | "needs">;

/** Needs restricted to members of `nodes` (ids outside the set are ignored). */
function needsWithin<T extends Node>(nodes: readonly T[]): Map<number, number[]> {
  const ids = new Set(nodes.map((n) => n.id));
  return new Map(nodes.map((n) => [n.id, n.needs.filter((id) => ids.has(id) && id !== n.id)]));
}

/** Level per id: 0 for roots, else 1 + the deepest need (longest path from a root). Cycle-safe. */
function levelsById<T extends Node>(nodes: readonly T[]): Map<number, number> {
  const needs = needsWithin(nodes);
  const level = new Map<number, number>();
  const visiting = new Set<number>();
  const visit = (id: number): number => {
    const known = level.get(id);
    if (known !== undefined) return known;
    if (visiting.has(id)) return 0; // a loop (never valid server-side): stop descending
    visiting.add(id);
    let deepest = -1;
    for (const need of needs.get(id) ?? []) deepest = Math.max(deepest, visit(need));
    visiting.delete(id);
    level.set(id, deepest + 1);
    return deepest + 1;
  };
  for (const n of nodes) visit(n.id);
  return level;
}

/**
 * Member ids per level, roots first. A task's level is its longest need path from a root. Roots order by id; each
 * later band orders by the mean position of its needs in their own bands (so dependents sit under what they need),
 * ties by id.
 */
export function layoutLevels<T extends Node>(nodes: readonly T[]): number[][] {
  const level = levelsById(nodes);
  const needs = needsWithin(nodes);
  const bands: number[][] = [];
  for (const n of nodes) {
    const l = level.get(n.id) ?? 0;
    while (bands.length <= l) bands.push([]);
    bands[l]?.push(n.id);
  }
  const position = new Map<number, number>();
  const mean = (id: number): number => {
    const at = (needs.get(id) ?? []).map((need) => position.get(need)).filter((p) => p !== undefined);
    return at.length === 0 ? 0 : at.reduce((a, b) => a + b, 0) / at.length;
  };
  return bands.map((band, i) => {
    const keyed = band.map((id) => ({ id, key: i === 0 ? 0 : mean(id) }));
    const ordered = keyed.toSorted((a, b) => a.key - b.key || a.id - b.id).map((k) => k.id);
    ordered.forEach((id, p) => position.set(id, p));
    return ordered;
  });
}

/** True when `taskId` needing `needId` would close a loop: needId is the task or already (transitively) needs it. */
export function wouldCycle<T extends Node>(nodes: readonly T[], taskId: number, needId: number): boolean {
  if (taskId === needId) return true;
  const needs = needsWithin(nodes);
  const seen = new Set<number>();
  const stack = [needId];
  for (let id = stack.pop(); id !== undefined; id = stack.pop()) {
    if (id === taskId) return true;
    if (seen.has(id)) continue;
    seen.add(id);
    stack.push(...(needs.get(id) ?? []));
  }
  return false;
}

/** Members `taskId` may add as a need: not itself, not already a need, and not one that would loop. By id. */
export function needCandidates<T extends Node>(nodes: readonly T[], taskId: number): T[] {
  const task = nodes.find((n) => n.id === taskId);
  if (task === undefined) return [];
  return nodes
    .filter((n) => n.id !== taskId && !task.needs.includes(n.id) && !wouldCycle(nodes, taskId, n.id))
    .toSorted((a, b) => a.id - b.id);
}

/** Direct needs and direct dependents of `id` within the tree (not `id` itself). */
export function related<T extends Node>(nodes: readonly T[], id: number): Set<number> {
  const out = new Set<number>();
  const needs = needsWithin(nodes);
  for (const need of needs.get(id) ?? []) out.add(need);
  for (const [other, list] of needs) if (list.includes(id)) out.add(other);
  return out;
}

// ---- Roll-up ----

/** What a member needs to roll up. */
type Member = Pick<Task, "state" | "readiness">;

/** Member counts by state; blocked and ready split backlog (same shape the server returns on tree lists). */
export function countMembers(members: readonly Member[]): TreeCounts {
  const c: TreeCounts = {
    total: members.length,
    backlog: 0,
    blocked: 0,
    ready: 0,
    launching: 0,
    running: 0,
    needsInput: 0,
    done: 0,
    settled: 0,
    failed: 0,
    interrupted: 0,
    cancelled: 0,
  };
  for (const m of members) {
    if (m.state === "needs_input") c.needsInput++;
    else c[m.state]++;
    if (m.state === "backlog" && m.readiness === "blocked") c.blocked++;
    if (m.state === "backlog" && m.readiness === "ready") c.ready++;
  }
  return c;
}

/** One stretch of the state progress bar. `hollow` stretches have not started (blocked or waiting backlog). */
export interface Segment {
  key: "settled" | "done" | "running" | "needs_input" | "failed" | "ready" | "waiting" | "cancelled";
  label: string;
  tone: "ok" | "done" | "run" | "need" | "bad" | "idle";
  count: number;
  hollow: boolean;
}

/** The tree's most pressing next step: answer > start > retry > settle > running > waiting > done. */
export type NextAction =
  | { kind: "answer"; count: number }
  | { kind: "start"; count: number }
  | { kind: "retry"; count: number }
  | { kind: "settle"; count: number }
  | { kind: "running"; count: number }
  | { kind: "waiting"; count: number }
  | { kind: "done"; count: number };

export interface Rollup {
  counts: TreeCounts;
  /** Non-empty stretches, finished work first. */
  segments: Segment[];
  next: NextAction;
}

export function treeRollup(counts: TreeCounts): Rollup {
  const failed = counts.failed + counts.interrupted;
  const running = counts.running + counts.launching;
  const waiting = counts.backlog - counts.ready;
  const all: Segment[] = [
    { key: "settled", label: "settled", tone: "ok", count: counts.settled, hollow: false },
    { key: "done", label: "done", tone: "done", count: counts.done, hollow: false },
    { key: "running", label: "running", tone: "run", count: running, hollow: false },
    { key: "needs_input", label: "needs input", tone: "need", count: counts.needsInput, hollow: false },
    { key: "failed", label: "failed", tone: "bad", count: failed, hollow: false },
    { key: "ready", label: "ready", tone: "idle", count: counts.ready, hollow: false },
    { key: "waiting", label: counts.blocked > 0 ? "blocked" : "backlog", tone: "idle", count: waiting, hollow: true },
    { key: "cancelled", label: "cancelled", tone: "idle", count: counts.cancelled, hollow: false },
  ];
  const next: NextAction =
    counts.needsInput > 0
      ? { kind: "answer", count: counts.needsInput }
      : counts.ready > 0
        ? { kind: "start", count: counts.ready }
        : failed > 0
          ? { kind: "retry", count: failed }
          : counts.done > 0
            ? { kind: "settle", count: counts.done }
            : running > 0
              ? { kind: "running", count: running }
              : waiting + counts.cancelled > 0
                ? { kind: "waiting", count: waiting + counts.cancelled }
                : { kind: "done", count: counts.settled };
  return { counts, segments: all.filter((s) => s.count > 0), next };
}

/** "7 tasks: 2 settled, 1 running, 4 blocked" (state bar label). */
export function rollupLabel(r: Rollup): string {
  const n = r.counts.total;
  const parts = r.segments.map((s) => `${s.count} ${s.label}`);
  return `${n} task${n === 1 ? "" : "s"}${parts.length === 0 ? "" : `: ${parts.join(", ")}`}`;
}

/** Short mono status for the next action: "1 needs input", "2 ready", "2 to settle in T3", "all settled". */
export function nextActionText(next: NextAction): string {
  switch (next.kind) {
    case "answer":
      return `${next.count} need${next.count === 1 ? "s" : ""} input`;
    case "start":
      return `${next.count} ready`;
    case "retry":
      return `${next.count} failed`;
    case "settle":
      return `${next.count} to settle in T3`;
    case "running":
      return `${next.count} running`;
    case "waiting":
      return "waiting on needs";
    case "done":
      return "all settled";
  }
}

const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? "" : "s"}`;

/** "7 tasks · 3 levels · 2 projects · auto" for a tree list row or a tree detail. */
export function treeMeta(tree: TreeSummary | TreeDetail): string {
  const shape: { tasks: number; levels: number; projects: number; mode: StartMode } =
    "tasks" in tree
      ? {
          tasks: tree.tasks.length,
          levels: tree.levels.length,
          projects: new Set(tree.tasks.map((t) => t.projectId)).size,
          mode: tree.startMode,
        }
      : { tasks: tree.counts.total, levels: tree.levels, projects: tree.projects.length, mode: tree.startMode };
  return [plural(shape.tasks, "task"), plural(shape.levels, "level"), plural(shape.projects, "project"), shape.mode].join(" · ");
}

/** Needs a task's worktree may start from: its needs in the same project (another project's branch cannot be used). */
export function baseCandidates(task: Pick<Task, "needs" | "projectId">, tree: Pick<TreeDetail, "tasks">): Task[] {
  return tree.tasks.filter((t) => task.needs.includes(t.id) && t.projectId === task.projectId).toSorted((a, b) => a.id - b.id);
}

/** A member's node label: ready / blocked for backlog members with readiness, else its task state. */
export type NodeState = Task["state"] | "ready" | "blocked";

export function nodeState(t: Pick<Task, "state" | "readiness">): NodeState {
  if (t.state === "backlog" && t.readiness !== null) return t.readiness;
  return t.state;
}

/** What inState reads of a member. */
type Timed = Pick<Task, "state" | "readiness" | "stateSince" | "readyAt" | "createdAt" | "launchedAt" | "finishedAt" | "settledAt">;

/**
 * A member's time in its current state, for tree rows: "running 18m", "waiting 6m", "done 22h ago", "settled Nov 15",
 * "ready 3h", "queued 2d". `at` is the instant it counts from (exact time on hover).
 */
export function inState(t: Timed, now: number = Date.now()): { text: string; at: string } {
  const since = (fallback: string | null) => t.stateSince ?? fallback ?? t.createdAt;
  switch (t.state) {
    case "settled": {
      const at = t.settledAt ?? since(null);
      return { text: `settled ${shortDate(at, now)}`, at };
    }
    case "done": {
      const at = t.finishedAt ?? since(null);
      return { text: `done ${relativeTime(at, now)}`, at };
    }
    case "launching":
    case "running": {
      const at = since(t.launchedAt);
      return { text: `${t.state} ${age(at, now)}`, at };
    }
    case "needs_input": {
      const at = since(null);
      return { text: `waiting ${age(at, now)}`, at };
    }
    case "failed":
    case "interrupted":
    case "cancelled": {
      const at = since(t.finishedAt);
      return { text: `${t.state} ${relativeTime(at, now)}`, at };
    }
    case "backlog": {
      if (t.readiness === "ready") {
        const at = t.readyAt ?? since(null);
        return { text: `ready ${age(at, now)}`, at };
      }
      const at = since(null);
      return { text: `queued ${age(at, now)}`, at };
    }
  }
}

/** Every member settled. */
export const treeSettled = (tasks: readonly Pick<Task, "state">[]) => tasks.length > 0 && tasks.every((t) => t.state === "settled");

/** New tree from a selection: parallel (every task a root) or a chain (each needs the one before it). */
export type TreeShape = "parallel" | "chain";

/** Members for POST /trees: chain needs follow `order`. */
export function treeMembers(order: readonly Pick<Task, "id">[], shape: TreeShape): TreeMemberRequest[] {
  return order.map((t, i) => {
    const prev = order[i - 1];
    return shape === "chain" && prev !== undefined ? { taskId: t.id, needs: [prev.id] } : { taskId: t.id };
  });
}
