import type { Attachment, ThreadDetail } from "../api/types.ts";

/**
 * Where session detail reads its opening prompt: the linked stage task's
 * prompt and images (no T3 call), else the session's first own message in T3
 * (also for an adopted task, which has no prompt of its own).
 */
export type SessionPromptSource =
  | { kind: "task"; taskId: number; prompt: string; attachments: readonly Attachment[] }
  | { kind: "t3"; threadId: string };

export function sessionPromptSource(detail: Pick<ThreadDetail, "thread" | "task">): SessionPromptSource {
  const { task, thread } = detail;
  if (task !== null && !task.adopted) return { kind: "task", taskId: task.id, prompt: task.prompt, attachments: task.attachments };
  return { kind: "t3", threadId: thread.t3ThreadId };
}
