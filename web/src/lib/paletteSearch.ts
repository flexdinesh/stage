import type { MatchSource, T3SearchStatus, Thread, ThreadSearchResult } from "../api/types.ts";

/** One session row in the Ctrl K palette. */
export interface SessionHit {
  id: string;
  title: string;
  projectTitle: string | null;
  source: MatchSource;
  /** Message text around the match, whitespace collapsed (message hits only). */
  snippet: string | null;
  /** null until stage syncs the session. */
  thread: Thread | null;
  t3DeepLink: string | null;
}

export const PALETTE_SESSION_CAP = 12;
export const PALETTE_MESSAGE_CAP = 8;

const isMessage = (source: MatchSource) => source === "user" || source === "assistant";

/**
 * Local title matches first, in their order; then T3 search hits not already
 * shown (title-ish ones to sessions, message ones to messages). Late search
 * results only append, so rows already on screen never move. `skip` holds
 * thread ids already listed elsewhere (task rows).
 */
export function mergePaletteSessions(
  local: readonly Thread[],
  search: readonly ThreadSearchResult[],
  skip: ReadonlySet<string | null>,
): { sessions: SessionHit[]; messages: SessionHit[] } {
  const seen = new Set<string>();
  const sessions: SessionHit[] = [];
  const messages: SessionHit[] = [];
  for (const th of local) {
    if (skip.has(th.t3ThreadId) || seen.has(th.t3ThreadId)) continue;
    seen.add(th.t3ThreadId);
    sessions.push({
      id: th.t3ThreadId, title: th.title, projectTitle: th.projectTitle, source: "title",
      snippet: null, thread: th, t3DeepLink: th.t3DeepLink,
    });
  }
  for (const r of search) {
    if (skip.has(r.t3ThreadId) || seen.has(r.t3ThreadId)) continue;
    const message = isMessage(r.matchSource);
    const into = message ? messages : sessions;
    if (into.length >= (message ? PALETTE_MESSAGE_CAP : PALETTE_SESSION_CAP)) continue;
    seen.add(r.t3ThreadId);
    into.push({
      id: r.t3ThreadId, title: r.title, projectTitle: r.projectTitle, source: r.matchSource,
      snippet: r.snippet === null ? null : r.snippet.replace(/\s+/g, " ").trim(),
      thread: r.thread, t3DeepLink: r.t3DeepLink,
    });
  }
  return { sessions, messages };
}

/** Hint label for why a session matched; null for plain title matches. */
export function matchLabel(source: MatchSource): string | null {
  switch (source) {
    case "title":
      return null;
    case "user":
      return "you said";
    case "assistant":
      return "agent said";
    case "pr":
      return "PR";
    default:
      return source;
  }
}

/** Quiet note when T3 could not search message text; null when it did. */
export function t3SearchNote(status: T3SearchStatus | undefined): string | null {
  switch (status) {
    case undefined:
    case "ok":
      return null;
    case "timeout":
      return "T3 slow · titles only";
    default:
      return "T3 offline · titles only";
  }
}
