import type { JournalEntry, JournalOwner, NoteCategory } from "../api/types.ts";

export const MAX_NOTE_BYTES = 16 * 1024;

export const NOTE_CATEGORIES: { value: Exclude<NoteCategory, "">; label: string }[] = [
  { value: "finding", label: "Finding" },
  { value: "decision", label: "Decision" },
  { value: "blocker", label: "Blocker" },
  { value: "outcome", label: "Outcome" },
];

export function isNoteCategory(value: string): value is NoteCategory {
  return value === "" || NOTE_CATEGORIES.some((category) => category.value === value);
}

export function noteBytes(text: string): number {
  return new TextEncoder().encode(text).length;
}

export interface NoteRequest {
  text: string;
  category: NoteCategory;
  requestId: string;
}

/** Correlation key, not a credential. Plain JS works in every host, including HTTP on a private network. */
function requestKey(): string {
  const random = Array.from({ length: 4 }, () => Math.floor(Math.random() * 0x100000000).toString(16).padStart(8, "0")).join("");
  return `note-${Date.now().toString(36)}-${random}`;
}

/** Preserve the key after an ambiguous failure; changed content is a new append. */
export function noteRequest(previous: NoteRequest | null, text: string, category: NoteCategory): NoteRequest {
  if (previous !== null && previous.text === text && previous.category === category) return previous;
  return { text, category, requestId: requestKey() };
}

export function journalEntryPath(entry: JournalEntry): string {
  const query = `entry=${entry.id}#journal-${entry.id}`;
  if (entry.taskId !== null) return `/tasks/${entry.taskId}?${query}`;
  return `/trees/${entry.treeId}?view=notes&${query}`;
}

export function journalOwnerKey(owner: JournalOwner): string {
  return owner.taskId === undefined ? `tree-${owner.treeId}` : `task-${owner.taskId}`;
}

export function journalEntryId(value: string | null): number | null {
  if (value === null || !/^[1-9]\d*$/.test(value)) return null;
  const id = Number(value);
  return Number.isSafeInteger(id) ? id : null;
}
