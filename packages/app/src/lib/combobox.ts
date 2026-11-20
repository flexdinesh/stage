import type { MatchRange } from "./projects.ts";

export interface ComboboxOption {
  value: string;
  label: string;
  /** Span of `label` to bold. */
  labelMatch?: MatchRange | null | undefined;
  /** Secondary mono line (e.g. a path). */
  detail?: string | undefined;
  /** Span of `detail` to bold. */
  detailMatch?: MatchRange | null | undefined;
  /** Plain chip after the label (e.g. "archived"). */
  chip?: string | undefined;
  /** Muted mono label at the row's end (e.g. a model's provider). */
  tag?: string | undefined;
  /** Span of `tag` to bold. */
  tagMatch?: MatchRange | null | undefined;
  /** Listed but not choosable (e.g. a saved value the harness no longer offers). */
  disabled?: boolean | undefined;
}

/** Lists with more options than this get a type-to-filter input; shorter ones are select-only. */
export const SEARCH_THRESHOLD = 8;

function find(text: string | undefined, q: string): MatchRange | null {
  if (text === undefined) return null;
  const start = text.toLowerCase().indexOf(q);
  return start < 0 ? null : { start, end: start + q.length };
}

/**
 * Options matching `query` (case-insensitive substring of label, tag or detail, surrounding space ignored), with the
 * matched spans. Ranked label prefix, label substring, tag, detail; ties keep the given order. `""` lists all as given.
 */
export function filterOptions(options: readonly ComboboxOption[], query: string): ComboboxOption[] {
  const q = query.trim().toLowerCase();
  if (q === "") return [...options];
  const ranked: { option: ComboboxOption; rank: number }[] = [];
  for (const o of options) {
    const label = find(o.label, q);
    const tag = find(o.tag, q);
    const detail = find(o.detail, q);
    if (label === null && tag === null && detail === null) continue;
    const rank = label !== null ? (label.start === 0 ? 0 : 1) : tag !== null ? 2 : 3;
    ranked.push({ option: { ...o, labelMatch: label, tagMatch: tag, detailMatch: detail }, rank });
  }
  return ranked.toSorted((a, b) => a.rank - b.rank).map((r) => r.option);
}

/**
 * Type-ahead for a select-only list: the first choosable option whose label starts with `typed`, searching from
 * `from`. Repeating one character cycles through the options starting with it, after `from`. -1 when none match.
 */
export function typeaheadIndex(options: readonly ComboboxOption[], typed: string, from: number): number {
  const t = typed.toLowerCase();
  if (t === "" || options.length === 0) return -1;
  const cycling = t.length > 1 && [...t].every((c) => c === t[0]);
  const prefix = cycling ? t.slice(0, 1) : t;
  const start = from < 0 ? 0 : from + (cycling || t.length === 1 ? 1 : 0);
  for (let k = 0; k < options.length; k++) {
    const i = (start + k) % options.length;
    const o = options[i];
    if (o !== undefined && o.disabled !== true && o.label.toLowerCase().startsWith(prefix)) return i;
  }
  return -1;
}

/** The next choosable index from `from` in direction `step` (wrapping); `from` when nothing else is choosable. */
export function stepIndex(options: readonly ComboboxOption[], from: number, step: 1 | -1): number {
  const n = options.length;
  for (let k = 1; k <= n; k++) {
    const i = (((from + step * k) % n) + n) % n;
    if (options[i]?.disabled !== true) return i;
  }
  return from;
}

/** The first (`"first"`) or last choosable index; -1 when none. */
export function edgeIndex(options: readonly ComboboxOption[], edge: "first" | "last"): number {
  if (edge === "first") return options.findIndex((o) => o.disabled !== true);
  return options.findLastIndex((o) => o.disabled !== true);
}
