import type { LaunchBase, Task, Workspace } from "../api/types.ts";

type Predecessor = Pick<Task, "needs"> & { workspace: Workspace };

/** Task form workspace choice: root, worktree from a ref, or worktree from a need's branch. */
export type WorkspaceChoice = "root" | "worktree" | "predecessor";

/**
 * The need a worktree would stack on: workspace.baseTaskId when it is a need, else the sole need ("#12");
 * "a need" with several needs and none picked (launch requires a choice); null without needs.
 */
export function predecessorName(t: Predecessor): string | null {
  const w = t.workspace;
  const picked = w.type === "worktree" ? (w.baseTaskId ?? null) : null;
  if (picked !== null && t.needs.includes(picked)) return `#${picked}`;
  const [only] = t.needs;
  if (t.needs.length === 1 && only !== undefined) return `#${only}`;
  return t.needs.length === 0 ? null : "a need";
}

/** "#12's branch", or "a need's branch" when there is no need to name. */
export function branchLabel(name: string | null): string {
  return `${name ?? "a need"}'s branch`;
}

/** True when the workspace starts from a need's branch. */
export function fromPredecessor(w: Workspace): boolean {
  return w.type === "worktree" && w.base === "predecessor";
}

export function workspaceChoice(w: Workspace): WorkspaceChoice {
  if (w.type === "root") return "root";
  return fromPredecessor(w) ? "predecessor" : "worktree";
}

/** Workspace segmented options; the need's-branch cell only when the task has needs. */
export function workspaceOptions(name: string | null, retainPredecessor = false): { value: WorkspaceChoice; label: string }[] {
  const base: { value: WorkspaceChoice; label: string }[] = [
    { value: "root", label: "root" },
    { value: "worktree", label: "worktree" },
  ];
  return name === null && !retainPredecessor ? base : [...base, { value: "predecessor", label: name === null ? "Predecessor unavailable" : branchLabel(name) }];
}

/** Keep the requested base until the user explicitly changes it. */
export function effectiveChoice(choice: WorkspaceChoice, _name: string | null): WorkspaceChoice {
  return choice;
}

/** The form's workspace; baseTaskId (kept from the task) applies only to a predecessor choice. */
export function choiceToWorkspace(choice: WorkspaceChoice, baseRef: string, branch: string, baseTaskId: number | null = null): Workspace {
  if (choice === "root") return { type: "root" };
  const b = branch.trim();
  const baseRefT = baseRef.trim();
  const br = b === "" ? null : b;
  if (choice === "predecessor") return { type: "worktree", baseRef: baseRefT, branch: br, base: "predecessor", baseTaskId };
  return { type: "worktree", baseRef: baseRefT, branch: br, base: "ref" };
}

/** Who a recorded base came from: "#12" or the session title (id fallback). */
function recordedSource(b: LaunchBase): string {
  if (b.sourceTaskId !== null) return `#${b.sourceTaskId}`;
  return b.sourceTitle !== "" ? b.sourceTitle : (b.sourceThreadId ?? "need");
}

/**
 * Config panel workspace line for a predecessor-base task: the resolved base once launched
 * ("worktree from feat-x (#12)" / "worktree from origin/main · fallback"), else what it will use
 * ("worktree from #12's branch · fallback origin/main"). null for other workspaces.
 */
export function predecessorWorkspaceText(t: Predecessor, base: LaunchBase | null): string | null {
  const w = t.workspace;
  if (w.type !== "worktree" || !fromPredecessor(w)) return null;
  const into = w.branch === null ? "" : ` → ${w.branch}`;
  if (base === null) return `worktree from ${branchLabel(predecessorName(t))} · requires available branch${into}`;
  if (base.source === "predecessor") return `worktree from ${base.baseRef} (${recordedSource(base)})${into}`;
  return `worktree from ${base.baseRef} · fallback${into}`;
}

/** Header chip text for a predecessor-base task with no worktree branch known yet. */
export function predecessorChip(t: Predecessor, base: LaunchBase | null): string | null {
  if (!fromPredecessor(t.workspace)) return null;
  if (base !== null) return `worktree from ${base.baseRef}`;
  return `worktree from ${branchLabel(predecessorName(t))}`;
}

/** Prompt panel note for a backlog task with needs that will stack on a need's branch; null otherwise. */
export function pendingBaseNote(t: Predecessor & Pick<Task, "state">): string | null {
  const name = predecessorName(t);
  const w = t.workspace;
  if (t.state !== "backlog" || name === null || w.type !== "worktree" || !fromPredecessor(w)) return null;
  return `+ on launch: worktree from ${branchLabel(name)} (requires available branch)`;
}
