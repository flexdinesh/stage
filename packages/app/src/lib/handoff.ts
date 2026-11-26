import type { Handoff, Task } from "../api/types.ts";

type Needs = Pick<Task, "needs">;

/** Who a task's handoff would come from: "#12", "#3, #5", or null without needs. */
export function handoffSource(t: Needs): string | null {
  return t.needs.length === 0 ? null : t.needs.map((id) => `#${id}`).join(", ");
}

/** Checkbox hint: what the toggle appends, naming the need when there is one. */
export function handoffHint(t: Needs): string {
  const [only] = t.needs;
  const whose = t.needs.length === 1 && only !== undefined ? `#${only}'s` : "each need's";
  return `Appends ${whose} accepted result and available references from Stage at launch.`;
}

/** Prompt panel note for a backlog task that will append a handoff at launch; null otherwise. */
export function pendingHandoffNote(t: Needs & Pick<Task, "state" | "handoff">): string | null {
  const source = handoffSource(t);
  if (t.state !== "backlog" || !t.handoff || source === null) return null;
  return `+ on launch: handoff from ${source} (accepted result and available references)`;
}

/** Who a recorded handoff came from: "#12" or the session title (id fallback). */
export function recordedSource(h: Pick<Handoff, "sourceTaskId" | "session" | "sourceTitle">): string {
  if (h.sourceTaskId !== null) return `#${h.sourceTaskId}`;
  return h.sourceTitle === "" ? h.session?.externalSessionId ?? "" : h.sourceTitle;
}

/** One-line summary of a sent handoff: "1,234 chars appended to the prompt". */
export function handoffSummary(h: Pick<Handoff, "text">): string {
  return `${h.text.length.toLocaleString("en-US")} chars appended to the prompt`;
}
