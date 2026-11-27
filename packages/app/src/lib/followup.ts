// Following up a task's session from Stage (POST /tasks/:id/messages). Any harness whose connection offers operation
// `send` takes one; the harness stays authoritative and refuses a message it can't take now (409 `session_busy`, with
// why). Hooks: api/queries.ts useConnectionOperation, useSendMessage; UI: components/FollowUp.tsx.
import type { TaskState } from "../api/types.ts";

/** Where the composer sits: `lead` under the needs-input alert (the turn ended asking you), `after-reply` under Last word. */
export type FollowUpPlacement = "lead" | "after-reply";

export interface FollowUpView {
  placement: FollowUpPlacement;
  /** Panel title: Reply when the session asked you, else Follow up. */
  title: "Reply" | "Follow up";
  /** The session is working: the draft stays editable, Send waits. */
  busy: boolean;
}

/** States whose session is working: shown, Send disabled. */
const BUSY_STATES: readonly TaskState[] = ["launching", "running"];

/** Whether a task in `state` may show the composer at all (worth reading the connection's capabilities for). */
export const mayFollowUp = (state: TaskState): boolean => state === "needs_input" || state === "done" || BUSY_STATES.includes(state);

/**
 * The composer for a task, or null when it has none: the connection doesn't offer `send` or is retired, the task has
 * no session, its state can't take a message (backlog, failed, interrupted, cancelled), or its session waits
 * on requests (the Requests panel answers those), on requests only its harness answers (`unlisted`: the harness
 * declares `unlistedRequests`), or Stage hasn't read them yet.
 */
export function followUpView(
  task: { state: TaskState; externalSessionId: string | null; connectionActive?: boolean },
  session: { offered: boolean; requestsReady: boolean; requestCount: number; unlisted?: boolean },
): FollowUpView | null {
  if (!session.offered || task.connectionActive === false || task.externalSessionId === null) return null;
  if (task.state === "needs_input") {
    return session.requestsReady && session.requestCount === 0 && session.unlisted !== true ? { placement: "lead", title: "Reply", busy: false } : null;
  }
  if (task.state === "done") return { placement: "after-reply", title: "Follow up", busy: false };
  if (BUSY_STATES.includes(task.state)) return { placement: "after-reply", title: "Follow up", busy: true };
  return null;
}

/** The needs-input alert when the composer leads. */
export const replyPrompt = (harness: string): string => `${harness} is waiting for your reply.`;

/** Why Send waits while the session works. */
export const busyHint = (harness: string): string => `${harness} is working. Send once it finishes.`;

/** The most the server takes (32 KiB of UTF-8). */
export const MAX_MESSAGE_BYTES = 32 * 1024;

/** Whether `text` is over the server's limit. */
export const tooLong = (text: string): boolean => new TextEncoder().encode(text).length > MAX_MESSAGE_BYTES;

/** Whether `text` can be sent: nonblank and within the server's limit. */
export const sendable = (text: string): boolean => text.trim() !== "" && !tooLong(text);

/** Ctrl/Cmd+Enter sends (plain Enter is a newline). */
export const isSendKey = (e: { key: string; ctrlKey: boolean; metaKey: boolean }): boolean => e.key === "Enter" && (e.ctrlKey || e.metaKey);
