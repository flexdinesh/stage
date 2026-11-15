// Transcript model (docs/api.md "Transcript"): REST pages merged with SSE
// upserts, keyed by T3 timeline position.
import { THREAD_STATUSES, TRANSCRIPT_KINDS, type ThreadStatus, type TranscriptItem, type TranscriptPage, type TranscriptState } from "../api/types.ts";

export interface TranscriptModel {
  /** Ascending by position, unique positions. */
  items: TranscriptItem[];
  hasEarlier: boolean;
  /** Stream resume point: every item at or below it is final. -1 = from the start. */
  cursor: number;
  active: boolean;
  status: ThreadStatus | null;
  error: string | null;
}

export type TranscriptAction =
  | { type: "tail"; page: TranscriptPage }
  | { type: "earlier"; page: TranscriptPage }
  | { type: "item"; item: TranscriptItem }
  | { type: "state"; state: TranscriptState };

export const emptyTranscript: TranscriptModel = { items: [], hasEarlier: false, cursor: -1, active: false, status: null, error: null };

/** Highest position p >= cursor such that no item at or below p (above cursor) is streaming. */
export function advanceCursor(cursor: number, items: readonly TranscriptItem[]): number {
  let next = cursor;
  for (const item of items) {
    if (item.position <= next) continue;
    if (item.streaming) break;
    next = item.position;
  }
  return next;
}

/** Inserts or replaces by position; an older copy (earlier updatedAt) never replaces a newer one. */
export function upsertItem(items: readonly TranscriptItem[], item: TranscriptItem): TranscriptItem[] {
  const index = items.findIndex((it) => it.position >= item.position);
  if (index === -1) return [...items, item];
  const existing = items[index];
  if (existing === undefined) return [...items, item];
  if (existing.position !== item.position) return [...items.slice(0, index), item, ...items.slice(index)];
  if (Date.parse(item.updatedAt) < Date.parse(existing.updatedAt)) return [...items];
  return [...items.slice(0, index), item, ...items.slice(index + 1)];
}

export function transcriptReducer(model: TranscriptModel, action: TranscriptAction): TranscriptModel {
  switch (action.type) {
    case "tail": {
      const items = action.page.items.reduce<TranscriptItem[]>(upsertItem, []);
      const cursor = action.page.cursor ?? -1;
      return {
        items,
        hasEarlier: action.page.hasEarlier,
        cursor: advanceCursor(cursor, items),
        active: action.page.active,
        status: action.page.status,
        error: null,
      };
    }
    case "earlier": {
      const first = model.items[0];
      const older = action.page.items.filter((it) => first === undefined || it.position < first.position);
      return { ...model, items: [...older.reduce<TranscriptItem[]>(upsertItem, []), ...model.items], hasEarlier: action.page.hasEarlier };
    }
    case "item": {
      const first = model.items[0];
      // Older than the loaded window: it belongs behind "Load earlier", not after a gap.
      if (model.hasEarlier && first !== undefined && action.item.position < first.position) return model;
      const items = upsertItem(model.items, action.item);
      return { ...model, items, cursor: advanceCursor(model.cursor, items) };
    }
    case "state":
      return { ...model, active: action.state.active, status: action.state.status, error: action.state.error };
  }
}

// ---- SSE payload guards ----

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

const isNullableString = (v: unknown): v is string | null => v === null || typeof v === "string";

function isKind(v: unknown): v is TranscriptItem["kind"] {
  return TRANSCRIPT_KINDS.some((k) => k === v);
}

function isThreadStatus(v: unknown): v is ThreadStatus {
  return THREAD_STATUSES.some((s) => s === v);
}

export function parseTranscriptItem(v: unknown): TranscriptItem | null {
  if (!isRecord(v)) return null;
  const { position, itemId, sourceThreadId, inherited, kind, status, streaming, createdBy, runId, text, truncated, updatedAt } = v;
  if (
    typeof position !== "number" ||
    typeof itemId !== "string" ||
    typeof sourceThreadId !== "string" ||
    typeof inherited !== "boolean" ||
    !isKind(kind) ||
    typeof status !== "string" ||
    typeof streaming !== "boolean" ||
    !isNullableString(createdBy) ||
    !isNullableString(runId) ||
    typeof text !== "string" ||
    typeof truncated !== "boolean" ||
    typeof updatedAt !== "string"
  ) {
    return null;
  }
  return { position, itemId, sourceThreadId, inherited, kind, status, streaming, createdBy, runId, text, truncated, updatedAt };
}

export function parseTranscriptState(v: unknown): TranscriptState | null {
  if (!isRecord(v)) return null;
  const { active, status, error } = v;
  if (typeof active !== "boolean" || !isThreadStatus(status) || !isNullableString(error)) return null;
  return { active, status, error };
}

/** JSON.parse that never throws (malformed event data is dropped). */
export function parseJson(text: string): unknown {
  try {
    const value: unknown = JSON.parse(text);
    return value;
  } catch {
    return null;
  }
}

/** Items appended after `seen` (the last position the reader saw at the bottom). */
export function newSince(items: readonly TranscriptItem[], seen: number): number {
  return items.filter((it) => it.position > seen).length;
}
