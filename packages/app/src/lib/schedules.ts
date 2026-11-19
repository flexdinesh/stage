// Repeat presets <-> 5-field cron, summaries, and wall-time helpers for IANA zones.
// Pure: no React, no fetch (tests/schedules.test.mjs).

import type { TaskState } from "../api/types.ts";

export const REPEAT_PRESETS = ["hourly", "daily", "weekdays", "weekly", "cron"] as const;
export type RepeatPreset = (typeof REPEAT_PRESETS)[number];

/** Form state of a repeat: preset fields plus the raw expression (used by "cron"). */
export interface RepeatSpec {
  preset: RepeatPreset;
  /** 0-59. */
  minute: number;
  /** 0-23 (ignored by hourly). */
  hour: number;
  /** 0 Sunday .. 6 Saturday (weekly only). */
  weekday: number;
  /** Raw expression (cron preset). */
  cron: string;
}

export const WEEKDAYS = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"] as const;

export const DEFAULT_REPEAT: RepeatSpec = { preset: "daily", minute: 0, hour: 9, weekday: 1, cron: "0 9 * * *" };

const pad = (n: number) => String(n).padStart(2, "0");

/** The cron expression a spec saves as. */
export function buildCron(spec: RepeatSpec): string {
  switch (spec.preset) {
    case "hourly":
      return `${spec.minute} * * * *`;
    case "daily":
      return `${spec.minute} ${spec.hour} * * *`;
    case "weekdays":
      return `${spec.minute} ${spec.hour} * * 1-5`;
    case "weekly":
      return `${spec.minute} ${spec.hour} * * ${spec.weekday}`;
    case "cron":
      return spec.cron.trim().replace(/\s+/g, " ");
  }
}

function int(field: string, max: number): number | null {
  if (!/^\d{1,2}$/.test(field)) return null;
  const n = Number(field);
  return n <= max ? n : null;
}

/** Reads an expression back into the preset it matches, else the raw "cron" preset. */
export function parseCron(expr: string): RepeatSpec {
  const cron = expr.trim().replace(/\s+/g, " ");
  const raw: RepeatSpec = { ...DEFAULT_REPEAT, preset: "cron", cron };
  const f = cron.split(" ");
  if (f.length !== 5) return raw;
  const [mi = "", h = "", dom = "", mon = "", dow = ""] = f;
  const minute = int(mi, 59);
  if (minute === null || dom !== "*" || mon !== "*") return raw;
  if (h === "*" && dow === "*") return { ...raw, preset: "hourly", minute };
  const hour = int(h, 23);
  if (hour === null) return raw;
  if (dow === "*") return { ...raw, preset: "daily", minute, hour };
  if (dow === "1-5") return { ...raw, preset: "weekdays", minute, hour };
  const day = int(dow, 7);
  if (day === null) return raw;
  return { ...raw, preset: "weekly", minute, hour, weekday: day % 7 };
}

/** "every day at 02:00", "weekdays at 09:30", "every hour at :15", "Mondays at 02:00", "cron 0 *\/6 * * *". */
export function repeatSummary(spec: RepeatSpec): string {
  const at = `${pad(spec.hour)}:${pad(spec.minute)}`;
  switch (spec.preset) {
    case "hourly":
      return `every hour at :${pad(spec.minute)}`;
    case "daily":
      return `every day at ${at}`;
    case "weekdays":
      return `weekdays at ${at}`;
    case "weekly":
      return `${WEEKDAYS[spec.weekday] ?? "Sunday"}s at ${at}`;
    case "cron":
      return `cron ${buildCron(spec)}`;
  }
}

/** Summary straight from a stored expression. */
export const cronSummary = (expr: string) => repeatSummary(parseCron(expr));

// ---- Zones and wall time ----

export function browserTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone;
  } catch {
    return "UTC";
  }
}

/** IANA zones for a picker; always contains UTC and `include`. */
export function timeZones(include: string): string[] {
  let list: string[] = [];
  try {
    list = Intl.supportedValuesOf("timeZone");
  } catch {
    list = [];
  }
  const out = new Set(list);
  out.add("UTC");
  if (include !== "") out.add(include);
  return [...out].toSorted();
}

export function validTimeZone(tz: string): boolean {
  if (tz === "") return false;
  try {
    return Intl.DateTimeFormat("en-US", { timeZone: tz }).resolvedOptions().timeZone !== "";
  } catch {
    return false;
  }
}

interface WallParts {
  year: number;
  month: number;
  day: number;
  hour: number;
  minute: number;
  second: number;
}

const partFormats = new Map<string, Intl.DateTimeFormat>();

