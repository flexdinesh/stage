import type { Question, QuestionAnswer, QuestionOption } from "../api/types.ts";

/**
 * Answer drafts for T3 user-input questions. Rules mirror T3's client
 * (packages/client-runtime/src/state/pendingUserInput.ts): custom text wins
 * over a selection, picking an option clears the text, multi-select answers
 * are arrays, and every question needs an answer before submit.
 */
export interface Draft {
  /** Option answers in selection order (value, or trimmed label when the option has none). */
  selected: string[];
  custom: string;
}

/** Hash that lands detail pages on the Needs input panel. */
export const ANSWER_HASH = "answer";

export const optionAnswer = (o: QuestionOption): string => (o.value === undefined || o.value === null ? o.label.trim() : o.value);
export const customAllowed = (q: Question): boolean => q.allowCustomAnswer !== false;
const hasInitial = (q: Question): boolean => q.initialAnswer !== undefined && q.initialAnswer !== null;

export const initialDraft = (q: Question): Draft => ({ selected: [], custom: customAllowed(q) ? (q.initialAnswer ?? "") : "" });

export function isSelected(q: Question, d: Draft, value: string): boolean {
  if (customAllowed(q) && d.custom.trim() !== "") return false;
  return d.selected.includes(value);
}

export function toggleOption(q: Question, d: Draft, value: string): Draft {
  if (!q.options.some((o) => optionAnswer(o) === value)) return d;
  if (q.multiSelect === true) {
    const selected = d.selected.includes(value) ? d.selected.filter((v) => v !== value) : [...d.selected, value];
    return { selected, custom: "" };
  }
  return { selected: [value], custom: "" };
}

export function setCustom(q: Question, d: Draft, custom: string): Draft {
  if (!customAllowed(q)) return d;
  return { custom, selected: hasInitial(q) || custom.trim() !== "" ? [] : d.selected };
}

/** The answer to submit for one question, or null while unanswered. */
export function resolveAnswer(q: Question, d: Draft | undefined): QuestionAnswer | null {
  const draft = d ?? initialDraft(q);
  let custom: string | null = null;
  if (customAllowed(q)) {
    const trimmed = draft.custom.trim();
    custom = hasInitial(q) ? draft.custom : trimmed === "" ? null : trimmed;
  }
  if (custom !== null && custom.length > 0) return custom;
  const valid = draft.selected.filter((v) => q.options.some((o) => optionAnswer(o) === v));
  if (q.multiSelect === true) return valid.length > 0 ? valid : custom;
  return valid[0] ?? custom;
}

/** Every question's answer keyed by id, or null while any is unanswered. */
export function buildAnswers(questions: Question[], drafts: Record<string, Draft>): Record<string, QuestionAnswer> | null {
  const out: Record<string, QuestionAnswer> = {};
  for (const q of questions) {
    const a = resolveAnswer(q, drafts[q.id]);
    if (a === null) return null;
    out[q.id] = a;
  }
  return out;
}

export const answeredCount = (questions: Question[], drafts: Record<string, Draft>): number =>
  questions.filter((q) => resolveAnswer(q, drafts[q.id]) !== null).length;

/** Human text for a sent answer: option labels, or the custom text. */
export function answerText(q: Question, a: QuestionAnswer): string {
  const label = (v: string) => q.options.find((o) => optionAnswer(o) === v)?.label ?? v;
  return Array.isArray(a) ? a.map(label).join(", ") : label(a);
}
