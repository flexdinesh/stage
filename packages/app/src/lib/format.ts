import type { ExecutionConfig, ModelOption, Workspace } from "../api/types.ts";

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

/** Compact elapsed time between two instants: "45s", "8m", "1h 12m", "2d 3h". Null when either is missing or invalid. */
export function duration(fromIso: string | null, toIso: string | null): string | null {
  if (fromIso === null || toIso === null) return null;
  const ms = new Date(toIso).getTime() - new Date(fromIso).getTime();
  if (Number.isNaN(ms) || ms < 0) return null;
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return m % 60 === 0 ? `${h}h` : `${h}h ${m % 60}m`;
  const d = Math.floor(h / 24);
  return h % 24 === 0 ? `${d}d` : `${d}d ${h % 24}h`;
}

/** Compact age of an instant, largest unit only: "45s", "18m", "3h", "2d" ("now" under a second; "-" when invalid). */
export function age(iso: string | null, now: number = Date.now()): string {
  const rel = relativeTime(iso, now);
  return rel.endsWith(" ago") ? rel.slice(0, -4) : rel.startsWith("in ") ? "now" : rel;
}

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
const WEEKDAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
const pad2 = (n: number) => String(n).padStart(2, "0");

/** Local calendar day of `ms` as days since the epoch (DST-safe day comparisons). */
const dayNumber = (d: Date) => Math.round(Date.UTC(d.getFullYear(), d.getMonth(), d.getDate()) / 86_400_000);

const validDate = (iso: string | null): Date | null => {
  if (iso === null) return null;
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? null : d;
};

/** Local 24h clock time: "14:54". */
export function clockTime(iso: string | null): string {
  const d = validDate(iso);
  return d === null ? "-" : `${pad2(d.getHours())}:${pad2(d.getMinutes())}`;
}

/** Local date anchor: "today 10:20", "yesterday 16:20", "Nov 14" (this year), "Oct 3, 2025". */
export function shortDate(iso: string | null, now: number = Date.now()): string {
  const d = validDate(iso);
  if (d === null) return "-";
  const n = new Date(now);
  const days = dayNumber(n) - dayNumber(d);
  if (days === 0) return `today ${clockTime(iso)}`;
  if (days === 1) return `yesterday ${clockTime(iso)}`;
  const md = `${MONTHS[d.getMonth()] ?? ""} ${d.getDate()}`;
  return d.getFullYear() === n.getFullYear() ? md : `${md}, ${d.getFullYear()}`;
}

/** Local day heading: "Today · Mon Nov 16", "Yesterday · Sun Nov 15", "Sat Nov 14", "Fri Oct 3, 2025". */
export function dayLabel(iso: string | null, now: number = Date.now()): string {
  const d = validDate(iso);
  if (d === null) return "-";
  const n = new Date(now);
  const base = `${WEEKDAYS[d.getDay()] ?? ""} ${MONTHS[d.getMonth()] ?? ""} ${d.getDate()}`;
  const days = dayNumber(n) - dayNumber(d);
  if (days === 0) return `Today · ${base}`;
  if (days === 1) return `Yesterday · ${base}`;
  return d.getFullYear() === n.getFullYear() ? base : `${base}, ${d.getFullYear()}`;
}

/** Items (newest first) grouped by local day, order kept; each group carries its dayLabel. */
export function groupByDay<T extends { at: string }>(items: readonly T[], now: number = Date.now()): { label: string; items: T[] }[] {
  const out: { key: number; label: string; items: T[] }[] = [];
  for (const item of items) {
    const d = validDate(item.at);
    const key = d === null ? Number.NaN : dayNumber(d);
    const last = out.at(-1);
    if (last !== undefined && last.key === key) last.items.push(item);
    else out.push({ key, label: dayLabel(item.at, now), items: [item] });
  }
  return out.map(({ label, items: list }) => ({ label, items: list }));
}

export function dateTime(iso: string | null): string {
  if (iso === null) return "-";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

export function optionsText(options: ModelOption[]): string {
  return options.map((o) => (typeof o.value === "boolean" ? `${o.id}=${o.value}` : o.value)).join(" · ");
}

/** "claude-haiku-5-5 · low", or "Harness default". */
export function modelText(model: string | null, options: ModelOption[]): string {
  const opts = optionsText(options);
  const base = model ?? "Harness default";
  return opts === "" ? base : `${base} · ${opts}`;
}

/** A model key's display name: a composite key (a JSON array such as ["provider","model"]) shows its last part. */
export function modelName(key: string): string | null {
  if (key === "") return null;
  if (!key.startsWith("[")) return key;
  try {
    const parts: unknown = JSON.parse(key);
    if (Array.isArray(parts)) {
      const last: unknown = parts[parts.length - 1];
      if (typeof last === "string" && last !== "") return last;
    }
  } catch {
    // Not a composite key: show it as is.
  }
  return key;
}

/** The model and options a launch uses ("" model = harness default). */
export function configModelText(c: ExecutionConfig | null | undefined): string {
  return modelText(modelName(c?.model ?? ""), c?.options ?? []);
}

/** Permission and mode a launch uses ("" = harness default). */
export function executionModes(x: { executionConfig?: ExecutionConfig | null }): { permission: string; mode: string } {
  return { permission: x.executionConfig?.permission ?? "", mode: x.executionConfig?.mode ?? "" };
}

export function workspaceText(w: Workspace): string {
  if (w.type === "root") return "project root";
  if (!w.baseRef) return "harness-managed worktree";
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

/** A badge count, capped so it fits its slot: 99 and below as is, then "99+". */
export function capCount(n: number): string {
  return n > 99 ? "99+" : String(n);
}

/** Narrows a string (e.g. a select value) to a member of `list` without a type assertion. */
export function pick<T extends string>(list: readonly T[], value: string): T | undefined {
  return list.find((item) => item === value);
}
