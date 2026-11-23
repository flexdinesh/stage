// In-app back/forward: where this tab stands in its own history, what lies behind and ahead, and the names the history
// list shows. The router's browser history numbers its entries (`history.state.idx`, 0 for the first app page); this
// model follows navigations from that index. Kept in memory: after a reload Back still works from the index, but what
// lay ahead and the names of earlier pages are unknown until visited again.

export type NavigationType = "PUSH" | "POP" | "REPLACE";

export interface HistoryEntry {
  path: string;
  /** The page's own name (a task title), once the page reported it. */
  label: string | null;
}

export interface NavHistory {
  /** The current entry's index. */
  idx: number;
  /** The furthest entry still ahead of a Back (a push drops everything past it). */
  maxIdx: number;
  entries: ReadonlyMap<number, HistoryEntry>;
}

/** One row of the history list: how far to go (negative is back) and where. */
export interface HistoryJump {
  delta: number;
  path: string;
  label: string;
  kind: string | null;
}

/** The router's index from `history.state`; 0 when absent or malformed. */
export function historyIndex(state: unknown): number {
  if (typeof state !== "object" || state === null || !("idx" in state)) return 0;
  const idx = state.idx;
  return typeof idx === "number" && Number.isInteger(idx) && idx >= 0 ? idx : 0;
}

export function startHistory(idx: number, path: string): NavHistory {
  return { idx, maxIdx: idx, entries: new Map([[idx, { path, label: null }]]) };
}

/**
 * Follows one navigation. A push to a new page forgets every entry ahead; a pop keeps them. A push to the URL already
 * shown keeps its name: the page stays mounted and does not report it again.
 */
export function recordNavigation(h: NavHistory, idx: number, type: NavigationType, path: string): NavHistory {
  const entries = new Map(h.entries);
  if (type === "PUSH") {
    const from = h.entries.get(h.idx);
    for (const i of entries.keys()) if (i >= idx) entries.delete(i);
    entries.set(idx, { path, label: from !== undefined && from.path === path ? from.label : null });
    return { idx, maxIdx: idx, entries };
  }
  const known = entries.get(idx);
  entries.set(idx, known !== undefined && known.path === path ? known : { path, label: null });
  return { idx, maxIdx: Math.max(h.maxIdx, idx), entries };
}

/** Names the current entry; a no-op (same object) when unchanged or the entry moved on. */
export function labelCurrent(h: NavHistory, path: string, label: string): NavHistory {
  const entry = h.entries.get(h.idx);
  if (entry === undefined || entry.path !== path || entry.label === label) return h;
  const entries = new Map(h.entries);
  entries.set(h.idx, { path, label });
  return { ...h, entries };
}

/** The current page's name: the name it reported, else its route's fallback ("task 12") until it does. */
export function currentName(h: NavHistory): string | null {
  const entry = h.entries.get(h.idx);
  return entry === undefined ? null : (entry.label ?? routeLabel(entry.path));
}

/** A list page's name under a project scope ("Tasks · stage-app"); the bare page name for All projects. */
export function scopedPageName(page: string, project: string | null): string {
  return project === null ? page : `${page} · ${project}`;
}

/** The document title for a page name ("Tasks · stage"); the bare product name when there is none. */
export function documentTitle(name: string | null): string {
  return name === null ? "stage" : `${name} · stage`;
}

export const canGoBack = (h: NavHistory): boolean => h.idx > 0;
export const canGoForward = (h: NavHistory): boolean => h.idx < h.maxIdx;

const LIST_LIMIT = 10;

function jump(h: NavHistory, i: number): HistoryJump | null {
  const entry = h.entries.get(i);
  if (entry === undefined) return null;
  return { delta: i - h.idx, path: entry.path, label: entry.label ?? routeLabel(entry.path), kind: routeKind(entry.path) };
}

/** Known pages behind, nearest first (pages before a reload are unknown and left out). */
export function backList(h: NavHistory, limit = LIST_LIMIT): HistoryJump[] {
  const out: HistoryJump[] = [];
  for (let i = h.idx - 1; i >= 0 && out.length < limit; i--) {
    const j = jump(h, i);
    if (j !== null) out.push(j);
  }
  return out;
}

/** Known pages ahead, nearest first. */
export function forwardList(h: NavHistory, limit = LIST_LIMIT): HistoryJump[] {
  const out: HistoryJump[] = [];
  for (let i = h.idx + 1; i <= h.maxIdx && out.length < limit; i++) {
    const j = jump(h, i);
    if (j !== null) out.push(j);
  }
  return out;
}

const PAGES: Record<string, string> = {
  "/": "Dashboard",
  "/tasks": "Tasks",
  "/trees": "Trees",
  "/projects": "Projects",
  "/schedules": "Schedules",
  "/snippets": "Snippets",
  "/settings": "Settings",
  "/settings/connections": "Connections",
  "/settings/notifications": "Notifications",
  "/more": "More",
};

const DETAIL = /^\/(tasks|trees|projects|schedules)\/(\d+)\/?$/;
const KIND: Record<string, string> = { tasks: "task", trees: "tree", projects: "project", schedules: "schedule" };

function pathname(path: string): string {
  const end = path.search(/[?#]/);
  return end === -1 ? path : path.slice(0, end);
}

/** A detail page's kind ("task"), else null. */
export function routeKind(path: string): string | null {
  const m = DETAIL.exec(pathname(path));
  return m === null ? null : (KIND[m[1] ?? ""] ?? null);
}

/** The fallback name of an app path: "Tasks", "task 12"; the path itself when unknown. */
export function routeLabel(path: string): string {
  const p = pathname(path);
  const page = PAGES[p];
  if (page !== undefined) return page;
  const m = DETAIL.exec(p);
  const kind = m === null ? undefined : KIND[m[1] ?? ""];
  return kind === undefined || m === null ? p : `${kind} ${m[2]}`;
}
