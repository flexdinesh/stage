import type { Handoff, Task } from "../api/types.ts";

type Predecessor = Pick<Task, "afterTaskId" | "afterThreadId" | "afterThreadTitle">;

/** Who a task's handoff would come from: "#12", the session title (id fallback), or null without a predecessor. */
export function handoffSource(t: Predecessor): string | null {
  if (t.afterTaskId !== null) return `#${t.afterTaskId}`;
  if (t.afterThreadId !== null) return t.afterThreadTitle ?? t.afterThreadId;
  return null;
}

/** Checkbox hint: what the toggle appends, naming the predecessor when known. */
export function handoffHint(t: Pick<Task, "afterTaskId" | "afterThreadId">): string {
  const whose = t.afterTaskId !== null ? `#${t.afterTaskId}'s` : t.afterThreadId !== null ? "the session's" : "the predecessor's";
  return `Appends ${whose} final reply, PRs, branch and thread link at launch.`;
}

/** Prompt panel note for a backlog task that will append a handoff at launch; null otherwise. */
export function pendingHandoffNote(t: Predecessor & Pick<Task, "state" | "handoff">): string | null {
  const source = handoffSource(t);
  if (t.state !== "backlog" || !t.handoff || source === null) return null;
  return `+ on launch: handoff from ${source} (final reply, PRs, branch, thread link)`;
}

/** Who a recorded handoff came from: "#12" or the session title (id fallback). */
export function recordedSource(h: Pick<Handoff, "sourceTaskId" | "sourceThreadId" | "sourceTitle">): string {
  if (h.sourceTaskId !== null) return `#${h.sourceTaskId}`;
  return h.sourceTitle === "" ? h.sourceThreadId : h.sourceTitle;
}

/** One-line summary of a sent handoff: "1,234 chars appended to the prompt". */
export function handoffSummary(h: Pick<Handoff, "text">): string {
  return `${h.text.length.toLocaleString("en-US")} chars appended to the prompt`;
}
