import type { ModelOption, Workspace } from "../api/types.ts";

const UNITS: [number, string][] = [
  [86_400_000, "d"],
  [3_600_000, "h"],
  [60_000, "m"],
  [1000, "s"],
];

/** Compact relative time: "5m ago", "in 3d", "now". */
export function relativeTime(iso: string | null, now: number = Date.now()): string {
  if (iso === null) return "-";
  const diff = new Date(iso).getTime() - now;
  if (Number.isNaN(diff)) return "-";
  const abs = Math.abs(diff);
  for (const [ms, unit] of UNITS) {
    if (abs >= ms) {
      const n = Math.floor(abs / ms);
      return diff < 0 ? `${n}${unit} ago` : `in ${n}${unit}`;
    }
  }
  return "now";
}

export function dateTime(iso: string | null): string {
  if (iso === null) return "-";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

export function optionsText(options: ModelOption[]): string {
  return options.map((o) => (typeof o.value === "boolean" ? `${o.id}=${o.value}` : o.value)).join(" · ");
}

/** "claude-haiku-5-5 · low", or "T3 default". */
export function modelText(model: string | null, options: ModelOption[]): string {
  const opts = optionsText(options);
  const base = model ?? "T3 default";
  return opts === "" ? base : `${base} · ${opts}`;
}

export function workspaceText(w: Workspace): string {
  if (w.type === "root") return "project root";
  return `worktree from ${w.baseRef}${w.branch === null ? "" : ` → ${w.branch}`}`;
}

/** "a, b , ,c" → ["a","b","c"] (deduped). */
export function parseTags(text: string): string[] {
  return [...new Set(text.split(",").map((t) => t.trim()).filter((t) => t !== ""))];
}

/** Local midnight `daysAgo` days before today, as an ISO datetime. */
export function localMidnight(daysAgo: number): string {
  const d = new Date();
  d.setHours(0, 0, 0, 0);
  d.setDate(d.getDate() - daysAgo);
  return d.toISOString();
}

/** Local midnight of a `YYYY-MM-DD` date input value (+ `addDays`), as an ISO datetime. */
export function localDateToIso(date: string, addDays = 0): string | undefined {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date);
  if (m === null) return undefined;
  const d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]) + addDays);
  return d.toISOString();
}

/** Narrows a string (e.g. a select value) to a member of `list` without a type assertion. */
export function pick<T extends string>(list: readonly T[], value: string): T | undefined {
  return list.find((item) => item === value);
}
