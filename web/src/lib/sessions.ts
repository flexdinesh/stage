import type { Continuation, ContinuationState, Thread, WaitReason } from "../api/types.ts";
import type { ChipTone } from "../components/ui.tsx";

export const sessionPath = (id: string) => `/sessions/${encodeURIComponent(id)}`;
export const canLinkSession = (thread: Thread) => thread.parentThreadId === null && !thread.archived;
export const canEditContinuation = (entry: Continuation) =>
  entry.sentAt === null && entry.runId === null && entry.messageId === null &&
  (entry.state === "waiting" || entry.state === "paused" || entry.state === "blocked");
export const isPendingContinuation = (entry: Continuation) => entry.state !== "settled" && entry.state !== "cancelled";

export const CONTINUATION_LABELS: Record<ContinuationState, string> = {
  waiting: "waiting", paused: "paused", blocked: "blocked", sending: "sending",
  running: "running", awaiting_settle: "awaiting settle", settled: "settled",
  cancelled: "cancelled", uncertain: "delivery uncertain",
};
export const CONTINUATION_TONES: Record<ContinuationState, ChipTone> = {
  waiting: "idle", paused: "idle", blocked: "bad", sending: "run", running: "run",
  awaiting_settle: "done", settled: "ok", cancelled: "idle", uncertain: "bad",
};
export const WAIT_REASONS: Record<WaitReason, string> = {
  predecessor_unsettled: "The preceding session must be explicitly settled in T3.",
  target_running: "The target session is still running. It must finish before delivery.",
  target_needs_input: "The target session needs input. Resolve its requests in T3.",
  predecessor_failed: "The preceding session failed, was interrupted, or was cancelled. Recover it in T3 before continuing.",
  target_failed: "The target session failed, was interrupted, or was cancelled. Recover it in T3 before continuing.",
  upstream_pending: "The preceding queued continuation must finish and settle first.",
  upstream_cancelled: "An upstream continuation was cancelled. This step stays blocked; it will not be skipped.",
  thread_missing: "A linked session is unavailable. Check its session link and sync T3.",
  t3_offline: "T3 is offline. Reconnect in Settings to check readiness.",
  delivery_uncertain: "Delivery could not be confirmed. Review the target in T3 before taking further action.",
  legacy_conflict: "A session belongs to a legacy task run order. Unlink that order on task detail first.",
};

/** Keep byte-for-byte aligned with the server's continuation send envelope. */
export function continuationPreview(prompt: string, after: Thread): string {
  const link = after.t3WebUrl ?? after.t3AppUrl ?? after.t3DeepLink;
  return `${prompt}\n\n---\nPrerequisite session\nTitle: ${after.title}\nThread ID: ${after.t3ThreadId}${link === null ? "" : `\nT3: ${link}`}`;
}
