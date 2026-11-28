// Answering what a task's session waits on (GET /tasks/:id/requests, POST /tasks/:id/requests/:rid). Any harness
// whose connection offers operation `respond` lists requests; the harness stays authoritative, so its own client (Open
// in / resume command) keeps answering too. Hooks: api/queries.ts useSessionRequests, useRespondRequest.
import { ApiError } from "../api/client.ts";
import type { HarnessChoice, RequestQuestion, RequestQuestionOption, RequestResponse, SessionRequest } from "../api/types.ts";

/** A preview longer than this many lines starts collapsed. */
export const PREVIEW_LINES = 8;

/** The preview's lines, and whether it is long enough to collapse. */
export function previewLines(preview: string): { lines: string[]; long: boolean } {
  const lines = preview.replace(/\n+$/, "").split("\n");
  return { lines, long: lines.length > PREVIEW_LINES };
}

/** What a request offers to pick, in the harness's order: proceed first, decline last. */
export const requestOptions = (r: Pick<SessionRequest, "options">): HarnessChoice[] => r.options ?? [];

/** An option's description to show under its label; null when blank or only repeating the label. */
export function optionNote(o: Pick<RequestQuestionOption, "label" | "description">): string | null {
  const d = o.description?.trim() ?? "";
  return d === "" || d.toLowerCase() === o.label.trim().toLowerCase() ? null : d;
}

/** One question's draft answer: picked option labels, and the free-text Other. */
export interface QuestionDraft {
  picked: string[];
  otherOn: boolean;
  other: string;
}

export type QuestionsDraft = Readonly<Record<string, QuestionDraft>>;

export const emptyDraft = (): QuestionDraft => ({ picked: [], otherOn: false, other: "" });

/** `id`'s draft (empty when untouched). */
export const draftOf = (draft: QuestionsDraft, id: string): QuestionDraft => draft[id] ?? emptyDraft();

/** Picks `label`: a single-select question replaces its answer (and leaves Other); a multi-select one toggles it. */
export function pick(q: Pick<RequestQuestion, "multi">, d: QuestionDraft, label: string): QuestionDraft {
  if (q.multi !== true) return { ...d, picked: [label], otherOn: false };
  return { ...d, picked: d.picked.includes(label) ? d.picked.filter((l) => l !== label) : [...d.picked, label] };
}

/** Turns Other on (a single-select question drops its picked option) or, for a multi-select one, toggles it. */
export function pickOther(q: Pick<RequestQuestion, "multi">, d: QuestionDraft): QuestionDraft {
  if (q.multi !== true) return { ...d, picked: [], otherOn: true };
  return { ...d, otherOn: !d.otherOn };
}

/** A question answered only in free text: it takes custom answers and offers no options, so it shows one text field. */
export const freeText = (q: Pick<RequestQuestion, "custom" | "options">): boolean => q.custom === true && (q.options ?? []).length === 0;

/** The answer a question sends: its picked labels, then the Other text when it is on (always, for free text) and not blank. */
export function answerOf(q: Pick<RequestQuestion, "custom" | "options">, d: QuestionDraft): string[] {
  const other = q.custom === true && (d.otherOn || freeText(q)) ? d.other.trim() : "";
  return other === "" ? d.picked : [...d.picked, other];
}

/** Every question has an answer: Send is enabled. */
export function allAnswered(questions: readonly RequestQuestion[], draft: QuestionsDraft): boolean {
  return questions.length > 0 && questions.every((q) => answerOf(q, draftOf(draft, q.id)).length > 0);
}

/** The response that answers every question of a question request. */
export function answersResponse(questions: readonly RequestQuestion[], draft: QuestionsDraft): RequestResponse {
  return { answers: Object.fromEntries(questions.map((q) => [q.id, answerOf(q, draftOf(draft, q.id))])) };
}

/** The response that picks `option`, with `text` when given and not blank. */
export function optionResponse(option: string, text = ""): RequestResponse {
  const t = text.trim();
  return t === "" ? { option } : { option, text: t };
}

/**
 * A row's answer action. `inline`: exactly one pending approval, answered on the row with its first option (proceed)
 * and, when it offers more, its last (decline); the rest stay on the task. `answer`: a question or several requests,
 * answered on the task (`/tasks/:id#requests`). Null: nothing pending.
 */
export type RowRequestAction =
  | { kind: "inline"; request: SessionRequest; proceed: HarnessChoice; decline: HarnessChoice | null }
  | { kind: "answer"; count: number };

export function rowRequestAction(requests: readonly SessionRequest[]): RowRequestAction | null {
  const only = requests.length === 1 ? requests[0] : undefined;
  if (only !== undefined && only.kind === "approval") {
    const options = requestOptions(only);
    const proceed = options[0];
    const last = options.at(-1);
    if (proceed !== undefined) return { kind: "inline", request: only, proceed, decline: last === undefined || last === proceed ? null : last };
  }
  return requests.length === 0 ? null : { kind: "answer", count: requests.length };
}

/** A row's one-line summary of a request: its title, then its preview's first line ("Run a command · npm test"). */
export function requestSummary(r: Pick<SessionRequest, "title" | "preview">): string {
  const first = r.preview?.split("\n").find((l) => l.trim() !== "")?.trim() ?? "";
  return first === "" ? r.title : `${r.title} · ${first}`;
}

/** The task-detail anchor of its requests panel. */
export const REQUESTS_ANCHOR = "requests";

export const requestsPath = (taskId: number) => `/tasks/${taskId}#${REQUESTS_ANCHOR}`;

/** A failed answer: `expired` when the request is no longer pending (answered in the harness, timed out), else an error to show. */
export const respondFailure = (error: unknown): "expired" | "error" => (error instanceof ApiError && error.status === 404 ? "expired" : "error");

/** Copy for answered or expired requests; `harness` is its display name. */
export const answeredText = (harness: string) => `Answered · waiting for ${harness}`;
export const expiredText = (harness: string) => `Already answered in ${harness} or expired`;
