import type { Snippet } from "../api/types.ts";

/** Snippets visible in a project: global ones plus that project's (global only when `projectId` is null). */
export function snippetsFor(list: readonly Snippet[], projectId: number | null): Snippet[] {
  return list.filter((s) => s.projectId === null || s.projectId === projectId);
}

export interface Trigger {
  /** Index of the token's first char (`/` or `t` of `template:`). */
  start: number;
  /** Caret index (token end). */
  end: number;
  query: string;
}

const LITERAL = "template";

/**
 * The snippet trigger the caret sits at the end of, if any: a whitespace-delimited token starting with `/`
 * (`/rev`, `/template`) or `template:`. The caret must close the token, so editing inside `/home/x` never triggers.
 */
export function findTrigger(value: string, caret: number): Trigger | null {
  const next = value.charAt(caret);
  if (next !== "" && !/\s/.test(next)) return null;
  let start = caret;
  while (start > 0 && !/\s/.test(value.charAt(start - 1))) start--;
  const token = value.slice(start, caret);
  if (token.startsWith("/")) return { start, end: caret, query: token.slice(1) };
  if (token.toLowerCase().startsWith(`${LITERAL}:`)) return { start, end: caret, query: token.slice(LITERAL.length + 1) };
  return null;
}

const norm = (s: string) => s.toLowerCase().replace(/[\s-]+/g, "");

function rank(s: Snippet, q: string): number | null {
  if (q === "") return 0;
  const name = norm(s.name);
  if (name.startsWith(q)) return 0;
  if (name.includes(q)) return 1;
  if (s.body.toLowerCase().includes(q)) return 2;
  return null;
}

function ranked(list: readonly Snippet[], q: string): Snippet[] {
  return list
    .map((s) => ({ s, r: rank(s, q) }))
    .filter((x): x is { s: Snippet; r: number } => x.r !== null)
    .toSorted((a, b) => a.r - b.r)
    .map((x) => x.s);
}

export const MAX_MATCHES = 8;

/**
 * Snippets matching a trigger query: name prefix, then name substring, then body substring (case-insensitive,
 * spaces and hyphens ignored in names). `/template` also lists by what follows it, so `/template` alone shows all.
 */
export function matchSnippets(list: readonly Snippet[], query: string): Snippet[] {
  const q = norm(query);
  const out = ranked(list, q);
  if (q.startsWith(LITERAL)) {
    const rest = q.slice(LITERAL.length).replace(/^:/, "");
    for (const s of ranked(list, rest)) if (!out.includes(s)) out.push(s);
  }
  return out.slice(0, MAX_MATCHES);
}