function wallParts(ms: number, tz: string): WallParts {
  let fmt = partFormats.get(tz);
  if (fmt === undefined) {
    fmt = new Intl.DateTimeFormat("en-US", {
      timeZone: tz,
      hourCycle: "h23",
      year: "numeric",
      month: "numeric",
      day: "numeric",
      hour: "numeric",
      minute: "numeric",
      second: "numeric",
    });
    partFormats.set(tz, fmt);
  }
  const get = (type: Intl.DateTimeFormatPartTypes) => Number(fmt.formatToParts(ms).find((p) => p.type === type)?.value ?? 0);
  return { year: get("year"), month: get("month"), day: get("day"), hour: get("hour") % 24, minute: get("minute"), second: get("second") };
}

const localString = (p: WallParts) => `${p.year}-${pad(p.month)}-${pad(p.day)}T${pad(p.hour)}:${pad(p.minute)}`;

/** An instant as wall time "YYYY-MM-DDTHH:mm" in tz (datetime-local value). */
export function toLocalInput(iso: string, tz: string): string {
  return localString(wallParts(new Date(iso).getTime(), tz));
}

/** Default one-shot start: the next full hour of wall time in tz, as "YYYY-MM-DDTHH:mm". */
export function defaultStartLocal(tz: string, now: number = Date.now()): string {
  const p = wallParts(now, tz);
  const ms = now + ((60 - p.minute) * 60 - p.second) * 1000;
  return localString(wallParts(ms, tz));
}

const LOCAL_RE = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/;

/** Wall time "YYYY-MM-DDTHH:mm" in tz to an instant (ms); null when unparseable. Gaps resolve forward. */
export function localToInstant(local: string, tz: string): number | null {
  const m = LOCAL_RE.exec(local);
  if (m === null) return null;
  const wall = Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3]), Number(m[4]), Number(m[5]));
  if (Number.isNaN(wall)) return null;
  const offset = (ms: number) => {
    const p = wallParts(ms, tz);
    return Date.UTC(p.year, p.month - 1, p.day, p.hour, p.minute, p.second) - ms;
  };
  let guess = wall - offset(wall);
  guess = wall - offset(guess);
  return guess;
}

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

function zoneAbbr(ms: number, tz: string): string {
  try {
    const part = new Intl.DateTimeFormat("en-US", { timeZone: tz, timeZoneName: "short" })
      .formatToParts(ms)
      .find((p) => p.type === "timeZoneName");
    return part?.value ?? tz;
  } catch {
    return tz;
  }
}

/** "Nov 12 02:00" in tz, or "02:00" when it falls on today's date in tz (`short`). */
export function wallTime(iso: string, tz: string, opts: { short?: boolean; zone?: boolean; now?: number } = {}): string {
  const ms = new Date(iso).getTime();
  if (Number.isNaN(ms)) return "-";
  const p = wallParts(ms, tz);
  const n = wallParts(opts.now ?? Date.now(), tz);
  const today = p.year === n.year && p.month === n.month && p.day === n.day;
  const time = `${pad(p.hour)}:${pad(p.minute)}`;
  const date = opts.short === true && today ? time : `${MONTHS[p.month - 1] ?? ""} ${p.day} ${time}`;
  return opts.zone === true ? `${date} ${zoneAbbr(ms, tz)}` : date;
}

/** Compact time until `ms`: "9h 12m", "3d 4h", "45s", "now". */
export function until(ms: number, now: number = Date.now()): string {
  const s = Math.floor((ms - now) / 1000);
  if (s <= 0) return "now";
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return m % 60 === 0 ? `${h}h` : `${h}h ${m % 60}m`;
  const d = Math.floor(h / 24);
  return h % 24 === 0 ? `${d}d` : `${d}d ${h % 24}h`;
}

/**
 * How a schedule row shows its last spawned task. "quiet": a healthy or
 * finished run, plain meta text. "flag": a state chip, because the run failed
 * or keeps the next fire from spawning (`skip`, mirroring the server's skip
 * rule for launching/running/needs input; backlog is not claimed since only a
 * held or timed backlog task blocks).
 */
export type LastRun = { kind: "quiet"; word: string } | { kind: "flag"; skip: string | null };

export function lastRun(taskId: number, state: TaskState): LastRun {
  switch (state) {
    case "launching":
    case "running":
      return { kind: "flag", skip: `skips while #${taskId} runs` };
    case "needs_input":
      return { kind: "flag", skip: `skips until #${taskId} is answered` };
    case "failed":
    case "interrupted":
      return { kind: "flag", skip: null };
    case "done":
      return { kind: "quiet", word: "finished" };
    case "backlog":
      return { kind: "quiet", word: "pending" };
    case "settled":
    case "cancelled":
      return { kind: "quiet", word: state };
  }
}
