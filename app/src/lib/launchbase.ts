import type { LaunchBase, Task, Workspace } from "../api/types.ts";

type Predecessor = Pick<Task, "afterTaskId" | "afterThreadId">;

/** Task form workspace choice: root, worktree from a ref, or worktree from the predecessor's branch. */
export type WorkspaceChoice = "root" | "worktree" | "predecessor";

/** The predecessor a worktree could stack on: "#12", "session", or null when unlinked. */
export function predecessorName(t: Predecessor): string | null {
  if (t.afterTaskId !== null) return `#${t.afterTaskId}`;
  if (t.afterThreadId !== null) return "session";
  return null;
}

/** "#12's branch", "session's branch", or "predecessor's branch" when unlinked. */
export function branchLabel(name: string | null): string {
  return `${name ?? "predecessor"}'s branch`;
}

/** True when the workspace starts from the predecessor's branch. */
export function fromPredecessor(w: Workspace): boolean {
  return w.type === "worktree" && w.base === "predecessor";
}

export function workspaceChoice(w: Workspace): WorkspaceChoice {
  if (w.type === "root") return "root";
  return fromPredecessor(w) ? "predecessor" : "worktree";
}

/** Workspace segmented options; the predecessor cell only when there is a predecessor to name. */
export function workspaceOptions(name: string | null): { value: WorkspaceChoice; label: string }[] {
  const base: { value: WorkspaceChoice; label: string }[] = [
    { value: "root", label: "root" },
    { value: "worktree", label: "worktree" },
  ];
  return name === null ? base : [...base, { value: "predecessor", label: branchLabel(name) }];
}

/** The choice in effect: a predecessor choice falls back to worktree once there is no predecessor (the cell is hidden). */
export function effectiveChoice(choice: WorkspaceChoice, name: string | null): WorkspaceChoice {
  return choice === "predecessor" && name === null ? "worktree" : choice;
}

export function choiceToWorkspace(choice: WorkspaceChoice, baseRef: string, branch: string): Workspace {
  if (choice === "root") return { type: "root" };
  const b = branch.trim();
  return { type: "worktree", baseRef: baseRef.trim(), branch: b === "" ? null : b, base: choice === "predecessor" ? "predecessor" : "ref" };
}

/** Who a recorded base came from: "#12" or the session title (id fallback). */
function recordedSource(b: LaunchBase): string {
  if (b.sourceTaskId !== null) return `#${b.sourceTaskId}`;
  return b.sourceTitle !== "" ? b.sourceTitle : (b.sourceThreadId ?? "predecessor");
}

/**
 * Config panel workspace line for a predecessor-base task: the resolved base once launched
 * ("worktree from feat-x (#12)" / "worktree from origin/main · fallback"), else what it will use
 * ("worktree from #12's branch · fallback origin/main"). null for other workspaces.
 */
export function predecessorWorkspaceText(t: Predecessor & { workspace: Workspace }, base: LaunchBase | null): string | null {
  const w = t.workspace;
  if (w.type !== "worktree" || !fromPredecessor(w)) return null;
  const into = w.branch === null ? "" : ` → ${w.branch}`;
  if (base === null) return `worktree from ${branchLabel(predecessorName(t))} · fallback ${w.baseRef}${into}`;
  if (base.source === "predecessor") return `worktree from ${base.baseRef} (${recordedSource(base)})${into}`;
  return `worktree from ${base.baseRef} · fallback${into}`;
}

/** Header chip text for a predecessor-base task with no worktree branch known yet. */
export function predecessorChip(t: Predecessor & { workspace: Workspace }, base: LaunchBase | null): string | null {
  if (!fromPredecessor(t.workspace)) return null;
  if (base !== null) return `worktree from ${base.baseRef}`;
  return `worktree from ${branchLabel(predecessorName(t))}`;
}

/** Prompt panel note for a waiting backlog task that will stack on its predecessor's branch; null otherwise. */
export function pendingBaseNote(t: Predecessor & Pick<Task, "state" | "workspace">): string | null {
  const name = predecessorName(t);
  const w = t.workspace;
  if (t.state !== "backlog" || name === null || w.type !== "worktree" || !fromPredecessor(w)) return null;
  return `+ on launch: worktree from ${branchLabel(name)} (fallback ${w.baseRef})`;
}
