import type { Usage } from "../api/types.ts";

/** One row of the Usage panel: a compact value, its exact form for the tooltip, and an optional breakdown (parts of one line). */
export interface UsageRow {
  label: string;
  value: string;
  exact?: string;
  detail?: string[];
}

const exactFormat = new Intl.NumberFormat("en-US");

/** A token count with a k or M suffix and one decimal (4875 is 4.9k); the CLI prints the same. */
export function compactCount(n: number): string {
  if (n < 1000) return String(n);
  if (n < 999_950) return `${trimZero((n / 1e3).toFixed(1))}k`;
  return `${trimZero((n / 1e6).toFixed(1))}M`;
}

const trimZero = (s: string) => (s.endsWith(".0") ? s.slice(0, -2) : s);

/** Time spent working, its two largest units: 23s, 4m 12s, 1h 3m. */
export function workingTime(ms: number): string {
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  return `${Math.floor(m / 60)}h ${m % 60}m`;
}

const tokens = (n: number) => `${exactFormat.format(n)} tokens`;

/**
 * The Usage panel's rows. Input counts every input token; the detail line splits out cache reads and writes.
 * Working time shows once a turn has finished; cost only when the harness reported one (Stage never prices tokens).
 */
export function usageRows(u: Usage): UsageRow[] {
  const input = u.inputTokens + u.cacheReadTokens + u.cacheWriteTokens;
  const cache = [
    u.cacheReadTokens > 0 ? `${compactCount(u.cacheReadTokens)} cache read` : "",
    u.cacheWriteTokens > 0 ? `${compactCount(u.cacheWriteTokens)} cache write` : "",
  ].filter((part) => part !== "");
  const rows: UsageRow[] = [
    { label: "Turns", value: String(u.turns) },
    { label: "Input tokens", value: compactCount(input), exact: tokens(input), ...(cache.length > 0 ? { detail: cache } : {}) },
    { label: "Output tokens", value: compactCount(u.outputTokens), exact: tokens(u.outputTokens) },
  ];
  if (u.workingMs > 0) rows.push({ label: "Working time", value: workingTime(u.workingMs) });
  if (u.costUsd !== null) rows.push({ label: "Cost", value: `$${u.costUsd.toFixed(2)}`, exact: `$${u.costUsd}` });
  return rows;
}
