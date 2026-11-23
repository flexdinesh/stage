// The sidebar status: one kind, one tone and one sentence over everything Stage depends on: every set-up harness
// connection on every machine (lib/connections.ts connectionStatusItem). The button shows the kind as an icon; the details panel lists the items.

export type StatusTone = "ok" | "need" | "bad" | "idle";

export interface StatusItem {
  key: string;
  /** Panel section ("Harnesses"). */
  group: string;
  name: string;
  tone: StatusTone;
  /** Short state ("connected", "expired"). */
  state: string;
  /** Sentence naming the item and its state ("T3 pairing expired"); the row's text when it is the only problem. */
  label: string;
}

/** What the status button draws: all healthy, a problem in its tone, still checking, or nothing set up. */
export type StatusKind = "ok" | "need" | "bad" | "checking" | "empty";

export interface StatusRow {
  kind: StatusKind;
  tone: StatusTone;
  text: string;
  /** Problems first (worst first), otherwise in the given order. */
  items: StatusItem[];
  problems: number;
}

const RANK: Record<StatusTone, number> = { ok: 0, idle: 1, need: 2, bad: 3 };
const isProblem = (i: StatusItem) => i.tone === "need" || i.tone === "bad";

/** `checking`: the item list itself is still loading. `empty`: the text with nothing set up. */
export function statusRow(items: readonly StatusItem[], opts: { checking: boolean; empty: string }): StatusRow {
  const sorted = items
    .map((item, i) => ({ item, i }))
    .toSorted((a, b) => RANK[b.item.tone] - RANK[a.item.tone] || a.i - b.i)
    .map(({ item }) => item);
  const problems = sorted.filter(isProblem);
  const base = { items: sorted, problems: problems.length };
  const first = problems[0];
  if (first !== undefined) {
    const text = problems.length === 1 ? first.label : `${problems.length} need attention`;
    return { ...base, kind: first.tone === "bad" ? "bad" : "need", tone: first.tone, text };
  }
  if (sorted.length === 0) {
    return opts.checking ? { ...base, kind: "checking", tone: "idle", text: "Checking" } : { ...base, kind: "empty", tone: "idle", text: opts.empty };
  }
  if (opts.checking || sorted.some((i) => i.tone === "idle")) return { ...base, kind: "checking", tone: "idle", text: "Checking" };
  return { ...base, kind: "ok", tone: "ok", text: "All connected" };
}

/** Groups in first-seen order, for the panel's sections. */
export function statusGroups(items: readonly StatusItem[]): { group: string; items: StatusItem[] }[] {
  const groups = new Map<string, StatusItem[]>();
  for (const item of items) groups.set(item.group, [...(groups.get(item.group) ?? []), item]);
  return [...groups].map(([group, list]) => ({ group, items: list }));
}
